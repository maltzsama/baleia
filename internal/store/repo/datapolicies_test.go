package repo

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/datapolicy"
	"github.com/google/uuid"
)

func dataPolicyFixture(t *testing.T) (*workbenchFixture, *DataPolicies, uuid.UUID, uuid.UUID) {
	t.Helper()
	f := newWorkbenchFixture(t)
	return f, NewDataPolicies(f.gdb), uuid.MustParse(string(f.catalog)), uuid.MustParse(string(f.owner))
}

func rowFilter(catalogID, author uuid.UUID, name string) datapolicy.Policy {
	return datapolicy.Policy{
		Name:       name,
		Kind:       datapolicy.KindRowFilter,
		Target:     datapolicy.Target{CatalogID: catalogID, Schema: "sales", Table: "orders"},
		Mode:       datapolicy.ModePermissive,
		Subjects:   []datapolicy.Subject{{Team: uuid.NewString()}},
		Expression: "region = 'EU'",
		State:      datapolicy.StateEnabled,
		Validation: datapolicy.ValidationPending,
		CreatedBy:  author,
	}
}

func TestDataPolicies_RoundTrip(t *testing.T) {
	t.Parallel()
	_, r, catalogID, author := dataPolicyFixture(t)
	ctx := context.Background()

	created, err := r.Create(ctx, rowFilter(catalogID, author, "eu"))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.ID == uuid.Nil || created.Version != 1 {
		t.Fatalf("created = %+v, want an id and version 1", created)
	}
	mask := datapolicy.Policy{
		Name:       "hide-email",
		Kind:       datapolicy.KindColumnMask,
		Target:     datapolicy.Target{CatalogID: catalogID, Schema: "sales", Table: "orders", Column: "email"},
		Priority:   5,
		Subjects:   []datapolicy.Subject{{Everyone: true, Except: []datapolicy.Subject{{Group: "auditors"}}}},
		Expression: "'***'",
		Template:   datapolicy.MaskTemplateConstant,
		State:      datapolicy.StateEnabled,
		Validation: datapolicy.ValidationPending,
		CreatedBy:  author,
	}
	if _, err := r.Create(ctx, mask); err != nil {
		t.Fatalf("Create mask: %v", err)
	}

	got, err := r.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Target != created.Target || got.Mode != datapolicy.ModePermissive || len(got.Subjects) != 1 || got.Subjects[0].Team == "" {
		t.Fatalf("Get = %+v, want the created policy", got)
	}

	list, err := r.ListByCatalog(ctx, catalogID)
	if err != nil || len(list) != 2 {
		t.Fatalf("ListByCatalog = %d policies, %v; want 2", len(list), err)
	}
	if list[0].Name != "eu" || list[1].Subjects[0].Except[0].Group != "auditors" {
		t.Fatalf("ListByCatalog order or subjects = %+v", list)
	}

	if _, err := r.Create(ctx, rowFilter(catalogID, author, "eu")); !errors.Is(err, datapolicy.ErrNameTaken) {
		t.Fatalf("a second policy named eu on the table: %v, want ErrNameTaken", err)
	}
	other := rowFilter(catalogID, author, "eu")
	other.Target.Table = "customers"
	if _, err := r.Create(ctx, other); err != nil {
		t.Fatalf("the same name on another table: %v", err)
	}

	if _, err := r.Get(ctx, uuid.New()); !errors.Is(err, datapolicy.ErrNotFound) {
		t.Fatalf("Get of an unknown id: %v, want ErrNotFound", err)
	}
}

func TestDataPolicies_UpdateIsVersioned(t *testing.T) {
	t.Parallel()
	_, r, catalogID, author := dataPolicyFixture(t)
	ctx := context.Background()
	p, err := r.Create(ctx, rowFilter(catalogID, author, "eu"))
	if err != nil {
		t.Fatal(err)
	}

	p.Expression = "region IN ('EU', 'UK')"
	p.Validation, p.Compiled = datapolicy.ValidationValid, "region IN ('EU', 'UK')"
	updated, err := r.Update(ctx, p, 1)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.Version != 2 || updated.Expression != p.Expression || updated.Validation != datapolicy.ValidationValid {
		t.Fatalf("updated = %+v, want version 2 with the new expression", updated)
	}

	if _, err := r.Update(ctx, p, 1); !errors.Is(err, datapolicy.ErrVersionConflict) {
		t.Fatalf("a stale update: %v, want ErrVersionConflict", err)
	}
	gone := p
	gone.ID = uuid.New()
	if _, err := r.Update(ctx, gone, 1); !errors.Is(err, datapolicy.ErrNotFound) {
		t.Fatalf("updating an unknown policy: %v, want ErrNotFound", err)
	}

	if removed, err := r.Delete(ctx, p.ID); err != nil || !removed {
		t.Fatalf("Delete = %v, %v", removed, err)
	}
	if removed, err := r.Delete(ctx, p.ID); err != nil || removed {
		t.Fatalf("deleting twice = %v, %v", removed, err)
	}
}

