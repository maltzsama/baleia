// Package metrics manages a workspace's semantic metrics (#115): structured
// definitions on a catalog table, kept in spaces and versioned through
// content.Service (#34, #40). A write runs in two steps so the caller can
// check the catalog, probe the definition on an engine and authorize the
// space in between: Prepare or Merge validate and build it, Create or Update
// store it.
package metrics

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/semantic"
	"github.com/galileostudio/baleia/internal/slug"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

// Store reads metrics and records their certification; *db.Queries
// implements it.
type Store interface {
	ListVisibleSemanticMetrics(ctx context.Context, arg db.ListVisibleSemanticMetricsParams) ([]db.SemanticMetric, error)
	ListSemanticMetricVersions(ctx context.Context, workspaceID pgtype.UUID) ([]db.ListSemanticMetricVersionsRow, error)
	GetSemanticMetric(ctx context.Context, arg db.GetSemanticMetricParams) (db.SemanticMetric, error)
	SetSemanticMetricCertification(ctx context.Context, arg db.SetSemanticMetricCertificationParams) (db.SemanticMetric, error)
}

// Content writes metrics and their versions; *content.Service implements it.
type Content interface {
	CreateSemanticMetric(ctx context.Context, actor pgtype.UUID, in db.CreateSemanticMetricParams) (db.SemanticMetric, error)
	UpdateSemanticMetric(ctx context.Context, actor pgtype.UUID, in db.UpdateSemanticMetricParams, expectedVersion *int32) (db.SemanticMetric, bool, error)
	DeleteSemanticMetric(ctx context.Context, actor pgtype.UUID, workspaceID, id pgtype.UUID) error
}

var (
	// ErrNotFound: no metric has this id in the workspace.
	ErrNotFound = errors.New("metrics: not found")
	// ErrSlugTaken: the workspace already has a metric with this slug.
	ErrSlugTaken = errors.New("a metric with this slug already exists in this workspace")
	// ErrCertifyForbidden: only a space manager may certify a metric.
	ErrCertifyForbidden = errors.New("only a space manager may certify a metric")
)

// InvalidDefinitionError: the definition does not parse.
type InvalidDefinitionError struct{ Err error }

func (e *InvalidDefinitionError) Error() string { return e.Err.Error() }
func (e *InvalidDefinitionError) Unwrap() error { return e.Err }

// InvalidFieldError: a slug, label or table is missing or malformed.
type InvalidFieldError struct{ Msg string }

func (e *InvalidFieldError) Error() string { return e.Msg }

// Service manages metrics.
type Service struct {
	store   Store
	content Content
}

// New builds the service.
func New(store Store, content Content) *Service { return &Service{store: store, content: content} }

// Item is a listed metric and its latest content version.
type Item struct {
	Metric  db.SemanticMetric
	Version int32
}

// Filter narrows a listing; nil fields do not filter. Q matches the label,
// slug or description, case-insensitively.
type Filter struct {
	SpaceID   *pgtype.UUID
	CatalogID *pgtype.UUID
	Certified *bool
	OwnerID   *pgtype.UUID
	Q         string
	// Limit is the most items returned; zero is no limit.
	Limit int
}

// List returns the metrics the user can see, with their versions.
func (s *Service) List(ctx context.Context, ws, userID pgtype.UUID, f Filter) ([]Item, error) {
	all, err := s.store.ListVisibleSemanticMetrics(ctx, db.ListVisibleSemanticMetricsParams{WorkspaceID: ws, UserID: userID})
	if err != nil {
		return nil, err
	}
	versions, err := s.store.ListSemanticMetricVersions(ctx, ws)
	if err != nil {
		return nil, err
	}
	byID := make(map[[16]byte]int32, len(versions))
	for _, v := range versions {
		byID[v.EntityID.Bytes] = v.Version
	}
	q := strings.ToLower(strings.TrimSpace(f.Q))
	out := make([]Item, 0, len(all))
	for _, m := range all {
		switch {
		case f.SpaceID != nil && m.SpaceID.Bytes != f.SpaceID.Bytes,
			f.CatalogID != nil && m.CatalogID.Bytes != f.CatalogID.Bytes,
			f.Certified != nil && m.Certified != *f.Certified,
			f.OwnerID != nil && m.OwnerUserID.Bytes != f.OwnerID.Bytes,
			q != "" && !strings.Contains(strings.ToLower(m.Label), q) && !strings.Contains(strings.ToLower(m.Slug), q) && !strings.Contains(strings.ToLower(m.Description), q):
			continue
		}
		out = append(out, Item{Metric: m, Version: byID[m.ID.Bytes]})
		if f.Limit > 0 && len(out) >= f.Limit {
			break
		}
	}
	return out, nil
}

