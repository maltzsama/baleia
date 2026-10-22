package content

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/dialect"
	"github.com/galileostudio/baleia/internal/policy"
	"github.com/galileostudio/baleia/internal/semantic"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// Bundle format v1 (#34, ADR 0013): content-as-code. Repeated exports of
// unchanged content are byte-identical; imports upsert by (kind, slug) and
// never delete items the bundle omits.
const (
	BundleFormat  = "baleia.bundle"
	BundleVersion = 1

	KindFolder         = "folder"
	KindSavedQuery     = "saved_query"
	KindSemanticMetric = "semantic_metric"
	KindModel          = "semantic_model"

	// maxFormatVersion bounds a bundle's per-item format version before it is
	// narrowed to the int32 stored in the database.
	maxFormatVersion = 1<<31 - 1
)

// Bundle is the portable envelope.
type Bundle struct {
	Format        string       `json:"format"`
	FormatVersion int          `json:"format_version"`
	BaleiaVersion string       `json:"baleia_version"`
	Space         BundleSpace  `json:"space"`
	Items         []BundleItem `json:"items"`
}

// BundleSpace identifies the source space (the destination space is the path).
type BundleSpace struct {
	Slug string `json:"slug"`
	Name string `json:"name"`
}

// BundleItem is one content item. Fields not used by its kind are omitted.
type BundleItem struct {
	Kind string `json:"kind"`
	Slug string `json:"slug"`

	// folder
	Parent *string `json:"parent,omitempty"`
	Name   string  `json:"name,omitempty"`

	// saved_query
	Folder *string       `json:"folder,omitempty"`
	SQL    string        `json:"sql,omitempty"`
	Target *BundleTarget `json:"target,omitempty"`

	// semantic_metric
	MetricFormatVersion int             `json:"format_version,omitempty"`
	Label               string          `json:"label,omitempty"`
	Description         string          `json:"description,omitempty"`
	Certified           bool            `json:"certified,omitempty"`
	Catalog             string          `json:"catalog,omitempty"`
	Namespace           []string        `json:"namespace,omitempty"`
	Table               string          `json:"table,omitempty"`
	Definition          json.RawMessage `json:"definition,omitempty"`

	// semantic_model
	ModelFormatVersion int `json:"model_format_version,omitempty"`
}

// BundleTarget is the portable target of a saved query.
type BundleTarget struct {
	Engine  string  `json:"engine,omitempty"`
	Catalog string  `json:"catalog"`
	Schema  *string `json:"schema,omitempty"`
}

// Marshal encodes the bundle deterministically: struct field order, two-space
// indent, trailing newline.
func (b Bundle) Marshal() ([]byte, error) {
	raw, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(raw, '\n'), nil
}

// Export builds the bundle of one space. The caller must have viewer access.
func (s *Service) Export(ctx context.Context, workspaceID, spaceID pgtype.UUID, baleiaVersion string) (Bundle, error) {
	space, err := s.q.GetSpace(ctx, db.GetSpaceParams{WorkspaceID: workspaceID, ID: spaceID})
	if err != nil {
		return Bundle{}, err
	}
	folders, err := s.q.ListSpaceFolders(ctx, spaceID)
	if err != nil {
		return Bundle{}, err
	}
	queries, err := s.q.ListSpaceSavedQueries(ctx, spaceID)
	if err != nil {
		return Bundle{}, err
	}
	metrics, err := s.q.ListSpaceMetrics(ctx, spaceID)
	if err != nil {
		return Bundle{}, err
	}
	models, err := s.q.ListSpaceModels(ctx, spaceID)
	if err != nil {
		return Bundle{}, err
	}

	b := Bundle{
		Format: BundleFormat, FormatVersion: BundleVersion, BaleiaVersion: baleiaVersion,
		Space: BundleSpace{Slug: space.Slug, Name: space.Name},
	}
	slugByID := map[string]string{}
	for _, f := range folders {
		slugByID[uuidKey(f.ID)] = f.Slug
	}
	for _, f := range orderFolders(folders) {
		item := BundleItem{Kind: KindFolder, Slug: f.Slug, Name: f.Name}
		if f.ParentID.Valid {
			if parent, ok := slugByID[uuidKey(f.ParentID)]; ok {
				item.Parent = &parent
			}
		}
		b.Items = append(b.Items, item)
	}
	for _, q := range queries {
		item := BundleItem{Kind: KindSavedQuery, Slug: q.Slug, Name: q.Name, SQL: q.Sql}
		if q.FolderID.Valid {
			if folder, ok := slugByID[uuidKey(q.FolderID)]; ok {
				item.Folder = &folder
			}
		}
		if q.TargetBindingID.Valid {
			if names, err := s.q.GetBindingTargetNames(ctx, q.TargetBindingID); err == nil {
				item.Target = &BundleTarget{Engine: names.EngineName, Catalog: names.CatalogName, Schema: q.TargetSchema}
			}
		}
		b.Items = append(b.Items, item)
	}
	for _, m := range metrics {
		name, err := s.q.GetCatalogNameByID(ctx, m.CatalogID)
		if err != nil {
			return Bundle{}, err
		}
		b.Items = append(b.Items, BundleItem{
			Kind: KindSemanticMetric, Slug: m.Slug, Label: m.Label, Description: m.Description,
			Certified: m.Certified, Catalog: name, Namespace: m.Namespace, Table: m.TableName,
			MetricFormatVersion: int(m.FormatVersion), Definition: m.Definition,
		})
	}
	for _, m := range models {
		name, err := s.q.GetCatalogNameByID(ctx, m.CatalogID)
		if err != nil {
			return Bundle{}, err
		}
		b.Items = append(b.Items, BundleItem{
			Kind: KindModel, Slug: m.Slug, Label: m.Label, Description: m.Description,
			Certified: m.Certified, Catalog: name, ModelFormatVersion: int(m.FormatVersion),
			Definition: m.Definition,
		})
	}
	return b, nil
}

