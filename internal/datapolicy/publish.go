package datapolicy

import (
	"bytes"
	"context"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
	"time"

	"github.com/google/uuid"
)

// PublishStore is the part of the store publication needs; *repo.DataPolicies
// implements it.
type PublishStore interface {
	ListByCatalog(ctx context.Context, catalogID uuid.UUID) ([]Policy, error)
	PublishedRevision(ctx context.Context) (Revision, error)
	PublishRevision(ctx context.Context, rev Revision, base uuid.UUID) (Revision, error)
	GetRevision(ctx context.Context, id uuid.UUID) (Revision, error)
	ListSubjectSources(ctx context.Context) ([]SubjectSource, error)
}

// ObjectStore keeps the signed bundles; *storage.S3Client implements it.
type ObjectStore interface {
	Put(ctx context.Context, key string, data []byte, contentType string) error
	GetReader(ctx context.Context, key string) (io.ReadCloser, string, error)
}

// ViewInspection is what the catalogs bound to enforcing engines hold in
// views: every view Baleia could read, and the engine catalog names of the
// catalogs that can hold Trino views but whose views Baleia cannot read.
type ViewInspection struct {
	Views         []CatalogView
	Uninspectable []string
}

// Publish problem codes beyond PublishProblems'.
const (
	ProblemNameConflict     = "catalog_name_conflict"
	ProblemTrustedDefiner   = "trusted_definer_view"
	ProblemViewsUninspected = "views_not_inspectable"
)

const (
	bundleContentType  = "application/gzip"
	bundleObjectPrefix = "data-policies/bundles/"
	// maxPublishAttempts bounds the retries of a publish that lost the race
	// for the pointer to another one.
	maxPublishAttempts = 3
	// reissueBeforeLeaseEndPct re-issues the published revision once less
	// than this share of its lease is left.
	reissueBeforeLeaseEndPct = 50
)

// RefusedError is a publish Baleia refuses, with every reason.
type RefusedError struct{ Problems []Problem }

func (e *RefusedError) Error() string {
	return fmt.Sprintf("data policy publish refused: %d problem(s)", len(e.Problems))
}

// BundleKey is the object storage key of a revision's signed bundle.
func BundleKey(revision uuid.UUID) string { return bundleObjectPrefix + revision.String() + ".tar.gz" }

// Publisher builds, signs, stores and publishes revisions (ADR 0028). Each
// revision is built once: its bundle is written to object storage under the
// revision's id before the pointer moves to it, and never rewritten.
type Publisher struct {
	Store        PublishStore
	Objects      ObjectStore
	Key          *rsa.PrivateKey
	Lease        time.Duration
	IdentityMode string
	// Trusted are the trusted Trino identities (admin and service users).
	Trusted map[string]bool
	Now     func() time.Time
}

func (p *Publisher) now() time.Time {
	if p.Now != nil {
		return p.Now().UTC()
	}
	return time.Now().UTC()
}

// PublishCatalog publishes a catalog's current policies under names, the
// catalog's names on the engines that enforce data policies, keeping every
// other catalog as the published revision has it. A catalog without enabled
// policies leaves the bundle. It refuses invalid or unchecked policies, two
// masks with one priority on a column, an engine catalog name another
// catalog already has, catalogs whose views cannot be read, and trusted-owned
// DEFINER views over a policied table.
//
// The catalog's policies are read again at every attempt: a retry after a
// concurrent publish never writes a snapshot older than what that publish
// saw.
func (p *Publisher) PublishCatalog(ctx context.Context, catalogID uuid.UUID, names []string, views ViewInspection, by uuid.UUID) (Revision, error) {
	return p.publish(ctx, ReasonPublish, nil, &by, views, func(ctx context.Context, content *Content) error {
		policies, err := p.Store.ListByCatalog(ctx, catalogID)
		if err != nil {
			return fmt.Errorf("list the catalog's policies: %w", err)
		}
		if problems := PublishProblems(policies); len(problems) > 0 {
			return &RefusedError{Problems: problems}
		}
		catalog := CatalogContentOf(names, policies)
		if len(catalog.Tables) == 0 {
			delete(content.Catalogs, catalogID.String())
			return nil
		}
		content.Catalogs[catalogID.String()] = catalog
		return nil
	})
}

// CatalogNames returns a catalog's names on the engines that serve it now,
// for a rollback. It fails when the catalog cannot carry policies today (a
// binding on an engine that does not enforce them); a nil slice and no error
// mean the catalog is gone and its policies leave the bundle.
type CatalogNames func(ctx context.Context, catalogID uuid.UUID) ([]string, error)

