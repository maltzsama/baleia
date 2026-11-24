package repo

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/galileostudio/baleia/internal/datapolicy"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/store/model"
	"github.com/google/uuid"
	"gorm.io/gorm/clause"
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

// TestDataPolicies_ChecksCountPublishedPolicies: a policy deleted from the
// draft stays in force until the next publish, so the checks still see it.
func TestDataPolicies_ChecksCountPublishedPolicies(t *testing.T) {
	t.Parallel()
	f, r, catalogID, author := dataPolicyFixture(t)
	ctx := context.Background()
	engineID := uuid.MustParse(string(f.engine))
	enforce(t, f, true)

	content := policiedContent(t, catalogID, author)
	if _, err := r.PublishRevision(ctx, datapolicy.NewRevision(datapolicy.ReasonPublish, content, time.Now().Add(time.Hour), &author), uuid.Nil); err != nil {
		t.Fatal(err)
	}
	if has, err := r.CatalogHasPolicies(ctx, catalogID); err != nil || !has {
		t.Fatalf("CatalogHasPolicies with only a published policy = %v, %v; want true", has, err)
	}
	if serves, err := r.EngineServesPoliciedCatalogs(ctx, engineID); err != nil || !serves {
		t.Fatalf("EngineServesPoliciedCatalogs with only a published policy = %v, %v; want true", serves, err)
	}
	if has, err := r.CatalogHasPolicies(ctx, uuid.New()); err != nil || has {
		t.Fatalf("another catalog = %v, %v", has, err)
	}
	if serves, err := r.EngineServesPoliciedCatalogs(ctx, uuid.New()); err != nil || serves {
		t.Fatalf("an engine without bindings = %v, %v", serves, err)
	}
}

// TestDataPolicies_PublishRequiresEnforcingEngines: a publish or a rollback
// that puts policies in force on a catalog bound to an engine that does not
// enforce them is refused, also when the engine stops enforcing while the
// publish runs; a re-issue is not.
func TestDataPolicies_PublishRequiresEnforcingEngines(t *testing.T) {
	t.Parallel()
	f, r, catalogID, author := dataPolicyFixture(t)
	ctx := context.Background()
	lease := time.Now().Add(time.Hour)
	content := policiedContent(t, catalogID, author)

	for _, reason := range []datapolicy.RevisionReason{datapolicy.ReasonPublish, datapolicy.ReasonRollback} {
		if _, err := r.PublishRevision(ctx, datapolicy.NewRevision(reason, content, lease, &author), uuid.Nil); !errors.Is(err, datapolicy.ErrPoliciesUnenforced) {
			t.Fatalf("%s on an engine that does not enforce = %v, want ErrPoliciesUnenforced", reason, err)
		}
	}
	enforce(t, f, true)
	first, err := r.PublishRevision(ctx, datapolicy.NewRevision(datapolicy.ReasonPublish, content, lease, &author), uuid.Nil)
	if err != nil {
		t.Fatal(err)
	}

	// The engine stops enforcing under its row lock, as UpdateEngine does,
	// while a publish runs: the publish waits for the lock and sees the flag.
	tx := f.gdb.Begin()
	if err := tx.Clauses(clause.Locking{Strength: clause.LockingStrengthUpdate}).
		Where("id = ?", string(f.engine)).First(&model.Engine{}).Error; err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Model(&model.Engine{}).Where("id = ?", string(f.engine)).Update("data_policies", false).Error; err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := r.PublishRevision(ctx, datapolicy.NewRevision(datapolicy.ReasonPublish, content, lease, &author), first.ID)
		done <- err
	}()
	if err := tx.Commit().Error; err != nil {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, datapolicy.ErrPoliciesUnenforced) {
		t.Fatalf("publish racing the engine turning off = %v, want ErrPoliciesUnenforced", err)
	}

	if _, err := r.PublishRevision(ctx, datapolicy.NewRevision(datapolicy.ReasonReissue, content, lease, nil), first.ID); err != nil {
		t.Fatalf("re-issue on an engine that stopped enforcing = %v, want it to keep the lease", err)
	}
}

