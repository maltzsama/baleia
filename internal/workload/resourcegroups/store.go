// Package resourcegroups publishes the generated Trino resource-group document
// (#598) into the tables Trino's database resource group manager reads (#599).
// The write is idempotent (it replaces the environment's groups and selectors
// wholesale) and every write is a new revision, so a rollback re-applies an
// earlier one. Trino reloads the tables every second, so publishing is the
// reload: no coordinator restart is involved.
package resourcegroups

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/workload"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrRevisionNotFound means the revision to roll back to does not exist.
var ErrRevisionNotFound = errors.New("resource group revision not found")

// Store writes resource groups through Baleia's pool.
type Store struct {
	pool *pgxpool.Pool
}

// New builds a store writing through pool.
func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

func (s *Store) inTx(ctx context.Context, fn func(q db.Querier) error) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := fn(db.New(tx)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Write validates and publishes the document for one cluster's environment,
// recording a new revision. It returns the revision number.
func (s *Store) Write(ctx context.Context, engineID pgtype.UUID, environment string, doc workload.ResourceGroups) (int, error) {
	if err := workload.Validate(doc); err != nil {
		return 0, err
	}
	var revision int
	err := s.inTx(ctx, func(q db.Querier) error {
		r, err := publish(ctx, q, engineID, environment, doc)
		revision = r
		return err
	})
	return revision, err
}

// Rollback re-publishes the configuration of an earlier revision as a new
// write, restoring the tables to that state.
func (s *Store) Rollback(ctx context.Context, engineID pgtype.UUID, environment string, revision int) error {
	return s.inTx(ctx, func(q db.Querier) error {
		config, err := q.GetTrinoResourceGroupRevision(ctx, db.GetTrinoResourceGroupRevisionParams{
			EngineID: engineID, Revision: int32(revision), //nolint:gosec // a revision number never overflows int32
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrRevisionNotFound
		}
		if err != nil {
			return err
		}
		var doc workload.ResourceGroups
		if err := json.Unmarshal(config, &doc); err != nil {
			return fmt.Errorf("resource groups: corrupt revision %d: %w", revision, err)
		}
		_, err = publish(ctx, q, engineID, environment, doc)
		return err
	})
}

// publish replaces the environment's groups and selectors and records the
// revision, on the querier already inside a transaction.
func publish(ctx context.Context, q db.Querier, engineID pgtype.UUID, environment string, doc workload.ResourceGroups) (int, error) {
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
// subgroup limits, and the queued limit is the sum of the subgroup queues.
func writeGroups(ctx context.Context, q db.Querier, environment string, doc workload.ResourceGroups) (map[string]int64, error) {
	root := doc.RootGroups[0]

	ceiling := 0
	if root.HardConcurrencyLimit != nil {
		ceiling = *root.HardConcurrencyLimit
	}
	maxQueued := 0
	for _, sub := range root.SubGroups {
		maxQueued += sub.MaxQueued
		if root.HardConcurrencyLimit == nil {
			ceiling += sub.HardConcurrencyLimit
		}
	}

	env := environment
	rootID, err := q.InsertTrinoResourceGroup(ctx, db.InsertTrinoResourceGroupParams{
		Name:                 root.Name,
		SoftMemoryLimit:      root.SoftMemoryLimit,
		MaxQueued:            int32(maxQueued),
		HardConcurrencyLimit: int32(ceiling),
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
func writeSelectors(ctx context.Context, q db.Querier, ids map[string]int64, doc workload.ResourceGroups) error {
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