// ── import ─────────────────────────────────────────────────────────────────────

// ImportMode is dry_run or apply.
type ImportMode string

const (
	ImportDryRun ImportMode = "dry_run"
	ImportApply  ImportMode = "apply"
)

// ImportAction is the per-item outcome.
type ImportAction string

const (
	ActionCreate    ImportAction = "CREATE"
	ActionUpdate    ImportAction = "UPDATE"
	ActionNoChanges ImportAction = "NO_CHANGES"
	ActionError     ImportAction = "ERROR"
)

// ImportItem is one line of the report.
type ImportItem struct {
	Kind      string       `json:"kind"`
	Slug      string       `json:"slug"`
	Title     string       `json:"title,omitempty"`
	Action    ImportAction `json:"action"`
	MatchedID string       `json:"matched_id,omitempty"`
	Message   string       `json:"message,omitempty"`
	Warnings  []string     `json:"warnings,omitempty"`
}

// ImportReport is the dry-run/apply response.
type ImportReport struct {
	Mode  string       `json:"mode"`
	OK    bool         `json:"ok"`
	Items []ImportItem `json:"items"`
}

// ErrImportFailed means an apply had item errors; nothing was written.
var ErrImportFailed = errors.New("import has item errors")

// ImportOptions configure one import.
type ImportOptions struct {
	WorkspaceID pgtype.UUID
	SpaceID     pgtype.UUID
	Actor       pgtype.UUID
	Bundle      Bundle
	Mode        ImportMode
	// AllowCertification lets the importer honor an item's certified flag.
	// Only a space manager may certify, so an editor's import preserves the
	// existing row's certification (and creates uncertified rows).
	AllowCertification bool
	// Dialect checks saved queries against the editor policy; they run on the
	// default Trino engine.
	Dialect dialect.Dialect
	// ValidateMetric runs #33's engine probe in apply mode; nil skips it.
	ValidateMetric func(catalogID pgtype.UUID, namespace []string, table string, definition json.RawMessage, bindingID pgtype.UUID) error
}

// Import plans every item and, in apply mode with no errors, writes them in one
// transaction. A dry run always rolls back.
func (s *Service) Import(ctx context.Context, opts ImportOptions) (ImportReport, error) {
	report := ImportReport{Mode: string(opts.Mode)}
	var q db.Querier
	var tx pgx.Tx
	var err error
	if s.pool != nil {
		tx, err = s.pool.Begin(ctx)
		if err != nil {
			return report, err
		}
		defer func() { _ = tx.Rollback(ctx) }()
		q = db.New(tx)
	} else {
		q = s.q
	}

	imp := &importer{q: q, svc: s.WithQuerier(q), opts: opts}
	report, err = imp.run(ctx)
	if err != nil {
		return report, err
	}
	if opts.Mode == ImportApply && !report.OK {
		return report, ErrImportFailed
	}
	if tx != nil && opts.Mode == ImportApply {
		return report, tx.Commit(ctx)
	}
	return report, nil
}

