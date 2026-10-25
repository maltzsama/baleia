// Package models manages a workspace's semantic models (#219): a table's
// dimensions, measures and joins, kept in spaces and versioned through
// content.Service (#34). A model is validated against the other models the
// user can see, so joins and field names resolve. Like metrics, a write runs
// in steps so the caller can check the catalog and authorize the space in
// between.
package models

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/semantic"
	"github.com/galileostudio/baleia/internal/slug"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

// Store reads models and records their certification; *db.Queries
// implements it.
type Store interface {
	ListVisibleSemanticModels(ctx context.Context, arg db.ListVisibleSemanticModelsParams) ([]db.SemanticModel, error)
	GetSemanticModel(ctx context.Context, arg db.GetSemanticModelParams) (db.SemanticModel, error)
	SetSemanticModelCertification(ctx context.Context, arg db.SetSemanticModelCertificationParams) (db.SemanticModel, error)
}

// Content writes models and their versions; *content.Service implements it.
type Content interface {
	CreateSemanticModel(ctx context.Context, actor pgtype.UUID, in db.CreateSemanticModelParams) (db.SemanticModel, error)
	UpdateSemanticModel(ctx context.Context, actor pgtype.UUID, in db.UpdateSemanticModelParams, expectedVersion *int32) (db.SemanticModel, bool, error)
	DeleteSemanticModel(ctx context.Context, actor pgtype.UUID, workspaceID, id pgtype.UUID) error
}

// FormatVersion is the stored definition format.
const FormatVersion = 1

var (
	// ErrNotFound: no model has this id in the workspace.
	ErrNotFound = errors.New("models: not found")
	// ErrSlugTaken: the workspace already has a model with this slug.
	ErrSlugTaken = errors.New("a model with this slug already exists in this workspace")
	// ErrCertifyForbidden: only a space manager may certify a model.
	ErrCertifyForbidden = errors.New("only a space manager may certify a model")
)

// InvalidDefinitionError: the definition does not parse or does not validate.
type InvalidDefinitionError struct{ Err error }

func (e *InvalidDefinitionError) Error() string { return e.Err.Error() }
func (e *InvalidDefinitionError) Unwrap() error { return e.Err }

// InvalidFieldError: a slug or label is missing or malformed.
type InvalidFieldError struct{ Msg string }

func (e *InvalidFieldError) Error() string { return e.Msg }

// Service manages models.
type Service struct {
	store   Store
	content Content
}

// New builds the service.
func New(store Store, content Content) *Service { return &Service{store: store, content: content} }

// List returns the models the user can see, of one space when spaceID is valid.
func (s *Service) List(ctx context.Context, ws, userID, spaceID pgtype.UUID) ([]db.SemanticModel, error) {
	return s.store.ListVisibleSemanticModels(ctx, db.ListVisibleSemanticModelsParams{WorkspaceID: ws, UserID: userID, SpaceID: spaceID})
}

// Get returns one model of the workspace.
func (s *Service) Get(ctx context.Context, ws, id pgtype.UUID) (db.SemanticModel, error) {
	m, err := s.store.GetSemanticModel(ctx, db.GetSemanticModelParams{WorkspaceID: ws, ID: id})
	if errors.Is(err, pgx.ErrNoRows) {
		return db.SemanticModel{}, ErrNotFound
	}
	return m, err
}

// Visible returns the definitions of every model the user can see, by slug.
// A malformed model is skipped: it must not block a valid edit.
func (s *Service) Visible(ctx context.Context, ws, userID pgtype.UUID) (map[string]semantic.Model, error) {
	rows, err := s.store.ListVisibleSemanticModels(ctx, db.ListVisibleSemanticModelsParams{WorkspaceID: ws, UserID: userID})
	if err != nil {
		return nil, err
	}
	out := make(map[string]semantic.Model, len(rows))
	for i := range rows {
		m, err := ParseStored(rows[i].Definition)
		if err != nil {
			continue
		}
		m.Slug = rows[i].Slug
		m.CatalogID = uuid.UUID(rows[i].CatalogID.Bytes).String()
		out[m.Slug] = m
	}
	return out, nil
}

