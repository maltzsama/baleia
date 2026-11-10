package api

import (
	"slices"
	"testing"

	"github.com/galileostudio/baleia/internal/store/model"
	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

func (f *metaFixture) glossary(t *testing.T, user string) SearchGlossary {
	t.Helper()
	r, err := f.h.ListSearchGlossary(f.as(user), ListSearchGlossaryRequestObject{Id: f.ws})
	if err != nil {
		t.Fatal(err)
	}
	res, ok := r.(ListSearchGlossary200JSONResponse)
	if !ok {
		t.Fatalf("ListSearchGlossary = %T", r)
	}
	return SearchGlossary(res)
}

func (f *metaFixture) addSynonyms(t *testing.T, user, term string, synonyms ...string) CreateSearchGlossaryEntryResponseObject {
	t.Helper()
	r, err := f.h.CreateSearchGlossaryEntry(f.as(user), CreateSearchGlossaryEntryRequestObject{
		Id: f.ws, Body: &SearchGlossaryEntryCreate{Term: term, Synonyms: synonyms},
	})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// The glossary keeps entries folded, rejects a second entry for a term, and
// answers each caller what they may do with it.
func TestSearchGlossary_CreateListDelete(t *testing.T) {
	t.Parallel()
	f := newMetaFixture(t)
	if got := f.glossary(t, f.owner); len(got.Entries) != 0 || !slices.Contains(got.AllowedActions, "view") {
		t.Fatalf("empty glossary = %+v", got)
	}

	r := f.addSynonyms(t, f.owner, "Receita", "Faturamento", "REVENUE")
	created, ok := r.(CreateSearchGlossaryEntry201JSONResponse)
	if !ok {
		t.Fatalf("create = %T", r)
	}
	if created.Term != "receita" || !slices.Equal(created.Synonyms, []string{"faturamento", "revenue"}) {
		t.Errorf("entry = %+v, want it folded", created)
	}
	if _, ok := f.addSynonyms(t, f.owner, "RECEITA", "x1").(CreateSearchGlossaryEntry409JSONResponse); !ok {
		t.Error("a second entry for a term answers 409")
	}
	got := f.glossary(t, f.owner)
	if len(got.Entries) != 1 || !slices.Contains(got.AllowedActions, "edit") {
		t.Fatalf("glossary = %+v", got)
	}

	del := func(id openapi_types.UUID) DeleteSearchGlossaryEntryResponseObject {
		r, err := f.h.DeleteSearchGlossaryEntry(f.as(f.owner), DeleteSearchGlossaryEntryRequestObject{Id: f.ws, EntryId: id})
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	if _, ok := del(openapi_types.UUID(uuid.New())).(DeleteSearchGlossaryEntry404JSONResponse); !ok {
		t.Error("an unknown entry is 404")
	}
	if _, ok := del(created.Id).(DeleteSearchGlossaryEntry204Response); !ok {
		t.Error("the entry is deleted")
	}
	if got := f.glossary(t, f.owner); len(got.Entries) != 0 {
		t.Errorf("glossary after delete = %+v", got)
	}
}

func TestSearchGlossary_Validation(t *testing.T) {
	t.Parallel()
	f := newMetaFixture(t)
	many := make([]string, glossaryMaxSynonyms+1)
	for i := range many {
		many[i] = "word" + string(rune('a'+i))
	}
	for name, body := range map[string]SearchGlossaryEntryCreate{
		"empty term":               {Term: "  ", Synonyms: []string{"a1"}},
		"no synonyms":              {Term: "revenue", Synonyms: nil},
		"too many synonyms":        {Term: "revenue", Synonyms: many},
		"a synonym is empty":       {Term: "revenue", Synonyms: []string{"sales", " "}},
		"the term itself":          {Term: "Revenue", Synonyms: []string{"REVENUE"}},
		"a repeated synonym":       {Term: "revenue", Synonyms: []string{"sales", "Sales"}},
		"a term of punctuation":    {Term: "!!", Synonyms: []string{"a1"}},
		"a synonym of punctuation": {Term: "revenue", Synonyms: []string{"sales", "--"}},
		"a too long term":          {Term: string(make([]rune, glossaryMaxWords+1)), Synonyms: []string{"a1"}},
		"a too long synonyms":      {Term: "revenue", Synonyms: []string{string(make([]rune, glossaryMaxWords+1))}},
	} {
		r, err := f.h.CreateSearchGlossaryEntry(f.as(f.owner), CreateSearchGlossaryEntryRequestObject{Id: f.ws, Body: &body})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if _, ok := r.(CreateSearchGlossaryEntry422JSONResponse); !ok {
			t.Errorf("%s = %T, want 422", name, r)
		}
	}
}

// Members read it, editors write it, and a stranger does not know the
// workspace exists.
func TestSearchGlossary_Permissions(t *testing.T) {
	t.Parallel()
	f := newMetaFixture(t)
	viewer := f.addMember(t, "viewer")
	if _, ok := f.addSynonyms(t, viewer, "revenue", "sales").(CreateSearchGlossaryEntry403JSONResponse); !ok {
		t.Error("a viewer cannot add an entry")
	}
	if got := f.glossary(t, viewer); slices.Contains(got.AllowedActions, "edit") {
		t.Errorf("a viewer is offered %v", got.AllowedActions)
	}
	if _, ok := f.addSynonyms(t, f.stranger, "revenue", "sales").(CreateSearchGlossaryEntry404JSONResponse); !ok {
		t.Error("a stranger gets 404")
	}
	r, err := f.h.ListSearchGlossary(f.as(f.stranger), ListSearchGlossaryRequestObject{Id: f.ws})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := r.(ListSearchGlossary404JSONResponse); !ok {
		t.Errorf("a stranger lists %T", r)
	}
}

// A search also looks for the synonyms of its words, ranks what the person
// typed first and says what it added.
func TestSearchWorkspace_ExpandsWithTheGlossary(t *testing.T) {
	t.Parallel()
	f := federatedFixture(t)
	without := searchAll(t, f, f.owner, SearchWorkspaceParams{Q: "ltv"})
	if len(without.ExpandedWith) != 0 {
		t.Fatalf("expanded without a glossary: %v", without.ExpandedWith)
	}
	if _, ok := f.addSynonyms(t, f.owner, "ltv", "churn").(CreateSearchGlossaryEntry201JSONResponse); !ok {
		t.Fatal("could not add the entry")
	}
	res := searchAll(t, f, f.owner, SearchWorkspaceParams{Q: "ltv"})
	if !slices.Equal(res.ExpandedWith, []string{"churn"}) {
		t.Fatalf("expanded_with = %v", res.ExpandedWith)
	}
	slugs := []string{}
	for _, item := range res.Items {
		if item.Content != nil {
			slugs = append(slugs, item.Content.Slug)
		}
	}
	if len(slugs) < 2 || slugs[0] != "customer_ltv" || !slices.Contains(slugs, "churn_rate") {
		t.Fatalf("content = %v, want the typed word first and its synonym too", slugs)
	}
}

// The glossary of one workspace does not apply to another.
func TestSearchGlossary_IsPerWorkspace(t *testing.T) {
	t.Parallel()
	f := federatedFixture(t)
	row := model.SearchSynonym{
		ID: model.UUID(uuid.NewString()), WorkspaceID: model.UUID(f.strangerWS.String()), Term: "ltv",
		Synonyms: model.StringArray{"churn"}, CreatedBy: model.UUID(f.stranger),
	}
	if err := f.gdb.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	if res := searchAll(t, f, f.owner, SearchWorkspaceParams{Q: "ltv"}); len(res.ExpandedWith) != 0 {
		t.Fatalf("another workspace's glossary applied: %v", res.ExpandedWith)
	}
}

// addMember makes a new user a member of the fixture's workspace with the role
// and returns their id.
func (f *metaFixture) addMember(t *testing.T, role string) string {
	t.Helper()
	password := "x"
	user := model.User{ID: model.UUID(uuid.NewString()), Name: role, Email: model.Text(role + "@x"), PasswordHash: &password}
	if err := f.gdb.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	member := model.WorkspaceMember{WorkspaceID: model.UUID(f.ws.String()), UserID: user.ID, Role: role, Source: "manual"}
	if err := f.gdb.Create(&member).Error; err != nil {
		t.Fatal(err)
	}
	return string(user.ID)
}

// A synonym the surfaces would refuse as a query (one letter) must not make
// the search fail: the variant is skipped.
func TestSearchWorkspace_SkipsGlossaryVariantsTheSurfacesRefuse(t *testing.T) {
	t.Parallel()
	f := federatedFixture(t)
	if _, ok := f.addSynonyms(t, f.owner, "orders", "x").(CreateSearchGlossaryEntry201JSONResponse); !ok {
		t.Fatal("could not add the entry")
	}
	res := searchAll(t, f, f.owner, SearchWorkspaceParams{Q: "orders"})
	if len(res.Items) == 0 {
		t.Fatal("the search still finds what was typed")
	}
	if len(res.ExpandedWith) != 0 {
		t.Errorf("the one-letter variant was searched: %v", res.ExpandedWith)
	}

	// A word replaced by a longer synonym can push the query past 200 characters.
	long := "synonym" + string(make([]rune, 0))
	for len(long) < glossaryMaxWords {
		long += "x"
	}
	if _, ok := f.addSynonyms(t, f.owner, "customers", long).(CreateSearchGlossaryEntry201JSONResponse); !ok {
		t.Fatal("could not add the long entry")
	}
	query := "customers"
	for len(query) < 197 {
		query = "a " + query
	}
	if res := searchAll(t, f, f.owner, SearchWorkspaceParams{Q: query}); res.ExpandedWith == nil {
		t.Fatal("the search answers")
	}
}