// Get returns one metric of the workspace.
func (s *Service) Get(ctx context.Context, ws, id pgtype.UUID) (db.SemanticMetric, error) {
	m, err := s.store.GetSemanticMetric(ctx, db.GetSemanticMetricParams{WorkspaceID: ws, ID: id})
	if errors.Is(err, pgx.ErrNoRows) {
		return db.SemanticMetric{}, ErrNotFound
	}
	return m, err
}

// Fields are a metric's editable fields.
type Fields struct {
	Slug        string
	Label       string
	Description string
	Certified   bool
	CatalogID   pgtype.UUID
	Namespace   []string
	Table       string
	BindingID   pgtype.UUID
	OwnerID     pgtype.UUID
}

// Prepared is a validated metric, ready to probe and store.
type Prepared struct {
	Fields
	Def semantic.SimpleMetricV1
	// Raw is the normalized definition stored in the database.
	Raw []byte
}

// Prepare parses a new metric's definition and checks its fields.
func Prepare(f Fields, definition map[string]any) (Prepared, error) {
	def, raw, err := ParseDefinition(definition)
	if err != nil {
		return Prepared{}, &InvalidDefinitionError{Err: err}
	}
	if err := checkFields(f); err != nil {
		return Prepared{}, err
	}
	return Prepared{Fields: f, Def: def, Raw: raw}, nil
}

// Create stores a prepared metric in spaceID as actor. Certifying needs
// canCertify.
func (s *Service) Create(ctx context.Context, actor, ws, spaceID pgtype.UUID, p Prepared, canCertify bool) (db.SemanticMetric, error) {
	if p.Certified && !canCertify {
		return db.SemanticMetric{}, ErrCertifyForbidden
	}
	m, err := s.content.CreateSemanticMetric(ctx, actor, db.CreateSemanticMetricParams{
		WorkspaceID:   ws,
		SpaceID:       spaceID,
		Slug:          p.Slug,
		Label:         p.Label,
		Description:   p.Description,
		Certified:     p.Certified,
		CatalogID:     p.CatalogID,
		Namespace:     p.Namespace,
		TableName:     p.Table,
		BindingID:     p.BindingID,
		Definition:    p.Raw,
		FormatVersion: semantic.FormatVersion,
		OwnerUserID:   p.OwnerID,
		UpdatedBy:     actor,
	})
	if isUniqueViolation(err) {
		return db.SemanticMetric{}, ErrSlugTaken
	}
	if err != nil {
		return db.SemanticMetric{}, err
	}
	if p.Certified {
		// Records who certified and when; best effort, the metric exists.
		if certified, cerr := s.store.SetSemanticMetricCertification(ctx, db.SetSemanticMetricCertificationParams{
			WorkspaceID: ws, ID: m.ID, Certified: true, Actor: actor,
		}); cerr == nil {
			m = certified
		}
	}
	return m, nil
}

// Change is a partial update; nil fields keep their value.
type Change struct {
	Slug        *string
	Label       *string
	Description *string
	Certified   *bool
	CatalogID   *pgtype.UUID
	Namespace   *[]string
	Table       *string
	BindingID   *pgtype.UUID
	OwnerID     *pgtype.UUID
	Definition  *map[string]any
}

