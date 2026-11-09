//go:build e2e

package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/galileostudio/baleia/internal/content"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/store/repo"
	"gorm.io/gorm"
)

// seedModels are the semantic models (#121) the Explore smoke explores: two
// tpch tables joined many_to_one, written through the content service so they
// get a version like any model created through the API.
var seedModels = []struct {
	slug, label, definition string
}{
	{
		slug: "e2e_customers", label: "E2E Customers",
		definition: `{"source":{"kind":"table","namespace":["tiny"],"table":"customer"},
			"primary_key":["custkey"],
			"dimensions":[{"name":"custkey","type":"int64"},{"name":"nation","sql":"nationkey","type":"int64"}],
			"metrics":[{"name":"customer_count","label":"Customers","aggregation":"count"}]}`,
	},
	{
		slug: "e2e_orders", label: "E2E Orders",
		definition: `{"source":{"kind":"table","namespace":["tiny"],"table":"orders"},
			"primary_key":["orderkey"],
			"dimensions":[{"name":"orderkey","type":"int64"},{"name":"custkey_fk","sql":"custkey","type":"int64"},
				{"name":"order_status","label":"Order status","sql":"orderstatus","type":"string"},
				{"name":"order_date","label":"Order date","sql":"orderdate","type":"date","grains":["month","year"]}],
			"metrics":[{"name":"order_count","label":"Orders","aggregation":"count"},
				{"name":"total_price","label":"Total price","aggregation":"sum","dimension":"orderkey"}],
			"joins":[{"to":"e2e_customers","relationship":"many_to_one","on":[{"from":"custkey_fk","to":"custkey"}]}]}`,
	},
}

// newContentService builds the content writer over the GORM store (ADR 0020).
// The seed writes saved content through the same path as the API.
func newContentService(gdb *gorm.DB) (*content.Service, error) {
	r := repo.New(gdb)
	return content.New(r, r.TxContent), nil
}

// ensureModels creates the e2e models in the member's default space; an
// existing slug is left untouched so the seed stays idempotent.
func ensureModels(ctx context.Context, q db.Querier, svc *content.Service, member db.User, catalog db.Catalog, binding db.CatalogBinding) error {
	workspaceID, err := q.PersonalWorkspaceID(ctx, member.ID)
	if err != nil {
		return err
	}
	space, err := q.GetDefaultSpace(ctx, workspaceID)
	if err != nil {
		return err
	}
	for _, m := range seedModels {
		_, err := q.GetSemanticModelBySlug(ctx, db.GetSemanticModelBySlugParams{WorkspaceID: workspaceID, Slug: m.slug})
		if err == nil {
			continue
		}
		if !errors.Is(err, db.ErrNoRows) {
			return err
		}
		if _, err := svc.CreateSemanticModel(ctx, member.ID, db.CreateSemanticModelParams{
			WorkspaceID: workspaceID, SpaceID: space.ID, Slug: m.slug, Label: m.label,
			CatalogID: catalog.ID, BindingID: binding.ID,
			Definition: []byte(m.definition), FormatVersion: 1,
			OwnerUserID: member.ID, UpdatedBy: member.ID,
		}); err != nil {
			return err
		}
	}
	return nil
}

// ensureV1Metric seeds one v1 metric on a table no model uses, so the
// migration dialog (#121) has something to migrate.
func ensureV1Metric(ctx context.Context, q db.Querier, svc *content.Service, member db.User, catalog db.Catalog, binding db.CatalogBinding) error {
	workspaceID, err := q.PersonalWorkspaceID(ctx, member.ID)
	if err != nil {
		return err
	}
	if _, err := q.GetSemanticMetricBySlug(ctx, db.GetSemanticMetricBySlugParams{WorkspaceID: workspaceID, Slug: "e2e_nation_count"}); err == nil {
		return nil
	} else if !errors.Is(err, db.ErrNoRows) {
		return err
	}
	space, err := q.GetDefaultSpace(ctx, workspaceID)
	if err != nil {
		return err
	}
	_, err = svc.CreateSemanticMetric(ctx, member.ID, db.CreateSemanticMetricParams{
		WorkspaceID: workspaceID, SpaceID: space.ID, Slug: "e2e_nation_count", Label: "E2E Nations",
		CatalogID: catalog.ID, Namespace: []string{"tiny"}, TableName: "nation", BindingID: binding.ID,
		Definition: []byte(`{"type":"simple","aggregation":"count"}`), FormatVersion: 1,
		OwnerUserID: member.ID, UpdatedBy: member.ID,
	})
	return err
}

// ensureMetadata seeds the tpch tables the e2e models and metrics use, so the
// editor's table allowlist (#614) resolves them without waiting for a sync.
func ensureMetadata(ctx context.Context, q db.Querier, catalog db.Catalog) error {
	for _, t := range []struct{ namespace, name string }{
		{"tiny", "customer"},
		{"tiny", "orders"},
		{"tiny", "nation"},
	} {
		if _, err := q.UpsertMetadataTable(ctx, db.UpsertMetadataTableParams{
			CatalogID: catalog.ID, Namespace: []string{t.namespace}, Name: t.name,
			Kind: "table", Fingerprint: "e2e", Properties: []byte(`{}`),
		}); err != nil {
			return fmt.Errorf("%s.%s: %w", t.namespace, t.name, err)
		}
	}
	return nil
}
