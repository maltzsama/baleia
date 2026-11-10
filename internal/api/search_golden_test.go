package api

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/store/model"
	"github.com/galileostudio/baleia/internal/store/repo"
	"github.com/google/uuid"
)

// The golden search set (#798, #941): a corpus of tables, columns, docs and
// content, and the queries people type with the results they expect. The
// harness reports recall@10 and the zero-result rate over the real handlers
// and fails when either is worse than the recorded baseline. Set
// BALEIA_SEARCH_GOLDEN_UPDATE=1 to record the current numbers instead.
const (
	goldenCorpusFile   = "testdata/search_golden.json"
	goldenBaselineFile = "testdata/search_golden_baseline.json"
	goldenTop          = 10
)

type goldenColumn struct {
	Name    string  `json:"name"`
	Comment *string `json:"comment"`
}

type goldenTable struct {
	Namespace []string       `json:"namespace"`
	Name      string         `json:"name"`
	Comment   *string        `json:"comment"`
	Columns   []goldenColumn `json:"columns"`
}

type goldenDoc struct {
	Namespace   []string `json:"namespace"`
	Table       string   `json:"table"`
	Column      *string  `json:"column"`
	Description string   `json:"description"`
}

type goldenContent struct {
	Name      string `json:"name"`
	Label     string `json:"label"`
	Slug      string `json:"slug"`
	Certified bool   `json:"certified"`
}

type goldenQuery struct {
	Surface string   `json:"surface"`
	Q       string   `json:"q"`
	Expect  []string `json:"expect"`
}

type goldenCorpus struct {
	Tables        []goldenTable   `json:"tables"`
	Docs          []goldenDoc     `json:"docs"`
	Folders       []goldenContent `json:"folders"`
	Queries       []goldenContent `json:"queries"`
	FillerQueries struct {
		Name  string `json:"name"`
		Slug  string `json:"slug"`
		Count int    `json:"count"`
	} `json:"filler_queries"`
	Metrics []goldenContent `json:"metrics"`
	Models  []goldenContent `json:"models"`
	Golden  []goldenQuery   `json:"golden"`
}

// goldenReport is what the baseline file records.
type goldenReport struct {
	Queries        int     `json:"queries"`
	RecallAt10     float64 `json:"recall_at_10"`
	ZeroResultRate float64 `json:"zero_result_rate"`
}

func TestSearchGolden(t *testing.T) {
	t.Parallel()
	corpus := readGoldenCorpus(t)
	f := newMetaFixture(t)
	seedGoldenCorpus(t, f, corpus)
	ctx := f.as(f.owner)

	recall, zero := 0.0, 0
	for _, g := range corpus.Golden {
		got := goldenSearch(ctx, t, f, g)
		if len(got) == 0 {
			zero++
		}
		top := map[string]bool{}
		for i, key := range got {
			if i == goldenTop {
				break
			}
			top[key] = true
		}
		found := 0
		for _, want := range g.Expect {
			if top[want] {
				found++
			}
		}
		recall += float64(found) / float64(len(g.Expect))
		if found < len(g.Expect) {
			t.Logf("miss %s %q: found %d/%d, top = %v", g.Surface, g.Q, found, len(g.Expect), firstN(got, goldenTop))
		}
	}
	n := len(corpus.Golden)
	report := goldenReport{
		Queries:        n,
		RecallAt10:     round(recall / float64(n)),
		ZeroResultRate: round(float64(zero) / float64(n)),
	}
	t.Logf("golden set: %d queries, recall@10 %.3f, zero-result rate %.3f", n, report.RecallAt10, report.ZeroResultRate)

	if os.Getenv("BALEIA_SEARCH_GOLDEN_UPDATE") == "1" {
		writeGoldenBaseline(t, report)
		return
	}
	baseline := readGoldenBaseline(t)
	if report.Queries != baseline.Queries {
		t.Fatalf("the golden set has %d queries, the baseline %d: record it again", report.Queries, baseline.Queries)
	}
	if report.RecallAt10 < baseline.RecallAt10 {
		t.Errorf("recall@10 %.3f is below the baseline %.3f", report.RecallAt10, baseline.RecallAt10)
	}
	if report.ZeroResultRate > baseline.ZeroResultRate {
		t.Errorf("zero-result rate %.3f is above the baseline %.3f", report.ZeroResultRate, baseline.ZeroResultRate)
	}
}