type importer struct {
	q    db.Querier
	svc  *Service
	opts ImportOptions

	space         db.Space
	folderIDs     map[string]pgtype.UUID
	foldersBySlug map[string]db.QueryFolder
	folderParents map[string]*string
	targets       []db.ListWorkspaceTargetsRow
	plan          []planItem
}

type planItem struct {
	item       BundleItem
	action     ImportAction
	existingID pgtype.UUID
	folderID   pgtype.UUID
	bindingID  pgtype.UUID
	target     *BundleTarget
	catalogID  pgtype.UUID
	definition []byte
	certified  bool
	message    string
	warnings   []string
}

func (im *importer) run(ctx context.Context) (ImportReport, error) {
	report := ImportReport{Mode: string(im.opts.Mode)}
	b := im.opts.Bundle
	if b.Format != BundleFormat {
		return report, fmt.Errorf("unsupported bundle format %q", b.Format)
	}
	if b.FormatVersion != BundleVersion {
		return report, fmt.Errorf("unsupported bundle format version %d", b.FormatVersion)
	}

	space, err := im.q.GetSpace(ctx, db.GetSpaceParams{WorkspaceID: im.opts.WorkspaceID, ID: im.opts.SpaceID})
	if err != nil {
		return report, err
	}
	im.space = space
	folders, err := im.q.ListSpaceFolders(ctx, im.opts.SpaceID)
	if err != nil {
		return report, err
	}
	im.folderIDs = map[string]pgtype.UUID{}
	im.foldersBySlug = map[string]db.QueryFolder{}
	im.folderParents = map[string]*string{}
	slugByID := map[string]string{}
	for _, f := range folders {
		im.folderIDs[f.Slug] = f.ID
		im.foldersBySlug[f.Slug] = f
		slugByID[uuidKey(f.ID)] = f.Slug
	}
	for _, f := range folders {
		if f.ParentID.Valid {
			if slug, ok := slugByID[uuidKey(f.ParentID)]; ok {
				im.folderParents[f.Slug] = &slug
			}
		}
	}
	im.targets, err = im.q.ListWorkspaceTargets(ctx, im.opts.WorkspaceID)
	if err != nil {
		return report, err
	}

	items := orderBundleItems(b.Items)
	im.plan = make([]planItem, 0, len(items))
	for _, item := range items {
		im.plan = append(im.plan, im.planItem(ctx, item))
	}
	report.OK = true
	for _, p := range im.plan {
		matched := ""
		if p.existingID.Valid {
			matched = uuid.UUID(p.existingID.Bytes).String()
		}
		title := p.item.Name
		if p.item.Kind == KindSemanticMetric {
			title = p.item.Label
		}
		report.Items = append(report.Items, ImportItem{
			Kind: p.item.Kind, Slug: p.item.Slug, Title: title, Action: p.action,
			MatchedID: matched, Message: p.message, Warnings: p.warnings,
		})
		if p.action == ActionError {
			report.OK = false
		}
	}
	if im.opts.Mode == ImportApply && report.OK {
		if err := im.apply(ctx); err != nil {
			return report, err
		}
	}
	return report, nil
}

func (im *importer) planItem(ctx context.Context, item BundleItem) planItem {
	p := planItem{item: item}
	switch item.Kind {
	case KindFolder:
		im.planFolder(&p)
	case KindSavedQuery:
		im.planSavedQuery(ctx, &p)
	case KindSemanticMetric:
		im.planMetric(ctx, &p)
	case KindModel:
		im.planModel(ctx, &p)
	default:
		p.action, p.message = ActionError, "unsupported_kind"
	}
	return p
}

func (im *importer) planFolder(p *planItem) {
	item := p.item
	if item.Name == "" {
		p.action, p.message = ActionError, "name_required"
		return
	}
	var parentID pgtype.UUID
	if item.Parent != nil {
		if id, ok := im.folderIDs[*item.Parent]; ok {
			parentID = id
		} else if !bundleHasFolder(im.opts.Bundle.Items, *item.Parent) {
			p.action, p.message = ActionError, "parent_not_found"
			return
		}
	}
	if existing, ok := im.foldersBySlug[item.Slug]; ok {
		p.existingID = existing.ID
		p.folderID = parentID
		sameParent := (!existing.ParentID.Valid && item.Parent == nil) ||
			(existing.ParentID.Valid && item.Parent != nil && *im.folderParents[item.Slug] == *item.Parent)
		if existing.Name == item.Name && sameParent {
			p.action = ActionNoChanges
		} else {
			p.action = ActionUpdate
		}
		return
	}
	p.folderID = parentID
	p.action = ActionCreate
}