// Rollback publishes an earlier revision's policies again, with today's
// subjects and a fresh lease. The earlier revision stays as it is. Each
// restored catalog takes the names names returns for it now, and the
// rollback fails with names' error when a catalog cannot carry policies
// today: a binding added since then may be on an engine that would not
// enforce them.
func (p *Publisher) Rollback(ctx context.Context, revisionID uuid.UUID, views ViewInspection, by uuid.UUID, names CatalogNames) (Revision, error) {
	source, err := p.Store.GetRevision(ctx, revisionID)
	if err != nil {
		return Revision{}, err
	}
	var earlier Content
	if err := json.Unmarshal(source.Content, &earlier); err != nil {
		return Revision{}, fmt.Errorf("read revision %s: %w", source.ID, err)
	}
	// The names are resolved on every attempt: a retry after a concurrent
	// publish also sees a binding renamed in between.
	return p.publish(ctx, ReasonRollback, &source.ID, &by, views, func(ctx context.Context, content *Content) error {
		restored := make(map[string]CatalogContent, len(earlier.Catalogs))
		for _, key := range slices.Sorted(maps.Keys(earlier.Catalogs)) {
			catalogID, err := uuid.Parse(key)
			if err != nil {
				return fmt.Errorf("read revision %s: catalog %q: %w", source.ID, key, err)
			}
			current, err := names(ctx, catalogID)
			if err != nil {
				return err
			}
			if current == nil {
				continue
			}
			catalog := earlier.Catalogs[key]
			catalog.Names = normalNames(current)
			restored[key] = catalog
		}
		content.Catalogs = restored
		return nil
	})
}

// PublishInitial publishes a revision without policies when nothing is
// published yet, and reports whether it did. OPA then has a bundle to
// activate, so the engines it gates become ready: an engine cannot take the
// catalog DDL that the first policy needs before that (ADR 0028). Nobody is
// filtered until the first policy is published, as before data policies were
// turned on. Once any revision is published it does nothing, and a replica
// that loses the race to another one reports false.
func (p *Publisher) PublishInitial(ctx context.Context) (Revision, bool, error) {
	_, err := p.Store.PublishedRevision(ctx)
	if err == nil {
		return Revision{}, false, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return Revision{}, false, err
	}
	subjects, err := p.subjects(ctx)
	if err != nil {
		return Revision{}, false, err
	}
	content := Content{Catalogs: map[string]CatalogContent{}, Subjects: subjects}
	rev, err := p.store(ctx, ReasonPublish, nil, nil, content, uuid.Nil)
	if errors.Is(err, ErrVersionConflict) {
		return Revision{}, false, nil
	}
	if err != nil {
		return Revision{}, false, err
	}
	return rev, true, nil
}

// Reissue keeps the published policies current: it publishes them again
// when the subjects changed (team membership, sign-in groups) or when less
// than half of the lease is left. While nothing is published it publishes
// the initial revision (PublishInitial). Re-issuing does not judge views: it
// never changes a policy, and a lapsed lease would deny every user.
func (p *Publisher) Reissue(ctx context.Context) (Revision, bool, error) {
	published, err := p.Store.PublishedRevision(ctx)
	if errors.Is(err, ErrNotFound) {
		return p.PublishInitial(ctx)
	}
	if err != nil {
		return Revision{}, false, err
	}
	var content Content
	if err := json.Unmarshal(published.Content, &content); err != nil {
		return Revision{}, false, fmt.Errorf("read revision %s: %w", published.ID, err)
	}
	subjects, err := p.subjects(ctx)
	if err != nil {
		return Revision{}, false, err
	}
	reason := ReasonSubjects
	if subjectsEqual(content.Subjects, subjects) {
		left := published.NotAfter.Sub(p.now())
		if left > p.Lease*reissueBeforeLeaseEndPct/100 {
			return published, false, nil
		}
		reason = ReasonReissue
	}
	content.Subjects = subjects
	rev, err := p.store(ctx, reason, &published.ID, nil, content, published.ID)
	if err != nil {
		return Revision{}, false, err
	}
	return rev, true, nil
}

// publish applies change to the published content, refreshes the subjects,
// checks the result and publishes it. A concurrent publish that landed first
// makes it start over from the new revision.
func (p *Publisher) publish(ctx context.Context, reason RevisionReason, source, by *uuid.UUID, views ViewInspection, change func(context.Context, *Content) error) (Revision, error) {
	for attempt := 1; ; attempt++ {
		content, base, err := p.published(ctx)
		if err != nil {
			return Revision{}, err
		}
		if err := change(ctx, &content); err != nil {
			return Revision{}, err
		}
		if content.Subjects, err = p.subjects(ctx); err != nil {
			return Revision{}, err
		}
		if problems := p.contentProblems(content, views); len(problems) > 0 {
			return Revision{}, &RefusedError{Problems: problems}
		}
		rev, err := p.store(ctx, reason, source, by, content, base)
		if errors.Is(err, ErrVersionConflict) && attempt < maxPublishAttempts {
			continue
		}
		return rev, err
	}
}