func TestDataPolicies_CatalogAndEngineChecks(t *testing.T) {
	t.Parallel()
	f, r, catalogID, author := dataPolicyFixture(t)
	ctx := context.Background()
	engineID := uuid.MustParse(string(f.engine))

	if has, err := r.CatalogHasPolicies(ctx, catalogID); err != nil || has {
		t.Fatalf("CatalogHasPolicies before any policy = %v, %v", has, err)
	}
	if serves, err := r.EngineServesPoliciedCatalogs(ctx, engineID); err != nil || serves {
		t.Fatalf("EngineServesPoliciedCatalogs before any policy = %v, %v", serves, err)
	}

	disabled := rowFilter(catalogID, author, "eu")
	disabled.State = datapolicy.StateDisabled
	if _, err := r.Create(ctx, disabled); err != nil {
		t.Fatal(err)
	}
	if has, err := r.CatalogHasPolicies(ctx, catalogID); err != nil || !has {
		t.Fatalf("CatalogHasPolicies with a disabled policy = %v, %v; want true", has, err)
	}
	if serves, err := r.EngineServesPoliciedCatalogs(ctx, engineID); err != nil || !serves {
		t.Fatalf("EngineServesPoliciedCatalogs = %v, %v; want true", serves, err)
	}
	if serves, err := r.EngineServesPoliciedCatalogs(ctx, uuid.New()); err != nil || serves {
		t.Fatalf("an engine without bindings = %v, %v", serves, err)
	}
}

func TestDataPolicies_DeletingTheCatalogRemovesItsPolicies(t *testing.T) {
	t.Parallel()
	f, r, catalogID, author := dataPolicyFixture(t)
	ctx := context.Background()
	p, err := r.Create(ctx, rowFilter(catalogID, author, "eu"))
	if err != nil {
		t.Fatal(err)
	}
	if err := f.r.DeleteCatalog(ctx, f.catalogPG()); err != nil {
		t.Fatalf("DeleteCatalog: %v", err)
	}
	if _, err := r.Get(ctx, p.ID); !errors.Is(err, datapolicy.ErrNotFound) {
		t.Fatalf("a policy of a deleted catalog: %v, want ErrNotFound", err)
	}
}

func TestDataPolicies_Revisions(t *testing.T) {
	t.Parallel()
	_, r, _, author := dataPolicyFixture(t)
	ctx := context.Background()
	lease := time.Now().Add(time.Hour)

	first, err := r.CreateRevision(ctx, datapolicy.NewRevision(datapolicy.ReasonPublish, []byte(`{"tables":{}}`), lease, &author))
	if err != nil {
		t.Fatalf("CreateRevision: %v", err)
	}
	if first.Number != 1 || len(first.Digest) != 64 {
		t.Fatalf("first revision = %+v, want number 1 and a SHA-256 digest", first)
	}
	reissue := datapolicy.NewRevision(datapolicy.ReasonReissue, first.Content, lease.Add(time.Hour), nil)
	reissue.SourceID = &first.ID
	second, err := r.CreateRevision(ctx, reissue)
	if err != nil {
		t.Fatalf("CreateRevision re-issue: %v", err)
	}
	if second.Number != 2 || second.Digest != first.Digest {
		t.Fatalf("re-issue = %+v, want number 2 and the same digest", second)
	}

	got, err := r.GetRevision(ctx, second.ID)
	if err != nil {
		t.Fatalf("GetRevision: %v", err)
	}
	if got.SourceID == nil || *got.SourceID != first.ID || got.CreatedBy != nil || got.Reason != datapolicy.ReasonReissue {
		t.Fatalf("GetRevision = %+v, want the re-issue of the first revision", got)
	}
	if _, err := r.GetRevision(ctx, uuid.New()); !errors.Is(err, datapolicy.ErrNotFound) {
		t.Fatalf("GetRevision of an unknown id: %v, want ErrNotFound", err)
	}

	list, err := r.ListRevisions(ctx, 10)
	if err != nil || len(list) != 2 || list[0].Number != 2 {
		t.Fatalf("ListRevisions = %+v, %v; want newest first", list, err)
	}
	// The stored bytes are the ones the digest was taken over: no
	// normalization of whitespace or key order on any backend.
	spaced := []byte(`{ "tables" : {"b": 1,   "a": [ 2, 1 ]} }`)
	third, err := r.CreateRevision(ctx, datapolicy.NewRevision(datapolicy.ReasonPublish, spaced, lease, nil))
	if err != nil {
		t.Fatalf("CreateRevision: %v", err)
	}
	stored, err := r.GetRevision(ctx, third.ID)
	if err != nil {
		t.Fatalf("GetRevision: %v", err)
	}
	sum := sha256.Sum256(stored.Content)
	if string(stored.Content) != string(spaced) || hex.EncodeToString(sum[:]) != stored.Digest {
		t.Fatalf("stored content %q does not hash to its digest %s", stored.Content, stored.Digest)
	}

	if _, err := r.CreateRevision(ctx, datapolicy.NewRevision(datapolicy.ReasonPublish, []byte("not json"), lease, nil)); err == nil {
		t.Fatal("a revision whose content is not JSON was stored")
	}
}