func (im *importer) planSavedQuery(ctx context.Context, p *planItem) {
	item := p.item
	if item.Name == "" || item.SQL == "" {
		p.action, p.message = ActionError, "name_and_sql_required"
		return
	}
	if policy.ForbiddenInEditor(im.opts.Dialect, item.SQL) {
		p.action, p.message = ActionError, "forbidden_sql"
		return
	}
	if item.Folder != nil {
		if _, ok := im.folderIDs[*item.Folder]; !ok && !bundleHasFolder(im.opts.Bundle.Items, *item.Folder) {
			p.action, p.message = ActionError, "folder_not_found"
			return
		}
	}
	bindingID, schema, warning, ok := im.resolveTarget(item.Target)
	if !ok {
		p.warnings = append(p.warnings, warning)
	}
	p.bindingID = bindingID
	if item.Target != nil {
		t := *item.Target
		t.Schema = schema
		p.target = &t
	}

	existing, err := im.q.GetSavedQueryBySlug(ctx, db.GetSavedQueryBySlugParams{WorkspaceID: im.opts.WorkspaceID, Slug: item.Slug})
	if errors.Is(err, pgx.ErrNoRows) {
		p.action = ActionCreate
		return
	}
	if err != nil {
		p.action, p.message = ActionError, "lookup_failed"
		return
	}
	if existing.SpaceID.Bytes != im.opts.SpaceID.Bytes {
		p.action, p.message = ActionError, "slug_conflict_other_space"
		return
	}
	p.existingID = existing.ID
	if savedQueryBundleEqual(existing, item, p.bindingID) {
		p.action = ActionNoChanges
		return
	}
	p.action = ActionUpdate
}