// published returns the published content and its revision id, or an empty
// content and uuid.Nil when nothing is published.
func (p *Publisher) published(ctx context.Context) (Content, uuid.UUID, error) {
	rev, err := p.Store.PublishedRevision(ctx)
	if errors.Is(err, ErrNotFound) {
		return Content{Catalogs: map[string]CatalogContent{}}, uuid.Nil, nil
	}
	if err != nil {
		return Content{}, uuid.Nil, err
	}
	var content Content
	if err := json.Unmarshal(rev.Content, &content); err != nil {
		return Content{}, uuid.Nil, fmt.Errorf("read revision %s: %w", rev.ID, err)
	}
	if content.Catalogs == nil {
		content.Catalogs = map[string]CatalogContent{}
	}
	return content, rev.ID, nil
}

func (p *Publisher) subjects(ctx context.Context) (map[string]SubjectEntry, error) {
	sources, err := p.Store.ListSubjectSources(ctx)
	if err != nil {
		return nil, fmt.Errorf("build the subject map: %w", err)
	}
	return Subjects(p.IdentityMode, sources), nil
}

// contentProblems are the reasons content cannot be published as a whole.
func (p *Publisher) contentProblems(content Content, views ViewInspection) []Problem {
	var problems []Problem
	for _, name := range content.NameConflicts() {
		problems = append(problems, Problem{Code: ProblemNameConflict, Detail: name})
	}
	policied := content.PoliciedTables()
	if len(policied) == 0 {
		return problems
	}
	for _, name := range slices.Sorted(slices.Values(views.Uninspectable)) {
		problems = append(problems, Problem{Code: ProblemViewsUninspected, Detail: name})
	}
	for _, v := range TrustedDefinerViews(DefinerReach(views.Views, policied), p.Trusted) {
		problems = append(problems, Problem{Code: ProblemTrustedDefiner, Detail: v.View.String()})
	}
	return problems
}

// store builds the revision of content, writes its signed bundle and then
// publishes it over base.
func (p *Publisher) store(ctx context.Context, reason RevisionReason, source, by *uuid.UUID, content Content, base uuid.UUID) (Revision, error) {
	if content.Catalogs == nil {
		content.Catalogs = map[string]CatalogContent{}
	}
	raw, err := json.Marshal(content)
	if err != nil {
		return Revision{}, fmt.Errorf("encode the revision: %w", err)
	}
	rev := NewRevision(reason, raw, p.now().Add(p.Lease), by)
	rev.SourceID = source
	tarball, err := Bundle(rev, content, p.Key)
	if err != nil {
		return Revision{}, err
	}
	// The object exists before any pointer can name it; a publish that then
	// fails leaves an unreferenced object, never a dangling pointer.
	if err := p.Objects.Put(ctx, BundleKey(rev.ID), tarball, bundleContentType); err != nil {
		return Revision{}, fmt.Errorf("store the bundle: %w", err)
	}
	return p.Store.PublishRevision(ctx, rev, base)
}

// CatalogChanged reports whether a catalog's current policies differ from
// what the published revision holds for it: the catalog has a draft.
func (p *Publisher) CatalogChanged(ctx context.Context, catalogID uuid.UUID, names []string) (bool, error) {
	policies, err := p.Store.ListByCatalog(ctx, catalogID)
	if err != nil {
		return false, err
	}
	content, _, err := p.published(ctx)
	if err != nil {
		return false, err
	}
	current := CatalogContentOf(names, policies)
	published, ok := content.Catalogs[catalogID.String()]
	if !ok {
		return len(current.Tables) > 0, nil
	}
	if len(current.Tables) > 0 && !slices.Equal(normalNames(current.Names), normalNames(published.Names)) {
		return true, nil
	}
	a, err := json.Marshal(current.Tables)
	if err != nil {
		return false, err
	}
	b, err := json.Marshal(published.Tables)
	if err != nil {
		return false, err
	}
	return !bytes.Equal(a, b), nil
}

// normalNames sorts names and drops repeats.
func normalNames(names []string) []string {
	return slices.Compact(slices.Sorted(slices.Values(names)))
}

func subjectsEqual(a, b map[string]SubjectEntry) bool {
	if len(a) != len(b) {
		return false
	}
	for _, k := range slices.Sorted(maps.Keys(a)) {
		x, y := a[k], b[k]
		if x.User != y.User || x.ServiceAccount != y.ServiceAccount || !slices.Equal(x.Teams, y.Teams) || !slices.Equal(x.Groups, y.Groups) {
			return false
		}
		if _, ok := b[k]; !ok {
			return false
		}
	}
	return true
}
