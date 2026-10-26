// Package lens reads the snapshots behind the legacy /lens routes, which the
// engine-aware monitoring API (#117) replaces: query snapshots, the cluster's
// latest state and history, table health, and a connection's catalogs for
// maintenance. The caller checks who may see what.
package lens

import (
	"context"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/jackc/pgx/v5/pgtype"
)

// Store reads the snapshots; *db.Queries implements it.
type Store interface {
	ListTrinoQuerySnapshots(ctx context.Context, arg db.ListTrinoQuerySnapshotsParams) ([]db.TrinoQuerySnapshot, error)
	ListTrinoQuerySnapshotsByUser(ctx context.Context, arg db.ListTrinoQuerySnapshotsByUserParams) ([]db.TrinoQuerySnapshot, error)
	GetTrinoQuerySnapshot(ctx context.Context, trinoQueryID string) (db.TrinoQuerySnapshot, error)
	GetLatestClusterSnapshot(ctx context.Context) (db.ClusterSnapshot, error)
	ListClusterSnapshots(ctx context.Context, arg db.ListClusterSnapshotsParams) ([]db.ClusterSnapshot, error)
	ListTableHealthSnapshots(ctx context.Context, arg db.ListTableHealthSnapshotsParams) ([]db.TableHealthSnapshot, error)
	ListTableHealthByConnection(ctx context.Context, arg db.ListTableHealthByConnectionParams) ([]db.TableHealthSnapshot, error)
	ListCatalogsByConnection(ctx context.Context, connectionID pgtype.UUID) ([]db.Catalog, error)
}

// Service reads lens snapshots.
type Service struct{ store Store }

// New builds the service.
func New(store Store) *Service { return &Service{store: store} }

// Queries lists query snapshots; an empty user lists everyone's.
func (s *Service) Queries(ctx context.Context, user string, limit, offset int32) ([]db.TrinoQuerySnapshot, error) {
	if user == "" {
		return s.store.ListTrinoQuerySnapshots(ctx, db.ListTrinoQuerySnapshotsParams{Limit: limit, Offset: offset})
	}
	return s.store.ListTrinoQuerySnapshotsByUser(ctx, db.ListTrinoQuerySnapshotsByUserParams{User: user, Limit: limit, Offset: offset})
}

// Query returns one query snapshot.
func (s *Service) Query(ctx context.Context, id string) (db.TrinoQuerySnapshot, error) {
	return s.store.GetTrinoQuerySnapshot(ctx, id)
}

// LatestCluster returns the latest cluster snapshot.
func (s *Service) LatestCluster(ctx context.Context) (db.ClusterSnapshot, error) {
	return s.store.GetLatestClusterSnapshot(ctx)
}

// ClusterHistory lists the latest cluster snapshots.
func (s *Service) ClusterHistory(ctx context.Context, limit int32) ([]db.ClusterSnapshot, error) {
	return s.store.ListClusterSnapshots(ctx, db.ListClusterSnapshotsParams{Limit: limit})
}

// TableHealth lists table health snapshots, of one connection when connID
// is valid.
func (s *Service) TableHealth(ctx context.Context, connID pgtype.UUID, limit int32) ([]db.TableHealthSnapshot, error) {
	if connID.Valid {
		return s.store.ListTableHealthByConnection(ctx, db.ListTableHealthByConnectionParams{CatalogConnectionID: connID, Limit: limit})
	}
	return s.store.ListTableHealthSnapshots(ctx, db.ListTableHealthSnapshotsParams{Limit: limit})
}

// TrinoCatalog finds the Trino name of a connection's catalog named name,
// matching its Trino name first, then its Baleia name. It is empty when none
// matches.
func (s *Service) TrinoCatalog(ctx context.Context, connID pgtype.UUID, name string) (string, error) {
	cats, err := s.store.ListCatalogsByConnection(ctx, connID)
	if err != nil {
		return "", err
	}
	for _, c := range cats {
		if c.TrinoName() != "" && c.TrinoName() == name {
			return c.TrinoName(), nil
		}
	}
	for _, c := range cats {
		if c.Name == name {
			return c.TrinoName(), nil
		}
	}
	return "", nil
}
