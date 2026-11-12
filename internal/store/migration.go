package store

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"time"
	"unicode/utf8"

	"gorm.io/gorm"
)

// BaselineVersion is the smallest accepted migration version.
const BaselineVersion int64 = 0

const (
	// schemaMigrationNameMax matches the ledger's name column size. Names are
	// validated up front so an over-long one cannot fail the ledger insert
	// after Up has already changed the schema.
	schemaMigrationNameMax = 255

	migrationLockName = "store_migrations"
	// migrationLockTTL breaks a lock left behind by a crashed runner. A
	// migration running longer than this can be taken over by another runner.
	migrationLockTTL = 30 * time.Minute
)

// Migration is one versioned schema transition. Up is mandatory; Down is set
// only when the transition is safely reversible.
type Migration struct {
	Version int64
	Name    string
	Up      func(*gorm.DB) error
	Down    func(*gorm.DB) error
}

// Registry is an ordered, validated set of migrations.
type Registry struct {
	migrations []Migration
}

// NewRegistry validates and orders migrations by version. It rejects
// duplicate or non-positive versions, and migrations without Up or with a name
// longer than the ledger column.
func NewRegistry(ms ...Migration) (*Registry, error) {
	ordered := append([]Migration(nil), ms...)
	sort.SliceStable(ordered, func(i, j int) bool {
		return ordered[i].Version < ordered[j].Version
	})
	for i, m := range ordered {
		if m.Version <= BaselineVersion {
			return nil, fmt.Errorf("migration %q: version must be greater than %d", m.Name, BaselineVersion)
		}
		if m.Name == "" || m.Up == nil {
			return nil, fmt.Errorf("migration %d: name and Up are required", m.Version)
		}
		if utf8.RuneCountInString(m.Name) > schemaMigrationNameMax {
			return nil, fmt.Errorf("migration %d: name is longer than %d characters", m.Version, schemaMigrationNameMax)
		}
		if i > 0 && ordered[i-1].Version == m.Version {
			return nil, fmt.Errorf("duplicate migration version %d", m.Version)
		}
	}
	return &Registry{migrations: ordered}, nil
}

// All returns a copy of the registered migrations in version order.
func (r *Registry) All() []Migration {
	return append([]Migration(nil), r.migrations...)
}

// SchemaMigration is GORM's own migration ledger. A dirty row means a
// migration or rollback was interrupted and must be reconciled before retrying.
type SchemaMigration struct {
	Version   int64     `gorm:"primaryKey;autoIncrement:false"`
	Name      string    `gorm:"size:255;not null"`
	AppliedAt time.Time `gorm:"not null"`
	Dirty     bool      `gorm:"not null;default:false"`
}

// TableName keeps the ledger out of GORM's pluralized naming.
func (SchemaMigration) TableName() string { return "schema_migrations" }

// migrationLock serializes runners across processes. Its row exists only
// while a runner holds the lock.
type migrationLock struct {
	Name       string    `gorm:"primaryKey;size:64"`
	AcquiredAt time.Time `gorm:"not null"`
}

func (migrationLock) TableName() string { return "store_migration_lock" }

// Status describes one applied or pending version.
type Status struct {
	Version   int64
	Name      string
	Applied   bool
	Dirty     bool
	AppliedAt time.Time
}

// Runner applies the registry in order and tracks it in schema_migrations.
//
// Runs are serialized by a row in store_migration_lock. A migration is marked
// dirty before its callback runs and cleared after; a dirty row blocks both Up
// and Down until an operator reconciles the schema. After a failed Up, delete
// the row (clearing the flag would mark the migration as applied and skip it).
// After a failed Down the row is still the applied migration: restore the applied
// schema and clear the flag to retry the rollback, or, if the rollback was
// finished by hand, delete the row and do not retry it.
type Runner struct {
	db       *gorm.DB
	registry *Registry
	logger   *slog.Logger
}

// NewRunner builds a runner over db. A nil logger discards progress lines.
func NewRunner(db *gorm.DB, registry *Registry, logger *slog.Logger) *Runner {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	return &Runner{db: db, registry: registry, logger: logger}
}

// Up applies every pending migration in order and records each one.
func (r *Runner) Up(ctx context.Context) ([]Migration, error) {
	if err := r.ensureTracking(ctx); err != nil {
		return nil, err
	}
	release, err := r.acquireLock(ctx)
	if err != nil {
		return nil, err
	}
	defer release()

	if err := r.refuseInterrupted(ctx); err != nil {
		return nil, err
	}

	applied, err := r.appliedVersions(ctx)
	if err != nil {
		return nil, err
	}

	var done []Migration
	for _, m := range r.registry.All() {
		if applied[m.Version] {
			continue
		}
		r.logger.InfoContext(ctx, "applying store migration", "version", m.Version, "name", m.Name)
		if err := r.db.WithContext(ctx).Create(&SchemaMigration{
			Version: m.Version, Name: m.Name, AppliedAt: time.Now().UTC(), Dirty: true,
		}).Error; err != nil {
			return done, fmt.Errorf("mark migration %d (%s) in progress: %w", m.Version, m.Name, err)
		}
		if err := m.Up(r.db.WithContext(ctx)); err != nil {
			return done, fmt.Errorf("migration %d (%s): %w (marked dirty; reconcile before retrying)", m.Version, m.Name, err)
		}
		if err := r.markApplied(ctx, m.Version); err != nil {
			return done, fmt.Errorf("record migration %d (%s): %w (marked dirty; reconcile before retrying)", m.Version, m.Name, err)
		}
		done = append(done, m)
	}
	return done, nil
}