func (im *importer) planMetric(ctx context.Context, p *planItem) {
	item := p.item
	if item.Label == "" || item.Table == "" || item.Catalog == "" || len(item.Definition) == 0 {
		p.action, p.message = ActionError, "incomplete_metric"
		return
	}
	if item.MetricFormatVersion < 0 || item.MetricFormatVersion > maxFormatVersion {
		p.action, p.message = ActionError, "invalid_format_version"
		return
	}
	catID, err := im.q.GetGrantedCatalogByName(ctx, db.GetGrantedCatalogByNameParams{
		WorkspaceID: im.opts.WorkspaceID, Name: item.Catalog,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		p.action, p.message = ActionError, "catalog_not_granted"
		return
	}
	if err != nil {
		p.action, p.message = ActionError, "lookup_failed"
		return
	}
	p.catalogID = catID
	def, err := semantic.ParseSimpleMetricV1(item.Definition)
	if err != nil {
		p.action, p.message = ActionError, "invalid_definition"
		return
	}
	normalized, err := json.Marshal(def)
	if err != nil {
		p.action, p.message = ActionError, "invalid_definition"
		return
	}
	p.definition = normalized

	existing, err := im.q.GetSemanticMetricBySlug(ctx, db.GetSemanticMetricBySlugParams{WorkspaceID: im.opts.WorkspaceID, Slug: item.Slug})
	effective := item
	if errors.Is(err, pgx.ErrNoRows) {
		p.certified = item.Certified && im.opts.AllowCertification
		effective.Certified = p.certified
		p.action = ActionCreate
	} else if err != nil {
		p.action, p.message = ActionError, "lookup_failed"
		return
	} else if existing.SpaceID.Bytes != im.opts.SpaceID.Bytes {
		p.action, p.message = ActionError, "slug_conflict_other_space"
		return
	} else {
		p.existingID = existing.ID
		if im.opts.AllowCertification {
			p.certified = item.Certified
		} else {
			p.certified = existing.Certified
		}
		effective.Certified = p.certified
		if metricBundleEqual(existing, effective, p.definition) {
			p.action = ActionNoChanges
			return
		}
		p.action = ActionUpdate
	}

	if im.opts.Mode == ImportApply && im.opts.ValidateMetric != nil {
		if err := im.opts.ValidateMetric(p.catalogID, item.Namespace, item.Table, item.Definition, p.bindingID); err != nil {
			p.action, p.message = ActionError, "engine_validation"
		}
	}
}

func (im *importer) planModel(ctx context.Context, p *planItem) {
	item := p.item
	if item.Label == "" || item.Catalog == "" || len(item.Definition) == 0 {
		p.action, p.message = ActionError, "incomplete_model"
		return
	}
	if item.ModelFormatVersion < 0 || item.ModelFormatVersion > maxFormatVersion {
		p.action, p.message = ActionError, "invalid_format_version"
		return
	}
	catID, err := im.q.GetGrantedCatalogByName(ctx, db.GetGrantedCatalogByNameParams{
		WorkspaceID: im.opts.WorkspaceID, Name: item.Catalog,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		p.action, p.message = ActionError, "catalog_not_granted"
		return
	}
	if err != nil {
		p.action, p.message = ActionError, "lookup_failed"
		return
	}
	p.catalogID = catID
	def, err := parseBundleModel(item.Definition, catID)
	if err != nil {
		p.action, p.message = ActionError, "invalid_definition"
		return
	}
	normalized, err := json.Marshal(def)
	if err != nil {
		p.action, p.message = ActionError, "invalid_definition"
		return
	}
	p.definition = normalized

	existing, err := im.q.GetSemanticModelBySlug(ctx, db.GetSemanticModelBySlugParams{WorkspaceID: im.opts.WorkspaceID, Slug: item.Slug})
	effective := item
	if errors.Is(err, pgx.ErrNoRows) {
		p.certified = item.Certified && im.opts.AllowCertification
		effective.Certified = p.certified
		p.action = ActionCreate
	} else if err != nil {
		p.action, p.message = ActionError, "lookup_failed"
		return
	} else if existing.SpaceID.Bytes != im.opts.SpaceID.Bytes {
		p.action, p.message = ActionError, "slug_conflict_other_space"
		return
	} else {
		p.existingID = existing.ID
		if im.opts.AllowCertification {
			p.certified = item.Certified
		} else {
			p.certified = existing.Certified
		}
		effective.Certified = p.certified
		if modelBundleEqual(existing, effective, p.definition, catID) {
			p.action = ActionNoChanges
			return
		}
		p.action = ActionUpdate
	}
}

func parseBundleModel(raw []byte, catalogID pgtype.UUID) (semantic.Model, error) {
	var m semantic.Model
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		return semantic.Model{}, err
	}
	// The row's catalog is authoritative; the bundle identifies it by name.
	m.CatalogID = uuid.UUID(catalogID.Bytes).String()
	m.BindingID = ""
	if err := m.Validate(nil); err != nil {
		return semantic.Model{}, err
	}
	return m, nil
}

func modelBundleEqual(cur db.SemanticModel, item BundleItem, definition []byte, catalogID pgtype.UUID) bool {
	return cur.Slug == item.Slug && cur.Label == item.Label && cur.Description == item.Description &&
		cur.Certified == item.Certified && cur.CatalogID.Bytes == catalogID.Bytes &&
		cur.FormatVersion == int32(item.ModelFormatVersion) &&
		sameModelDefinition(cur.Definition, definition)
}

func (im *importer) resolveTarget(t *BundleTarget) (pgtype.UUID, *string, string, bool) {
	if t == nil || t.Catalog == "" {
		return pgtype.UUID{}, nil, "", true
	}
	for _, row := range im.targets {
		if !row.BindingEnabled || !row.EngineEnabled || !row.CatalogEnabled {
			continue
		}
		if row.CatalogName != t.Catalog {
			continue
		}
		if t.Engine != "" && row.EngineName != t.Engine {
			continue
		}
		return row.BindingID, t.Schema, "", true
	}
	return pgtype.UUID{}, nil, "target_not_granted", false
}

func (im *importer) apply(ctx context.Context) error {
	for i := range im.plan {
		p := &im.plan[i]
		switch p.action {
		case ActionCreate, ActionUpdate:
		default:
			continue
		}
		switch p.item.Kind {
		case KindFolder:
			// Resolve the parent now: a parent defined later in the bundle was
			// created earlier in this loop.
			parentID := p.folderID
			if p.item.Parent != nil {
				parentID = im.folderIDs[*p.item.Parent]
			}
			if p.action == ActionCreate {
				folder, err := im.svc.q.CreateQueryFolder(ctx, db.CreateQueryFolderParams{
					WorkspaceID: im.opts.WorkspaceID, SpaceID: im.opts.SpaceID, ParentID: parentID,
					Name: p.item.Name, Slug: p.item.Slug,
				})
				if err != nil {
					return err
				}
				im.folderIDs[p.item.Slug] = folder.ID
			} else {
				if _, err := im.svc.q.UpdateQueryFolder(ctx, db.UpdateQueryFolderParams{
					WorkspaceID: im.opts.WorkspaceID, ID: p.existingID, Name: p.item.Name, ParentID: parentID,
				}); err != nil {
					return err
				}
			}
		case KindSavedQuery:
			var folderID pgtype.UUID
			if p.item.Folder != nil {
				folderID = im.folderIDs[*p.item.Folder]
			}
			var schema *string
			if p.target != nil {
				schema = p.target.Schema
			}
			if p.action == ActionCreate {
				if _, err := im.svc.CreateSavedQuery(ctx, im.opts.Actor, db.CreateSavedQueryParams{
					WorkspaceID: im.opts.WorkspaceID, SpaceID: im.opts.SpaceID, FolderID: folderID,
					CreatedBy: im.opts.Actor, UpdatedBy: im.opts.Actor, Name: p.item.Name,
					Slug: p.item.Slug, Sql: p.item.SQL, TargetBindingID: p.bindingID, TargetSchema: schema,
				}); err != nil {
					return err
				}
			} else {
				if _, err := im.svc.ImportSavedQuery(ctx, im.opts.Actor, db.UpdateSavedQueryParams{
					WorkspaceID: im.opts.WorkspaceID, ID: p.existingID, Name: p.item.Name, Sql: p.item.SQL,
					FolderID: folderID, SpaceID: im.opts.SpaceID, Slug: p.item.Slug,
					TargetBindingID: p.bindingID, TargetSchema: schema, UpdatedBy: im.opts.Actor,
				}); err != nil {
					return err
				}
			}
		case KindSemanticMetric:
			if p.action == ActionCreate {
				if _, err := im.svc.CreateSemanticMetric(ctx, im.opts.Actor, db.CreateSemanticMetricParams{
					WorkspaceID: im.opts.WorkspaceID, SpaceID: im.opts.SpaceID, Slug: p.item.Slug,
					Label: p.item.Label, Description: p.item.Description, Certified: p.certified,
					CatalogID: p.catalogID, Namespace: p.item.Namespace, TableName: p.item.Table,
					Definition: p.definition, FormatVersion: int32(p.item.MetricFormatVersion),
					UpdatedBy: im.opts.Actor,
				}); err != nil {
					return err
				}
			} else {
				if _, err := im.svc.ImportSemanticMetric(ctx, im.opts.Actor, db.UpdateSemanticMetricParams{
					WorkspaceID: im.opts.WorkspaceID, ID: p.existingID, SpaceID: im.opts.SpaceID,
					Slug: p.item.Slug, Label: p.item.Label, Description: p.item.Description,
					Certified: p.certified, CatalogID: p.catalogID, Namespace: p.item.Namespace,
					TableName: p.item.Table, Definition: p.definition,
					FormatVersion: int32(p.item.MetricFormatVersion), UpdatedBy: im.opts.Actor,
				}); err != nil {
					return err
				}
			}
		case KindModel:
			if p.action == ActionCreate {
				if _, err := im.svc.CreateSemanticModel(ctx, im.opts.Actor, db.CreateSemanticModelParams{
					WorkspaceID: im.opts.WorkspaceID, SpaceID: im.opts.SpaceID, Slug: p.item.Slug,
					Label: p.item.Label, Description: p.item.Description, Certified: p.certified,
					CatalogID: p.catalogID, Definition: p.definition,
					FormatVersion: int32(p.item.ModelFormatVersion), UpdatedBy: im.opts.Actor,
				}); err != nil {
					return err
				}
			} else {
				if _, err := im.svc.ImportSemanticModel(ctx, im.opts.Actor, db.UpdateSemanticModelParams{
					WorkspaceID: im.opts.WorkspaceID, ID: p.existingID, SpaceID: im.opts.SpaceID,
					Slug: p.item.Slug, Label: p.item.Label, Description: p.item.Description,
					Certified: p.certified, CatalogID: p.catalogID, Definition: p.definition,
					FormatVersion: int32(p.item.ModelFormatVersion), UpdatedBy: im.opts.Actor,
				}); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// ── helpers ─────────────────────────────────────────────────────────────────────

func orderFolders(folders []db.QueryFolder) []db.QueryFolder {
	bySlug := map[string]db.QueryFolder{}
	for _, f := range folders {
		bySlug[f.Slug] = f
	}
	var out []db.QueryFolder
	seen := map[string]bool{}
	var visit func(db.QueryFolder)
	visit = func(f db.QueryFolder) {
		if seen[f.Slug] {
			return
		}
		seen[f.Slug] = true
		if f.ParentID.Valid {
			for _, p := range folders {
				if p.ID.Bytes == f.ParentID.Bytes {
					visit(p)
					break
				}
			}
		}
		out = append(out, f)
	}
	for _, f := range folders {
		visit(f)
	}
	return out
}

// orderBundleItems puts folders first (parents before children), then queries,
// then metrics, each sorted by slug, so imports satisfy their dependencies.
func orderBundleItems(items []BundleItem) []BundleItem {
	var folders, queries, metrics, models []BundleItem
	for _, it := range items {
		switch it.Kind {
		case KindFolder:
			folders = append(folders, it)
		case KindSavedQuery:
			queries = append(queries, it)
		case KindSemanticMetric:
			metrics = append(metrics, it)
		case KindModel:
			models = append(models, it)
		default:
			queries = append(queries, it) // reported as unsupported, still ordered
		}
	}
	sort.Slice(folders, func(i, j int) bool { return folders[i].Slug < folders[j].Slug })
	sort.Slice(queries, func(i, j int) bool { return queries[i].Slug < queries[j].Slug })
	sort.Slice(metrics, func(i, j int) bool { return metrics[i].Slug < metrics[j].Slug })
	sort.Slice(models, func(i, j int) bool { return models[i].Slug < models[j].Slug })
	ordered := make([]BundleItem, 0, len(items))
	ordered = append(ordered, orderFoldersItems(folders)...)
	ordered = append(ordered, queries...)
	ordered = append(ordered, metrics...)
	ordered = append(ordered, models...)
	return ordered
}

func orderFoldersItems(folders []BundleItem) []BundleItem {
	bySlug := map[string]BundleItem{}
	for _, f := range folders {
		bySlug[f.Slug] = f
	}
	var out []BundleItem
	seen := map[string]bool{}
	var visit func(BundleItem)
	visit = func(f BundleItem) {
		if seen[f.Slug] {
			return
		}
		seen[f.Slug] = true
		if f.Parent != nil {
			if p, ok := bySlug[*f.Parent]; ok {
				visit(p)
			}
		}
		out = append(out, f)
	}
	for _, f := range folders {
		visit(f)
	}
	return out
}

func bundleHasFolder(items []BundleItem, slug string) bool {
	for _, it := range items {
		if it.Kind == KindFolder && it.Slug == slug {
			return true
		}
	}
	return false
}

func savedQueryBundleEqual(existing db.SavedQuery, item BundleItem, bindingID pgtype.UUID) bool {
	if existing.Name != item.Name || existing.Sql != item.SQL || existing.Slug != item.Slug {
		return false
	}
	if !sameUUID(existing.TargetBindingID, bindingID) {
		return false
	}
	if item.Target != nil && !sameStringPtr(existing.TargetSchema, item.Target.Schema) {
		return false
	}
	return true
}

func metricBundleEqual(existing db.SemanticMetric, item BundleItem, definition []byte) bool {
	if existing.Slug != item.Slug || existing.Label != item.Label || existing.Description != item.Description ||
		existing.Certified != item.Certified || existing.TableName != item.Table ||
		existing.FormatVersion != int32(item.MetricFormatVersion) || !sameStrings(existing.Namespace, item.Namespace) {
		return false
	}
	// Compare the structured definitions, not their bytes: jsonb returns a
	// canonical text with spaces, the bundle carries the original JSON.
	a, errA := semantic.ParseSimpleMetricV1(existing.Definition)
	b, errB := semantic.ParseSimpleMetricV1(definition)
	if errA != nil || errB != nil {
		return false
	}
	return reflect.DeepEqual(a, b)
}

func uuidKey(id pgtype.UUID) string {
	return fmt.Sprintf("%x", id.Bytes)
}