// Merge applies change to current and validates the result. A certification
// change needs isManager; a definition change by anyone else uncertifies.
func Merge(current db.SemanticMetric, change Change, isManager bool) (Prepared, error) {
	if change.Certified != nil && *change.Certified != current.Certified && !isManager {
		return Prepared{}, ErrCertifyForbidden
	}
	changesDefinition := change.Definition != nil || change.CatalogID != nil || change.Namespace != nil || change.Table != nil
	f := Fields{
		Slug: current.Slug, Label: current.Label, Description: current.Description, Certified: current.Certified,
		CatalogID: current.CatalogID, Namespace: current.Namespace, Table: current.TableName,
		BindingID: current.BindingID, OwnerID: current.OwnerUserID,
	}
	set := func(dst *string, src *string) {
		if src != nil {
			*dst = *src
		}
	}
	set(&f.Slug, change.Slug)
	set(&f.Label, change.Label)
	set(&f.Description, change.Description)
	set(&f.Table, change.Table)
	if change.Certified != nil {
		f.Certified = *change.Certified
	} else if changesDefinition && current.Certified && !isManager {
		f.Certified = false
	}
	if change.CatalogID != nil {
		f.CatalogID = *change.CatalogID
	}
	if change.Namespace != nil {
		f.Namespace = *change.Namespace
	}
	if change.BindingID != nil {
		f.BindingID = *change.BindingID
	}
	if change.OwnerID != nil {
		f.OwnerID = *change.OwnerID
	}

	raw := current.Definition
	if change.Definition != nil {
		_, b, err := ParseDefinition(*change.Definition)
		if err != nil {
			return Prepared{}, &InvalidDefinitionError{Err: err}
		}
		raw = b
	}
	// An unchanged definition is stored as it is, not renormalized.
	def, _, err := ParseDefinitionRaw(raw)
	if err != nil {
		return Prepared{}, &InvalidDefinitionError{Err: err}
	}
	if err := checkFields(f); err != nil {
		return Prepared{}, err
	}
	return Prepared{Fields: f, Def: def, Raw: raw}, nil
}

// Update stores a merged metric over current as actor. A version conflict is
// returned as content reports it.
func (s *Service) Update(ctx context.Context, actor pgtype.UUID, current db.SemanticMetric, p Prepared, expectedVersion *int32) (db.SemanticMetric, error) {
	updated, _, err := s.content.UpdateSemanticMetric(ctx, actor, db.UpdateSemanticMetricParams{
		WorkspaceID:   current.WorkspaceID,
		SpaceID:       current.SpaceID,
		ID:            current.ID,
		Slug:          p.Slug,
		Label:         p.Label,
		Description:   p.Description,
		Certified:     p.Certified,
		CatalogID:     p.CatalogID,
		Namespace:     p.Namespace,
		TableName:     p.Table,
		BindingID:     p.BindingID,
		Definition:    p.Raw,
		FormatVersion: semantic.FormatVersion,
		OwnerUserID:   p.OwnerID,
		UpdatedBy:     actor,
	}, expectedVersion)
	if isUniqueViolation(err) {
		return db.SemanticMetric{}, ErrSlugTaken
	}
	if err != nil {
		return db.SemanticMetric{}, err
	}
	// Certified_by/at follow the certification; best effort, the update landed.
	if p.Certified != current.Certified || (updated.Certified && !updated.CertifiedAt.Valid) {
		if certified, cerr := s.store.SetSemanticMetricCertification(ctx, db.SetSemanticMetricCertificationParams{
			WorkspaceID: current.WorkspaceID, ID: current.ID, Certified: updated.Certified, Actor: actor,
		}); cerr == nil {
			updated = certified
		}
	}
	return updated, nil
}

// Delete removes a metric as actor.
func (s *Service) Delete(ctx context.Context, actor, ws, id pgtype.UUID) error {
	return s.content.DeleteSemanticMetric(ctx, actor, ws, id)
}

// ParseDefinition decodes a free-form definition, rejecting unknown fields,
// and returns it with the normalized JSON stored in the database.
func ParseDefinition(in map[string]any) (semantic.SimpleMetricV1, []byte, error) {
	raw, err := json.Marshal(in)
	if err != nil {
		return semantic.SimpleMetricV1{}, nil, err
	}
	return ParseDefinitionRaw(raw)
}

// ParseDefinitionRaw parses a stored or submitted definition and normalizes it.
func ParseDefinitionRaw(raw []byte) (semantic.SimpleMetricV1, []byte, error) {
	def, err := semantic.ParseSimpleMetricV1(raw)
	if err != nil {
		return semantic.SimpleMetricV1{}, nil, err
	}
	normalized, err := json.Marshal(def)
	if err != nil {
		return semantic.SimpleMetricV1{}, nil, err
	}
	return def, normalized, nil
}

func checkFields(f Fields) error {
	switch {
	case !slug.Valid(f.Slug):
		return &InvalidFieldError{Msg: "slug must match ^[a-z][a-z0-9_]{0,62}$"}
	case strings.TrimSpace(f.Label) == "":
		return &InvalidFieldError{Msg: "label is required"}
	case strings.TrimSpace(f.Table) == "":
		return &InvalidFieldError{Msg: "table is required"}
	}
	return nil
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505" // unique_violation
}
