package api

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"sort"
	"strings"

	"github.com/galileostudio/baleia/internal/auth"
	"github.com/galileostudio/baleia/internal/authz"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/dialect"
	"github.com/galileostudio/baleia/internal/metadata/typemap"
	"github.com/galileostudio/baleia/internal/semantic"
	slugs "github.com/galileostudio/baleia/internal/slug"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

// migrationTable is one table used by v1 metrics: the unit the v1 -> v2
// migration turns into a model (#91, #121).
type migrationTable struct {
	catalogID pgtype.UUID
	namespace []string
	table     string
	metrics   []db.SemanticMetric
}

func (t migrationTable) key() string {
	return uuid.UUID(t.catalogID.Bytes).String() + "|" + strings.Join(t.namespace, ".") + "|" + t.table
}

// migrationGuards are the handler's authorization checks, handed to the
// per-table step.
type migrationGuards struct {
	granted func(catalog pgtype.UUID) (bool, error)
	canEdit func(space pgtype.UUID) (bool, error)
}

// MigrateSemanticMetricsV1 — POST /workspaces/{id}/semantic/models/migrate-v1
//
// Each table used by the caller's visible v1 metrics becomes a model (an
// existing model on the same table is reused) and each metric becomes a model
// metric with the same slug, so links keep resolving through the v1 page's
// redirect. Upsert by slug (FD16): nothing is deleted and a metric the model
// already has is left alone.
func (h *SemanticHandlers) MigrateSemanticMetricsV1(ctx context.Context, request MigrateSemanticMetricsV1RequestObject) (MigrateSemanticMetricsV1ResponseObject, error) {
	ws, nf, err := h.metadataWorkspace(ctx, request.Id)
	if err != nil {
		return nil, err
	}
	if nf {
		return MigrateSemanticMetricsV1404JSONResponse{NotFoundJSONResponse: metadataNotFound("workspace not found")}, nil
	}
	userID, err := uuidFromString(auth.UserIDFromContext(ctx))
	if err != nil {
		return MigrateSemanticMetricsV1401JSONResponse{UnauthorizedJSONResponse: UnauthorizedJSONResponse{Code: ErrorCodeUnauthorized, Message: "invalid user"}}, nil
	}
	dryRun := true
	choices := map[string][]string{}
	if request.Body != nil {
		if request.Body.DryRun != nil {
			dryRun = *request.Body.DryRun
		}
		if request.Body.Tables != nil {
			for _, t := range *request.Body.Tables {
				key := migrationTable{catalogID: pgtype.UUID{Bytes: t.CatalogId, Valid: true}, namespace: t.Namespace, table: t.Table}.key()
				choices[key] = t.PrimaryKey
			}
		}
	}

	metrics, err := h.queries.ListVisibleSemanticMetrics(ctx, db.ListVisibleSemanticMetricsParams{WorkspaceID: ws, UserID: userID})
	if err != nil {
		return nil, err
	}
	sort.Slice(metrics, func(i, j int) bool { return metrics[i].Slug < metrics[j].Slug })
	var tables []*migrationTable
	byKey := map[string]*migrationTable{}
	for _, m := range metrics {
		t := migrationTable{catalogID: m.CatalogID, namespace: m.Namespace, table: m.TableName}
		if existing, ok := byKey[t.key()]; ok {
			existing.metrics = append(existing.metrics, m)
			continue
		}
		t.metrics = []db.SemanticMetric{m}
		byKey[t.key()] = &t
		tables = append(tables, &t)
	}

	modelRows, err := h.queries.ListVisibleSemanticModels(ctx, db.ListVisibleSemanticModelsParams{WorkspaceID: ws, UserID: userID})
	if err != nil {
		return nil, err
	}
	visible, err := h.visibleModels(ctx, ws, userID)
	if err != nil {
		return nil, err
	}
	// Slugs taken in this run, so two tables named alike get distinct models.
	reserved := map[string]bool{}
	// The guards stay in the handler so the authz coverage test sees them:
	// metric visibility is the space role only, so reading a table's columns
	// and writing a model also need the catalog grant and an editable space.
	guards := migrationGuards{
		granted: func(catalog pgtype.UUID) (bool, error) {
			nf, err := h.semanticCatalog(ctx, ws, openapi_types.UUID(catalog.Bytes))
			return !nf, err
		},
		canEdit: func(space pgtype.UUID) (bool, error) {
			se, err := h.contentSpace(ctx, ws, space, authz.SpaceEditor)
			return err == nil && se == nil, err
		},
	}

	items := make([]MigrateV1Item, 0, len(tables))
	for _, t := range tables {
		item, err := h.migrateTable(ctx, ws, userID, guards, *t, modelRows, visible, reserved, choices[t.key()], dryRun)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return MigrateSemanticMetricsV1200JSONResponse(MigrateV1Report{DryRun: dryRun, Items: items}), nil
}

func (h *core) migrateTable(ctx context.Context, ws, userID pgtype.UUID, guards migrationGuards, t migrationTable, modelRows []db.SemanticModel, visible map[string]semantic.Model, reserved map[string]bool, primaryKey []string, dryRun bool) (MigrateV1Item, error) {
	item := MigrateV1Item{
		CatalogId: openapi_types.UUID(t.catalogID.Bytes),
		Namespace: t.namespace,
		Table:     t.table,
		Metrics:   make([]string, 0, len(t.metrics)),
		Columns:   []string{},
	}
	for _, m := range t.metrics {
		item.Metrics = append(item.Metrics, m.Slug)
	}
	fail := func(code MigrateV1ItemErrorCode, msg string) (MigrateV1Item, error) {
		item.Action = MigrateV1ItemActionERROR
		item.ErrorCode = &code
		item.Error = &msg
		if item.PrimaryKey == nil {
			item.PrimaryKey = []string{}
		}
		return item, nil
	}

	if granted, err := guards.granted(t.catalogID); err != nil {
		return MigrateV1Item{}, err
	} else if !granted {
		return fail(MigrateV1ItemErrorCodeCatalogNotGranted, "the catalog of "+t.table+" is not granted to this workspace")
	}

	columns, err := h.migrationColumns(ctx, t)
	if err != nil {
		return MigrateV1Item{}, err
	}
	for _, c := range columns {
		item.Columns = append(item.Columns, c.Name)
	}

	var existing *db.SemanticModel
	for i := range modelRows {
		def, err := parseSemanticModel(modelRows[i].Definition)
		if err != nil || def.Source.Kind != semantic.SourceTable {
			continue
		}
		if modelRows[i].CatalogID.Bytes == t.catalogID.Bytes && def.Source.Table == t.table && slices.Equal(def.Source.Namespace, t.namespace) {
			existing = &modelRows[i]
			break
		}
	}

	var model semantic.Model
	var spaceID pgtype.UUID
	if existing != nil {
		def, _ := parseSemanticModel(existing.Definition)
		model = def
		model.Slug = existing.Slug
		model.CatalogID = uuid.UUID(existing.CatalogID.Bytes).String()
		if existing.BindingID.Valid {
			model.BindingID = uuid.UUID(existing.BindingID.Bytes).String()
		}
		spaceID = existing.SpaceID
		id := openapi_types.UUID(existing.ID.Bytes)
		item.ModelId = &id
		item.ModelSlug = existing.Slug
		item.PrimaryKey = model.PrimaryKey
	} else {
		slug, err := h.migrationSlug(ctx, ws, t.table, reserved)
		if err != nil {
			return MigrateV1Item{}, err
		}
		item.ModelSlug = slug
		if len(primaryKey) == 0 {
			for _, c := range columns {
				if strings.EqualFold(c.Name, "id") {
					primaryKey = []string{c.Name}
				}
			}
		}
		item.PrimaryKey = primaryKey
		if len(primaryKey) == 0 {
			return fail(MigrateV1ItemErrorCodePrimaryKeyRequired, "choose the primary key columns of "+t.table)
		}
		for _, k := range primaryKey {
			if !slices.ContainsFunc(columns, func(c semantic.ColumnType) bool { return c.Name == k }) {
				return fail(MigrateV1ItemErrorCodePrimaryKeyInvalid, "primary key column "+k+" is not a column of "+t.table)
			}
		}
		model = semantic.Model{
			Slug:       slug,
			Label:      t.table,
			CatalogID:  uuid.UUID(t.catalogID.Bytes).String(),
			Source:     semantic.ModelSource{Kind: semantic.SourceTable, Namespace: t.namespace, Table: t.table},
			PrimaryKey: primaryKey,
		}
		// The model lives where the caller may write: the first metric's space
		// they edit.
		for _, m := range t.metrics {
			if ok, err := guards.canEdit(m.SpaceID); err != nil {
				return MigrateV1Item{}, err
			} else if ok {
				spaceID = m.SpaceID
				break
			}
		}
		if !spaceID.Valid {
			return fail(MigrateV1ItemErrorCodeSpaceForbidden, "you may not edit the space of any of these metrics")
		}
		if b := t.metrics[0].BindingID; b.Valid {
			model.BindingID = uuid.UUID(b.Bytes).String()
		}
	}
	space := openapi_types.UUID(spaceID.Bytes)
	item.SpaceId = &space

	added := 0
	for _, m := range t.metrics {
		if slices.ContainsFunc(model.Metrics, func(x semantic.Metric) bool { return x.Name == m.Slug }) {
			continue
		}
		v1, err := semantic.ParseSimpleMetricV1(m.Definition)
		if err != nil {
			return fail(MigrateV1ItemErrorCodeInvalidMetric, "metric "+m.Slug+": "+err.Error())
		}
		converted, metric, err := semantic.MigrateV1(semantic.MigrateV1Input{
			CatalogID: model.CatalogID, ModelSlug: model.Slug, Label: model.Label,
			Source: model.Source, PrimaryKey: model.PrimaryKey,
			MetricSlug: m.Slug, Metric: v1, Columns: columns,
		})
		if err != nil {
			return fail(MigrateV1ItemErrorCodeInvalidMetric, "metric "+m.Slug+": "+err.Error())
		}
		metric.Label = m.Label
		// The v1 page redirects only to a metric that records where it came
		// from, never to one that merely shares the slug.
		metric.MigratedFrom = uuid.UUID(m.ID.Bytes).String()
		for _, d := range converted.Dimensions {
			if !slices.ContainsFunc(model.Dimensions, func(x semantic.Dimension) bool { return x.Name == d.Name }) {
				model.Dimensions = append(model.Dimensions, d)
			}
		}
		model.Metrics = append(model.Metrics, metric)
		added++
	}
	// The primary key columns must be dimensions for the model to validate.
	for _, k := range model.PrimaryKey {
		if slices.ContainsFunc(model.Dimensions, func(x semantic.Dimension) bool { return x.Name == k }) {
			continue
		}
		i := slices.IndexFunc(columns, func(c semantic.ColumnType) bool { return c.Name == k })
		if i < 0 {
			continue
		}
		model.Dimensions = append([]semantic.Dimension{{Name: k, Type: columns[i].Type}}, model.Dimensions...)
		added++
	}

	switch {
	case existing != nil && added == 0:
		item.Action = MigrateV1ItemActionNOCHANGES
		return item, nil
	case existing != nil:
		item.Action = MigrateV1ItemActionUPDATE
	default:
		item.Action = MigrateV1ItemActionCREATE
	}
	others := make(map[string]semantic.Model, len(visible))
	for slug, m := range visible {
		if slug != model.Slug {
			others[slug] = m
		}
	}
	if err := model.Validate(others); err != nil {
		return fail(MigrateV1ItemErrorCodeInvalidDefinition, err.Error())
	}
	if dryRun {
		return item, nil
	}

	if existing != nil {
		if ok, err := guards.canEdit(existing.SpaceID); err != nil {
			return MigrateV1Item{}, err
		} else if !ok {
			return fail(MigrateV1ItemErrorCodeSpaceForbidden, "you may not edit the space of model "+existing.Slug)
		}
	}
	raw, err := json.Marshal(model)
	if err != nil {
		return MigrateV1Item{}, err
	}
	if existing != nil {
		version := h.semanticModelVersion(ctx, ws, existing.ID)
		_, _, err := h.contentService().UpdateSemanticModel(ctx, userID, db.UpdateSemanticModelParams{
			WorkspaceID: ws, SpaceID: existing.SpaceID, ID: existing.ID, Slug: existing.Slug,
			Label: existing.Label, Description: existing.Description, Certified: existing.Certified,
			CatalogID: existing.CatalogID, BindingID: existing.BindingID, Definition: raw,
			FormatVersion: 1, OwnerUserID: existing.OwnerUserID, UpdatedBy: userID,
		}, &version)
		if err != nil {
			return fail(MigrateV1ItemErrorCodeWriteFailed, "could not update model "+existing.Slug+": "+err.Error())
		}
		visible[model.Slug] = model
		return item, nil
	}
	created, err := h.contentService().CreateSemanticModel(ctx, userID, db.CreateSemanticModelParams{
		WorkspaceID: ws, SpaceID: spaceID, Slug: model.Slug, Label: model.Label,
		CatalogID: t.catalogID, BindingID: t.metrics[0].BindingID, Definition: raw,
		FormatVersion: 1, OwnerUserID: userID, UpdatedBy: userID,
	})
	if err != nil {
		if isUniqueViolation(err) {
			return fail(MigrateV1ItemErrorCodeSlugConflict, "a model with slug "+model.Slug+" already exists")
		}
		return MigrateV1Item{}, err
	}
	id := openapi_types.UUID(created.ID.Bytes)
	item.ModelId = &id
	visible[model.Slug] = model
	return item, nil
}

// migrationColumns reads the table's columns with their portable types from
// the metadata snapshot. Columns the semantic layer cannot type are skipped; a
// metric over one of them fails its item with a clear message.
func (h *core) migrationColumns(ctx context.Context, t migrationTable) ([]semantic.ColumnType, error) {
	tableID, err := h.queries.GetMetadataTableByName(ctx, db.GetMetadataTableByNameParams{
		CatalogID: t.catalogID, Namespace: t.namespace, Name: t.table,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	rows, err := h.queries.ListMetadataColumns(ctx, tableID)
	if err != nil {
		return nil, err
	}
	out := make([]semantic.ColumnType, 0, len(rows))
	for _, c := range rows {
		var lt dialect.LogicalType
		switch logical := typemap.Logical(c.TypeSystem, c.NativeType); logical {
		case "decimal":
			lt = dialect.TypeFloat64
		case "boolean", "int64", "float64", "string", "date", "timestamp", "timestamptz":
			lt = dialect.LogicalType(logical)
		default:
			continue
		}
		out = append(out, semantic.ColumnType{Name: c.Name, Type: lt})
	}
	return out, nil
}

// migrationSlug derives a free model slug from the table name.
func (h *core) migrationSlug(ctx context.Context, ws pgtype.UUID, table string, reserved map[string]bool) (string, error) {
	slug, err := slugs.Unique(ctx, table, func(ctx context.Context, s string) (bool, error) {
		if reserved[s] {
			return true, nil
		}
		_, err := h.queries.GetSemanticModelBySlug(ctx, db.GetSemanticModelBySlugParams{WorkspaceID: ws, Slug: s})
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		return err == nil, err
	})
	if err != nil {
		return "", err
	}
	reserved[slug] = true
	return slug, nil
}