// policiedContent is a revision's content with one row filter on catalogID.
func policiedContent(t *testing.T, catalogID, author uuid.UUID) []byte {
	t.Helper()
	p := rowFilter(catalogID, author, "eu")
	p.Validation, p.Compiled = datapolicy.ValidationValid, "region = 'EU'"
	content, err := json.Marshal(datapolicy.Content{Catalogs: map[string]datapolicy.CatalogContent{
		catalogID.String(): datapolicy.CatalogContentOf([]string{"lake"}, []datapolicy.Policy{p}),
	}})
	if err != nil {
		t.Fatal(err)
	}
	return content
}

// enforce sets the fixture engine's data_policies flag.
func enforce(t *testing.T, f *workbenchFixture, on bool) {
	t.Helper()
	if err := f.gdb.Model(&model.Engine{}).Where("id = ?", string(f.engine)).Update("data_policies", on).Error; err != nil {
		t.Fatal(err)
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

// TestLockEngineForUpdate: a second transaction that locks the same engine
// waits until the first one ends, which is what serializes a binding with a
// change of the engine's data_policies flag.
func TestLockEngineForUpdate(t *testing.T) {
	t.Parallel()
	f := newWorkbenchFixture(t)
	ctx := context.Background()
	engine := PGUUID(string(f.engine))

	locked := make(chan struct{})
	release := make(chan struct{})
	firstDone := make(chan error, 1)
	go func() {
		firstDone <- f.r.TxCatalogWrite(ctx, func(tx *Repo) error {
			if _, err := tx.LockEngineForUpdate(ctx, engine); err != nil {
				return err
			}
			close(locked)
			<-release
			return nil
		})
	}()
	<-locked

	secondLocked := make(chan time.Time, 1)
	secondDone := make(chan error, 1)
	go func() {
		secondDone <- f.r.TxCatalogWrite(ctx, func(tx *Repo) error {
			if _, err := tx.LockEngineForUpdate(ctx, engine); err != nil {
				return err
			}
			secondLocked <- time.Now()
			return nil
		})
	}()

	select {
	case <-secondLocked:
		t.Fatal("a second transaction locked the engine while the first held it")
	case <-time.After(500 * time.Millisecond):
	}
	released := time.Now()
	close(release)
	if err := <-firstDone; err != nil {
		t.Fatalf("first transaction: %v", err)
	}
	if err := <-secondDone; err != nil {
		t.Fatalf("second transaction: %v", err)
	}
	if got := <-secondLocked; got.Before(released) {
		t.Fatal("the second lock was taken before the first transaction ended")
	}

	if _, err := f.r.LockEngineForUpdate(ctx, PGUUID(NewUUID())); !errors.Is(err, db.ErrNoRows) {
		t.Fatalf("locking an unknown engine: %v, want db.ErrNoRows", err)
	}
}

func TestDataPolicies_SubjectSources(t *testing.T) {
	t.Parallel()
	f, r, _, _ := dataPolicyFixture(t)
	ctx := context.Background()
	suspended := createAPIKeyUser(t, f.gdb, "suspended@example.test")
	if err := f.gdb.Model(&model.User{}).Where("id = ?", string(suspended.ID)).Update("status", "suspended").Error; err != nil {
		t.Fatal(err)
	}
	team := model.Team{ID: model.UUID(NewUUID()), Name: "data", CreatedBy: f.owner, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	if err := f.gdb.Create(&team).Error; err != nil {
		t.Fatal(err)
	}
	member := model.TeamMember{ID: model.UUID(NewUUID()), TeamID: team.ID, UserID: f.owner, Role: "member", JoinedAt: time.Now().UTC()}
	if err := f.gdb.Create(&member).Error; err != nil {
		t.Fatal(err)
	}
	owner := PGUUID(string(f.owner))
	if err := f.r.ReplaceUserIDPGroups(ctx, owner, []string{"analysts", " analysts ", "", "auditors"}); err != nil {
		t.Fatalf("ReplaceUserIDPGroups: %v", err)
	}

	sources, err := r.ListSubjectSources(ctx)
	if err != nil {
		t.Fatalf("ListSubjectSources: %v", err)
	}
	byID := map[string]datapolicy.SubjectSource{}
	for _, s := range sources {
		byID[s.UserID.String()] = s
	}
	if _, ok := byID[string(suspended.ID)]; ok {
		t.Fatal("a suspended account is in the subject map")
	}
	got, ok := byID[string(f.owner)]
	if !ok || !slices.Contains(got.Teams, string(team.ID)) {
		t.Fatalf("owner = %+v (%v), want its team", got, ok)
	}
	slices.Sort(got.Groups)
	if !slices.Equal(got.Groups, []string{"analysts", "auditors"}) {
		t.Fatalf("owner groups = %v, want them trimmed and deduplicated", got.Groups)
	}

	// The next sign-in replaces the groups, an empty one clears them.
	if err := f.r.ReplaceUserIDPGroups(ctx, owner, nil); err != nil {
		t.Fatal(err)
	}
	sources, err = r.ListSubjectSources(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range sources {
		if s.UserID.String() == string(f.owner) && len(s.Groups) != 0 {
			t.Fatalf("groups after an empty sign-in = %v", s.Groups)
		}
	}
}

// TestReplaceUserIDPGroups_ConcurrentSignIns: two sign-ins of one user that
// replace the groups at once leave one sign-in's groups, never a mix.
func TestReplaceUserIDPGroups_ConcurrentSignIns(t *testing.T) {
	t.Parallel()
	f, r, _, _ := dataPolicyFixture(t)
	ctx := context.Background()
	owner := PGUUID(string(f.owner))
	for range 10 {
		var wg sync.WaitGroup
		errs := make([]error, 2)
		start := make(chan struct{})
		for i, groups := range [][]string{{"privileged"}, {"plain"}} {
			wg.Go(func() {
				<-start
				errs[i] = f.r.ReplaceUserIDPGroups(ctx, owner, groups)
			})
		}
		close(start)
		wg.Wait()
		if err := errors.Join(errs...); err != nil {
			t.Fatal(err)
		}
		sources, err := r.ListSubjectSources(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range sources {
			if s.UserID.String() == string(f.owner) && len(s.Groups) != 1 {
				t.Fatalf("groups after two concurrent sign-ins = %v, want one sign-in's", s.Groups)
			}
		}
	}
}

func TestDataPolicies_PublishMovesThePointer(t *testing.T) {
	t.Parallel()
	_, r, _, author := dataPolicyFixture(t)
	ctx := context.Background()
	if _, err := r.PublishedRevision(ctx); !errors.Is(err, datapolicy.ErrNotFound) {
		t.Fatalf("PublishedRevision before any publish: %v, want ErrNotFound", err)
	}
	lease := time.Now().Add(time.Hour)
	first, err := r.PublishRevision(ctx, datapolicy.NewRevision(datapolicy.ReasonPublish, []byte(`{"catalogs":{}}`), lease, &author), uuid.Nil)
	if err != nil {
		t.Fatalf("PublishRevision: %v", err)
	}
	if got, err := r.PublishedRevision(ctx); err != nil || got.ID != first.ID {
		t.Fatalf("PublishedRevision = %v, %v; want the first", got.ID, err)
	}
	// A revision stored without publishing does not move the pointer.
	if _, err := r.CreateRevision(ctx, datapolicy.NewRevision(datapolicy.ReasonPublish, []byte(`{}`), lease, nil)); err != nil {
		t.Fatal(err)
	}
	second, err := r.PublishRevision(ctx, datapolicy.NewRevision(datapolicy.ReasonReissue, first.Content, lease.Add(time.Hour), nil), first.ID)
	if err != nil {
		t.Fatal(err)
	}
	// A publish built on a revision that is no longer published is refused.
	if _, err := r.PublishRevision(ctx, datapolicy.NewRevision(datapolicy.ReasonPublish, first.Content, lease, nil), first.ID); !errors.Is(err, datapolicy.ErrVersionConflict) {
		t.Fatalf("a publish over a stale base: %v, want ErrVersionConflict", err)
	}
	if _, err := r.PublishRevision(ctx, datapolicy.NewRevision(datapolicy.ReasonPublish, first.Content, lease, nil), uuid.Nil); !errors.Is(err, datapolicy.ErrVersionConflict) {
		t.Fatalf("a first publish when one exists: %v, want ErrVersionConflict", err)
	}
	got, err := r.PublishedRevision(ctx)
	if err != nil || got.ID != second.ID || got.Number != 3 {
		t.Fatalf("PublishedRevision = %+v, %v; want the re-issue, number 3", got, err)
	}
}

// TestDataPolicies_ConcurrentFirstPublications: of two first publications
// racing, one lands and the other fails with ErrVersionConflict; neither
// overwrites the other's pointer.
func TestDataPolicies_ConcurrentFirstPublications(t *testing.T) {
	t.Parallel()
	_, r, _, _ := dataPolicyFixture(t)
	ctx := context.Background()
	lease := time.Now().Add(time.Hour)
	const racers = 2
	var (
		wg        sync.WaitGroup
		published = make([]datapolicy.Revision, racers)
		errs      = make([]error, racers)
		start     = make(chan struct{})
	)
	for i := range racers {
		wg.Go(func() {
			<-start
			published[i], errs[i] = r.PublishRevision(ctx, datapolicy.NewRevision(datapolicy.ReasonPublish, []byte(`{"catalogs":{}}`), lease, nil), uuid.Nil)
		})
	}
	close(start)
	wg.Wait()
	var landed []datapolicy.Revision
	for i, err := range errs {
		switch {
		case err == nil:
			landed = append(landed, published[i])
		case !errors.Is(err, datapolicy.ErrVersionConflict):
			t.Fatalf("racing first publication: %v, want success or ErrVersionConflict", err)
		}
	}
	if len(landed) != 1 {
		t.Fatalf("%d first publications landed, want exactly one", len(landed))
	}
	if got, err := r.PublishedRevision(ctx); err != nil || got.ID != landed[0].ID {
		t.Fatalf("PublishedRevision = %v, %v; want the one that landed", got.ID, err)
	}
}

func TestDataPolicies_EngineStatus(t *testing.T) {
	t.Parallel()
	f, r, _, _ := dataPolicyFixture(t)
	ctx := context.Background()
	engine := uuid.MustParse(string(f.engine))
	at := time.Now().UTC().Truncate(time.Millisecond)
	if err := r.RecordEngineStatus(ctx, datapolicy.EngineStatus{EngineID: engine, ReportedAt: at, ActiveRevision: "r1", PolicyDigest: "d1"}); err != nil {
		t.Fatalf("RecordEngineStatus: %v", err)
	}
	long := strings.Repeat("é", 2000)
	if err := r.RecordEngineStatus(ctx, datapolicy.EngineStatus{EngineID: engine, ReportedAt: at.Add(time.Minute), Code: "bundle_error", Message: long}); err != nil {
		t.Fatalf("RecordEngineStatus replacing: %v", err)
	}
	statuses, err := r.ListEngineStatuses(ctx)
	if err != nil || len(statuses) != 1 {
		t.Fatalf("ListEngineStatuses = %+v, %v", statuses, err)
	}
	s := statuses[0]
	if s.EngineID != engine || s.ActiveRevision != "" || s.Code != "bundle_error" || utf8.RuneCountInString(s.Message) != 1024 || !s.ReportedAt.Equal(at.Add(time.Minute)) {
		t.Fatalf("status = %+v, want the latest report with its message truncated", s)
	}
}