// Down rolls back the last applied migration that has a Down implementation.
// It returns the rolled-back migration, or nil when nothing is reversible.
func (r *Runner) Down(ctx context.Context) (*Migration, error) {
	if err := r.ensureTracking(ctx); err != nil {
		return nil, err
	}
	release, err := r.acquireLock(ctx)
	if err != nil {
		return nil, err
	}
	defer release()

	if err := r.refuseInterrupted(ctx); err != nil {
		return nil, err
	}
	applied, err := r.appliedVersions(ctx)
	if err != nil {
		return nil, err
	}

	all := r.registry.All()
	for i := len(all) - 1; i >= 0; i-- {
		m := all[i]
		if !applied[m.Version] {
			continue
		}
		if m.Down == nil {
			return nil, fmt.Errorf("migration %d (%s) has no safe rollback", m.Version, m.Name)
		}
		r.logger.InfoContext(ctx, "rolling back store migration", "version", m.Version, "name", m.Name)
		if err := r.db.WithContext(ctx).Model(&SchemaMigration{}).Where("version = ?", m.Version).Update("dirty", true).Error; err != nil {
			return nil, fmt.Errorf("mark rollback %d (%s) in progress: %w", m.Version, m.Name, err)
		}
		if err := m.Down(r.db.WithContext(ctx)); err != nil {
			return nil, fmt.Errorf("rollback %d (%s): %w (marked dirty; reconcile before retrying)", m.Version, m.Name, err)
		}
		if err := r.db.WithContext(ctx).Where("version = ?", m.Version).Delete(&SchemaMigration{}).Error; err != nil {
			return nil, fmt.Errorf("record rollback %d (%s): %w (marked dirty; reconcile before retrying)", m.Version, m.Name, err)
		}
		return &m, nil
	}
	return nil, nil
}

// Status reports the registry and the ledger. It never writes: when the ledger
// is absent every registered migration is pending.
func (r *Runner) Status(ctx context.Context) ([]Status, error) {
	if !r.db.WithContext(ctx).Migrator().HasTable(&SchemaMigration{}) {
		return r.pendingStatus(), nil
	}
	var rows []SchemaMigration
	if err := r.db.WithContext(ctx).Order("version").Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("read schema_migrations: %w", err)
	}
	byVersion := make(map[int64]SchemaMigration, len(rows))
	for _, row := range rows {
		byVersion[row.Version] = row
	}

	out := make([]Status, 0, len(rows)+len(r.registry.All()))
	for _, m := range r.registry.All() {
		st := Status{Version: m.Version, Name: m.Name}
		if row, ok := byVersion[m.Version]; ok {
			st.Applied = true
			st.Dirty = row.Dirty
			st.AppliedAt = row.AppliedAt
		}
		out = append(out, st)
	}
	return out, nil
}

func (r *Runner) pendingStatus() []Status {
	all := r.registry.All()
	out := make([]Status, 0, len(all))
	for _, m := range all {
		out = append(out, Status{Version: m.Version, Name: m.Name})
	}
	return out
}

func (r *Runner) ensureTracking(ctx context.Context) error {
	if err := r.db.WithContext(ctx).AutoMigrate(&SchemaMigration{}, &migrationLock{}); err != nil {
		return fmt.Errorf("create store migration tables: %w", err)
	}
	return nil
}

func (r *Runner) acquireLock(ctx context.Context) (func(), error) {
	now := time.Now().UTC()
	// Break a lock left behind by a crashed runner.
	if err := r.db.WithContext(ctx).
		Where("name = ? AND acquired_at < ?", migrationLockName, now.Add(-migrationLockTTL)).
		Delete(&migrationLock{}).Error; err != nil {
		return nil, fmt.Errorf("clear stale store migration lock: %w", err)
	}
	row := migrationLock{Name: migrationLockName, AcquiredAt: now}
	if err := r.db.WithContext(ctx).Create(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			return nil, fmt.Errorf("another store migration is running (lock %q)", migrationLockName)
		}
		return nil, fmt.Errorf("acquire store migration lock: %w", err)
	}
	return func() {
		// The caller's context may be cancelled; the lock is still released.
		_ = r.db.WithContext(context.WithoutCancel(ctx)).
			Where("name = ?", migrationLockName).Delete(&migrationLock{}).Error
	}, nil
}

// refuseInterrupted stops a run when a previous Up or Down did not finish.
func (r *Runner) refuseInterrupted(ctx context.Context) error {
	var rows []SchemaMigration
	if err := r.db.WithContext(ctx).Where("dirty = ?", true).Order("version").Find(&rows).Error; err != nil {
		return fmt.Errorf("read interrupted store migrations: %w", err)
	}
	if len(rows) == 0 {
		return nil
	}
	versions := make([]int64, 0, len(rows))
	for _, row := range rows {
		versions = append(versions, row.Version)
	}
	return fmt.Errorf("interrupted store migration(s) %v: reconcile the schema, then fix the dirty ledger rows before retrying: after a failed Up delete the row (setting dirty to false marks the migration as applied and skips it); after a failed Down restore the applied schema and set dirty to false to retry the rollback, or delete the row if you finished the rollback by hand", versions)
}

func (r *Runner) markApplied(ctx context.Context, version int64) error {
	return r.db.WithContext(ctx).Model(&SchemaMigration{}).Where("version = ?", version).Updates(map[string]any{
		"dirty":      false,
		"applied_at": time.Now().UTC(),
	}).Error
}

func (r *Runner) appliedVersions(ctx context.Context) (map[int64]bool, error) {
	var rows []SchemaMigration
	if err := r.db.WithContext(ctx).Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("read schema_migrations: %w", err)
	}
	out := make(map[int64]bool, len(rows))
	for _, row := range rows {
		out[row.Version] = true
	}
	return out, nil
}