// goldenSearch runs one golden query on its surface and returns the result
// keys in rank order.
func goldenSearch(ctx context.Context, t *testing.T, f *metaFixture, g goldenQuery) []string {
	t.Helper()
	limit := goldenTop
	keys := []string{}
	switch g.Surface {
	case "metadata":
		r, err := f.h.SearchMetadata(ctx, SearchMetadataRequestObject{Id: f.ws, Params: SearchMetadataParams{Q: g.Q, Limit: &limit}})
		if err != nil {
			t.Fatal(err)
		}
		hits, ok := r.(SearchMetadata200JSONResponse)
		if !ok {
			t.Fatalf("metadata %q: %T", g.Q, r)
		}
		for _, h := range hits {
			key := string(h.Kind) + ":" + strings.Join(h.Namespace, ".") + "." + h.Table
			if h.Column != nil {
				key += "." + *h.Column
			}
			keys = append(keys, key)
		}
	case "content":
		r, err := f.h.SearchContent(ctx, SearchContentRequestObject{Id: f.ws, Params: SearchContentParams{Q: g.Q, Limit: &limit}})
		if err != nil {
			t.Fatal(err)
		}
		hits, ok := r.(SearchContent200JSONResponse)
		if !ok {
			t.Fatalf("content %q: %T", g.Q, r)
		}
		for _, h := range hits {
			keys = append(keys, h.Kind+":"+h.Slug)
		}
	case "all":
		r, err := f.h.SearchWorkspace(ctx, SearchWorkspaceRequestObject{Id: f.ws, Params: SearchWorkspaceParams{Q: g.Q, Limit: &limit}})
		if err != nil {
			t.Fatal(err)
		}
		res, ok := r.(SearchWorkspace200JSONResponse)
		if !ok {
			t.Fatalf("all %q: %T", g.Q, r)
		}
		for _, item := range res.Items {
			keys = append(keys, federatedKey(item))
		}
	case "docs":
		r, err := f.h.SearchSemanticDocs(ctx, SearchSemanticDocsRequestObject{Id: f.ws, Params: SearchSemanticDocsParams{Q: g.Q, Limit: &limit}})
		if err != nil {
			t.Fatal(err)
		}
		hits, ok := r.(SearchSemanticDocs200JSONResponse)
		if !ok {
			t.Fatalf("docs %q: %T", g.Q, r)
		}
		for _, h := range hits {
			key := "doc:" + strings.Join(h.Namespace, ".") + "." + h.Table
			if h.Column != nil {
				key += "." + *h.Column
			}
			keys = append(keys, key)
		}
	default:
		t.Fatalf("unknown surface %q", g.Surface)
	}
	return keys
}

