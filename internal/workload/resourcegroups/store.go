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
	"github.com/galileostudio/baleia/internal/store/repo"
	"github.com/galileostudio/baleia/internal/workload"
	"github.com/galileostudio/baleia/internal/workload/resourcegroups/publish"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"gorm.io/gorm"
)

// ErrRevisionNotFound means the revision to roll back to does not exist.
var ErrRevisionNotFound = errors.New("resource group revision not found")

// Store writes resource groups through the GORM store.
type Store struct {
	db *gorm.DB
}

// New builds a store writing through db.
func New(db *gorm.DB) *Store { return &Store{db: db} }

func (s *Store) inTx(ctx context.Context, fn func(q db.Querier) error) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return fn(repo.New(tx))
	})
}

// Write validates and publishes the document for one cluster's environment,
// recording a new revision. It returns the revision number.
func (s *Store) Write(ctx context.Context, engineID pgtype.UUID, environment string, doc workload.ResourceGroups) (int, error) {
	var revision int
	err := s.inTx(ctx, func(q db.Querier) error {
		r, err := publish.Publish(ctx, q, engineID, environment, doc)
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
		_, err = publish.Publish(ctx, q, engineID, environment, doc)
		return err
	})
}