// Fields are a model's editable fields.
type Fields struct {
	Slug        string
	Label       string
	Description string
	Certified   bool
	CatalogID   pgtype.UUID
	BindingID   pgtype.UUID
	OwnerID     pgtype.UUID
}

// Prepared is a validated model, ready to store.
type Prepared struct {
	Fields
	// Raw is the definition stored in the database.
	Raw []byte
}

// PrepareNew checks a new model's fields, parses its definition and
// validates it against the models the user can see.
func (s *Service) PrepareNew(ctx context.Context, ws, userID pgtype.UUID, f Fields, definition map[string]any) (Prepared, error) {
	if err := checkFields(f); err != nil {
		return Prepared{}, err
	}
	def, raw, err := ParseDefinition(definition)
	if err != nil {
		return Prepared{}, &InvalidDefinitionError{Err: err}
	}
	visible, err := s.Visible(ctx, ws, userID)
	if err != nil {
		return Prepared{}, err
	}
	if err := validate(def, f, visible); err != nil {
		return Prepared{}, err
	}
	return Prepared{Fields: f, Raw: raw}, nil
}

// Create stores a prepared model in spaceID as actor. Certifying needs
// canCertify.
func (s *Service) Create(ctx context.Context, actor, ws, spaceID pgtype.UUID, p Prepared, canCertify bool) (db.SemanticModel, error) {
	if p.Certified && !canCertify {
		return db.SemanticModel{}, ErrCertifyForbidden
	}
	m, err := s.content.CreateSemanticModel(ctx, actor, db.CreateSemanticModelParams{
		WorkspaceID:   ws,
		SpaceID:       spaceID,
		Slug:          p.Slug,
		Label:         p.Label,
		Description:   p.Description,
		Certified:     p.Certified,
		CatalogID:     p.CatalogID,
		BindingID:     p.BindingID,
		Definition:    p.Raw,
		FormatVersion: FormatVersion,
		OwnerUserID:   p.OwnerID,
		UpdatedBy:     actor,
	})
	if isUniqueViolation(err) {
		return db.SemanticModel{}, ErrSlugTaken
	}
	if err != nil {
		return db.SemanticModel{}, err
	}
	if p.Certified {
		// Records who certified and when; best effort, the model exists.
		if certified, cerr := s.store.SetSemanticModelCertification(ctx, db.SetSemanticModelCertificationParams{
			WorkspaceID: ws, ID: m.ID, Certified: true, Actor: actor,
		}); cerr == nil {
			m = certified
		}
	}
	return m, nil
}

// Change is a partial update of the fields; nil fields keep their value.
type Change struct {
	Slug        *string
	Label       *string
	Description *string
	Certified   *bool
	CatalogID   *pgtype.UUID
	BindingID   *pgtype.UUID
	OwnerID     *pgtype.UUID
	// ChangesDefinition says the update also replaces the definition.
	ChangesDefinition bool
}

// MergeFields applies change to current's fields and checks them. A
// certification change needs isManager; a definition, catalog or binding
// change by anyone else uncertifies.
func MergeFields(current db.SemanticModel, change Change, isManager bool) (Fields, error) {
	if change.Certified != nil && *change.Certified != current.Certified && !isManager {
		return Fields{}, ErrCertifyForbidden
	}
	f := Fields{
		Slug: current.Slug, Label: current.Label, Description: current.Description, Certified: current.Certified,
		CatalogID: current.CatalogID, BindingID: current.BindingID, OwnerID: current.OwnerUserID,
	}
	set := func(dst *string, src *string) {
		if src != nil {
			*dst = *src
		}
	}
	set(&f.Slug, change.Slug)
	set(&f.Label, change.Label)
	set(&f.Description, change.Description)
	changesDefinition := change.ChangesDefinition || change.CatalogID != nil || change.BindingID != nil
	if change.Certified != nil {
		f.Certified = *change.Certified
	} else if changesDefinition && current.Certified && !isManager {
		f.Certified = false
	}
	if change.CatalogID != nil {
		f.CatalogID = *change.CatalogID
	}
	if change.BindingID != nil {
		f.BindingID = *change.BindingID
	}
	if change.OwnerID != nil {
		f.OwnerID = *change.OwnerID
	}
	if err := checkFields(f); err != nil {
		return Fields{}, err
	}
	return f, nil
}