// seedGoldenCorpus adds the corpus to the fixture's granted catalog and the
// owner's default space.
func seedGoldenCorpus(t *testing.T, f *metaFixture, c goldenCorpus) {
	t.Helper()
	create := func(row any) {
		t.Helper()
		if err := f.gdb.Create(row).Error; err != nil {
			t.Fatalf("create %T: %v", row, err)
		}
	}
	ws := model.UUID(uuid.UUID(f.ws).String())
	lake := model.UUID(uuid.UUID(f.lake).String())
	owner := model.UUID(f.owner)
	var space model.Space
	if err := f.gdb.Where("workspace_id = ? AND is_default", ws).First(&space).Error; err != nil {
		t.Fatalf("default space: %v", err)
	}
	now := time.Now()

	for _, table := range c.Tables {
		row := model.MetadataTable{
			ID: model.UUID(repo.NewUUID()), CatalogID: lake, Namespace: table.Namespace, Name: model.Text(table.Name),
			Kind: "table", Fingerprint: "f", LastSeenAt: now, LastChangedAt: now, Comment: table.Comment,
		}
		create(&row)
		for i, column := range table.Columns {
			create(&model.MetadataColumn{
				TableID: row.ID, Ordinal: int32(i + 1), Name: model.Text(column.Name),
				NativeType: "string", TypeSystem: "iceberg", Comment: column.Comment,
			})
		}
	}
	for _, doc := range c.Docs {
		row := model.SemanticDoc{
			ID: model.UUID(repo.NewUUID()), WorkspaceID: ws, CatalogID: lake, Namespace: doc.Namespace,
			TableNameValue: model.Text(doc.Table), Description: model.Text(doc.Description), Tags: model.StringArray{},
			UpdatedBy: owner,
		}
		if doc.Column != nil {
			column := model.Text(*doc.Column)
			row.ColumnName = &column
		}
		create(&row)
	}
	for _, folder := range c.Folders {
		create(&model.QueryFolder{
			ID: model.UUID(repo.NewUUID()), WorkspaceID: ws, SpaceID: space.ID, Name: folder.Name, Slug: model.Text(folder.Slug),
		})
	}
	queries := append([]goldenContent{}, c.Queries...)
	for i := 1; i <= c.FillerQueries.Count; i++ {
		queries = append(queries, goldenContent{
			Name: fmt.Sprintf(c.FillerQueries.Name, i), Slug: fmt.Sprintf(c.FillerQueries.Slug, i),
		})
	}
	for _, q := range queries {
		create(&model.SavedQuery{
			ID: model.UUID(repo.NewUUID()), WorkspaceID: ws, SpaceID: space.ID, CreatedBy: owner,
			Name: model.Text(q.Name), Slug: model.Text(q.Slug), SQL: "SELECT 1",
		})
	}
	definition := model.JSON(`{"type":"simple","aggregation":"count"}`)
	for _, m := range c.Metrics {
		create(&model.SemanticMetric{
			ID: model.UUID(repo.NewUUID()), WorkspaceID: ws, SpaceID: space.ID, Slug: model.Text(m.Slug), Label: m.Label,
			Certified: m.Certified, CatalogID: lake, Namespace: model.StringArray{"sales"}, TableNameValue: "orders",
			Definition: definition, UpdatedBy: owner,
		})
	}
	for _, m := range c.Models {
		create(&model.SemanticModel{
			ID: model.UUID(repo.NewUUID()), WorkspaceID: ws, SpaceID: space.ID, Slug: model.Text(m.Slug), Label: m.Label,
			Certified: m.Certified, CatalogID: lake, Definition: model.JSON(`{}`), UpdatedBy: owner,
		})
	}
}

func readGoldenCorpus(t *testing.T) goldenCorpus {
	t.Helper()
	raw, err := os.ReadFile(goldenCorpusFile)
	if err != nil {
		t.Fatal(err)
	}
	var c goldenCorpus
	if err := json.Unmarshal(raw, &c); err != nil {
		t.Fatalf("%s: %v", goldenCorpusFile, err)
	}
	return c
}

func readGoldenBaseline(t *testing.T) goldenReport {
	t.Helper()
	raw, err := os.ReadFile(goldenBaselineFile)
	if err != nil {
		t.Fatalf("%v: record it with BALEIA_SEARCH_GOLDEN_UPDATE=1", err)
	}
	var r goldenReport
	if err := json.Unmarshal(raw, &r); err != nil {
		t.Fatalf("%s: %v", goldenBaselineFile, err)
	}
	return r
}

func writeGoldenBaseline(t *testing.T, r goldenReport) {
	t.Helper()
	raw, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(goldenBaselineFile, append(raw, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("recorded %s", goldenBaselineFile)
}

// federatedKey names a federated result like the surface it came from does.
func federatedKey(item WorkspaceSearchItem) string {
	switch {
	case item.Metadata != nil:
		key := string(item.Kind) + ":" + strings.Join(item.Metadata.Namespace, ".") + "." + item.Metadata.Table
		if item.Metadata.Column != nil {
			key += "." + *item.Metadata.Column
		}
		return key
	case item.Doc != nil:
		key := "doc:" + strings.Join(item.Doc.Namespace, ".") + "." + item.Doc.Table
		if item.Doc.Column != nil {
			key += "." + *item.Doc.Column
		}
		return key
	case item.Content != nil:
		return string(item.Kind) + ":" + item.Content.Slug
	}
	return string(item.Kind) + ":" + item.Id
}

func firstN(values []string, n int) []string {
	if len(values) > n {
		return values[:n]
	}
	return values
}

// round keeps three decimals so the baseline file stays stable.
func round(v float64) float64 { return float64(int(v*1000+0.5)) / 1000 }
