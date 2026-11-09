// Package publish writes the generated Trino resource-group document through
// the narrow Writer surface the store implements (ADR 0020).
package publish

import (
	"context"
	"encoding/json"
	"fmt"
	"math"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/workload"
)

// Writer is the resource-group write surface Publish needs; db.Querier and the
// GORM store both implement it (ADR 0020).
type Writer interface {
	DeleteTrinoResourceGroups(ctx context.Context, environment *string) error
	InsertTrinoResourceGroup(ctx context.Context, arg db.InsertTrinoResourceGroupParams) (int64, error)
	InsertTrinoSelector(ctx context.Context, arg db.InsertTrinoSelectorParams) error
	NextTrinoResourceGroupRevision(ctx context.Context, engineID db.UUID) (int32, error)
	InsertTrinoResourceGroupRevision(ctx context.Context, arg db.InsertTrinoResourceGroupRevisionParams) error
}

// Publish validates and publishes the document on q, which must already be
// inside a transaction, and returns the new revision. Callers that change the
// settings the document is generated from use it so the settings and the
// published groups commit together (#635).
func Publish(ctx context.Context, q Writer, engineID db.UUID, environment string, doc workload.ResourceGroups) (int, error) {
	if err := workload.Validate(doc); err != nil {
		return 0, err
	}
	return publish(ctx, q, engineID, environment, doc)
}

// publish replaces the environment's groups and selectors and records the
// revision, on the querier already inside a transaction.
func publish(ctx context.Context, q Writer, engineID db.UUID, environment string, doc workload.ResourceGroups) (int, error) {
	if err := q.DeleteTrinoResourceGroups(ctx, &environment); err != nil {
		return 0, err
	}
	ids, err := writeGroups(ctx, q, environment, doc)
	if err != nil {
		return 0, err
	}
	if err := writeSelectors(ctx, q, ids, doc); err != nil {
		return 0, err
	}
	config, err := json.Marshal(doc)
	if err != nil {
		return 0, err
	}
	next, err := q.NextTrinoResourceGroupRevision(ctx, engineID)
	if err != nil {
		return 0, err
	}
	if err := q.InsertTrinoResourceGroupRevision(ctx, db.InsertTrinoResourceGroupRevisionParams{
		EngineID: engineID, Revision: next, Config: config,
	}); err != nil {
		return 0, err
	}
	return int(next), nil
}

// writeGroups inserts the root group and its subgroups, returning each group's
// database id keyed by its fully-qualified name. The root's required limits are
// derived when the ceiling is unset: the total concurrency is the sum of the
// subgroup limits, and the queued limit is the sum of the subgroup queues. A
// sum past the int32 column is clamped: at that size the root adds no limit of
// its own, which is what the sum means.
func writeGroups(ctx context.Context, q Writer, environment string, doc workload.ResourceGroups) (map[string]int64, error) {
	root := doc.RootGroups[0]

	var ceiling, maxQueued int64
	if root.HardConcurrencyLimit != nil {
		ceiling = int64(*root.HardConcurrencyLimit)
	}
	for _, sub := range root.SubGroups {
		maxQueued += int64(sub.MaxQueued)
		if root.HardConcurrencyLimit == nil {
			ceiling += int64(sub.HardConcurrencyLimit)
		}
	}

	env := environment
	rootID, err := q.InsertTrinoResourceGroup(ctx, db.InsertTrinoResourceGroupParams{
		Name:                 root.Name,
		SoftMemoryLimit:      root.SoftMemoryLimit,
		MaxQueued:            clampInt32(maxQueued),
		HardConcurrencyLimit: clampInt32(ceiling),
		Environment:          &env,
	})
	if err != nil {
		return nil, err
	}

	ids := map[string]int64{root.Name: rootID}
	for _, sub := range root.SubGroups {
		parent := rootID
		id, err := q.InsertTrinoResourceGroup(ctx, db.InsertTrinoResourceGroupParams{
			Name:                 sub.Name,
			SoftMemoryLimit:      root.SoftMemoryLimit,            // subgroups inherit the root's soft limit
			MaxQueued:            int32(sub.MaxQueued),            //nolint:gosec // a queue limit never overflows int32
			HardConcurrencyLimit: int32(sub.HardConcurrencyLimit), //nolint:gosec // a concurrency limit never overflows int32
			Parent:               &parent,
			Environment:          &env,
		})
		if err != nil {
			return nil, err
		}
		ids[root.Name+"."+sub.Name] = id
	}
	return ids, nil
}

// writeSelectors inserts the selectors, in order, giving the first the highest
// priority (Trino processes them in descending priority).
func writeSelectors(ctx context.Context, q Writer, ids map[string]int64, doc workload.ResourceGroups) error {
	for i, sel := range doc.Selectors {
		groupID, ok := ids[sel.Group]
		if !ok {
			return fmt.Errorf("resource groups: selector %d points at unknown group %q", i, sel.Group)
		}
		var tags *string
		if len(sel.ClientTags) > 0 {
			raw, err := json.Marshal(sel.ClientTags)
			if err != nil {
				return err
			}
			t := string(raw)
			tags = &t
		}
		if err := q.InsertTrinoSelector(ctx, db.InsertTrinoSelectorParams{
			ResourceGroupID: groupID,
			Priority:        int64(len(doc.Selectors) - i),
			ClientTags:      tags,
		}); err != nil {
			return err
		}
	}
	return nil
}

// clampInt32 bounds n to the int32 range the resource_groups columns hold.
func clampInt32(n int64) int32 {
	return int32(min(max(n, math.MinInt32), math.MaxInt32)) //nolint:gosec // bounded on both sides
}