// PrepareUpdate parses the new definition, or keeps current's, and validates
// it with f against the other models the user can see.
func (s *Service) PrepareUpdate(ctx context.Context, ws, userID pgtype.UUID, current db.SemanticModel, f Fields, definition *map[string]any) (Prepared, error) {
	raw := current.Definition
	var def semantic.Model
	if definition != nil {
		var err error
		if def, raw, err = ParseDefinition(*definition); err != nil {
			return Prepared{}, &InvalidDefinitionError{Err: err}
		}
	} else if err := json.Unmarshal(raw, &def); err != nil {
		return Prepared{}, &InvalidDefinitionError{Err: err}
	}
	visible, err := s.Visible(ctx, ws, userID)
	if err != nil {
		return Prepared{}, err
	}
	delete(visible, current.Slug)
	if err := validate(def, f, visible); err != nil {
		return Prepared{}, err
	}
	return Prepared{Fields: f, Raw: raw}, nil
}

// Update stores a prepared model over current as actor. A version conflict
// is returned as content reports it.
func (s *Service) Update(ctx context.Context, actor pgtype.UUID, current db.SemanticModel, p Prepared, expectedVersion *int32) (db.SemanticModel, error) {
	updated, _, err := s.content.UpdateSemanticModel(ctx, actor, db.UpdateSemanticModelParams{
		WorkspaceID:   current.WorkspaceID,
		SpaceID:       current.SpaceID,
		ID:            current.ID,
		Slug:          p.Slug,
		Label:         p.Label,
		Description:   p.Description,
		Certified:     p.Certified,
		CatalogID:     p.CatalogID,
		BindingID:     p.BindingID,
		Definition:    p.Raw,
		FormatVersion: FormatVersion,
		OwnerUserID:   p.OwnerID,
		UpdatedBy:     actor,
	}, expectedVersion)
	if isUniqueViolation(err) {
		return db.SemanticModel{}, ErrSlugTaken
	}
	if err != nil {
		return db.SemanticModel{}, err
	}
	// Certified_by/at follow the certification; best effort, the update landed.
	if p.Certified != current.Certified || (updated.Certified && !updated.CertifiedAt.Valid) {
		if certified, cerr := s.store.SetSemanticModelCertification(ctx, db.SetSemanticModelCertificationParams{
			WorkspaceID: current.WorkspaceID, ID: current.ID, Certified: updated.Certified, Actor: actor,
		}); cerr == nil {
			updated = certified
		}
	}
	return updated, nil
}

// Delete removes a model as actor.
func (s *Service) Delete(ctx context.Context, actor, ws, id pgtype.UUID) error {
	return s.content.DeleteSemanticModel(ctx, actor, ws, id)
}

// ParseDefinition decodes a submitted definition, rejecting unknown fields,
// and returns it with the JSON to store.
func ParseDefinition(in map[string]any) (semantic.Model, []byte, error) {
	raw, err := json.Marshal(in)
	if err != nil {
		return semantic.Model{}, nil, err
	}
	m, err := ParseStored(raw)
	if err != nil {
		return semantic.Model{}, nil, err
	}
	return m, raw, nil
}

// ParseStored decodes a stored definition, rejecting unknown fields.
func ParseStored(raw []byte) (semantic.Model, error) {
	var m semantic.Model
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		return semantic.Model{}, err
	}
	return m, nil
}

// validate binds def to its row fields and checks it against the models it
// may join.
func validate(def semantic.Model, f Fields, others map[string]semantic.Model) error {
	def.Slug = f.Slug
	def.CatalogID = uuid.UUID(f.CatalogID.Bytes).String()
	def.BindingID = ""
	if f.BindingID.Valid {
		def.BindingID = uuid.UUID(f.BindingID.Bytes).String()
	}
	if err := def.Validate(others); err != nil {
		return &InvalidDefinitionError{Err: err}
	}
	return nil
}

func checkFields(f Fields) error {
	switch {
	case !slug.Valid(f.Slug):
		return &InvalidFieldError{Msg: "slug must match ^[a-z][a-z0-9_]{0,62}$"}
	case strings.TrimSpace(f.Label) == "":
		return &InvalidFieldError{Msg: "label is required"}
	}
	return nil
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505" // unique_violation
}
