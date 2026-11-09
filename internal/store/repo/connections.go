package repo

import (
	"context"
	"time"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/store/model"
)

// ── catalog connections ───────────────────────────────────────────────────────

// CreateCatalogConnection inserts a connection. The caller supplies the id,
// because the credentials are sealed with it as additional authenticated data.
func (r *Repo) CreateCatalogConnection(ctx context.Context, arg db.CreateCatalogConnectionParams) (db.CatalogConnection, error) {
	now := time.Now().UTC()
	row := model.CatalogConnection{
		ID:                  model.UUID(UUIDString(arg.ID)),
		Name:                model.Text(arg.Name),
		Type:                arg.Type,
		AccessMode:          arg.AccessMode,
		URL:                 arg.Url,
		Config:              jsonOrDefault(arg.Config),
		CredentialsEnc:      arg.CredentialsEnc,
		CredentialsReadOnly: arg.CredentialsReadOnly,
		CreatedBy:           modelUUIDPtr(arg.CreatedBy),
		CreatedAt:           now,
		UpdatedAt:           now,
	}
	if err := r.Users.db.WithContext(ctx).Create(&row).Error; err != nil {
		return db.CatalogConnection{}, err
	}
	return toDBCatalogConnection(row), nil
}

// ListCatalogConnections lists the connections one user created.
func (r *Repo) ListCatalogConnections(ctx context.Context, createdBy db.UUID) ([]db.CatalogConnection, error) {
	var rows []model.CatalogConnection
	if err := r.Users.db.WithContext(ctx).
		Where("created_by = ?", uuidText(createdBy)).
		Order("name").
		Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]db.CatalogConnection, len(rows))
	for i, row := range rows {
		out[i] = toDBCatalogConnection(row)
	}
	return out, nil
}

// ListVisibleCatalogConnections lists the caller's own connections, and every
// one for an admin.
func (r *Repo) ListVisibleCatalogConnections(ctx context.Context, arg db.ListVisibleCatalogConnectionsParams) ([]db.CatalogConnection, error) {
	var rows []model.CatalogConnection
	if err := r.Users.db.WithContext(ctx).
		Where("? OR created_by = ?", arg.IsAdmin, uuidText(arg.UserID)).
		Order("name").
		Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]db.CatalogConnection, len(rows))
	for i, row := range rows {
		out[i] = toDBCatalogConnection(row)
	}
	return out, nil
}

// UpdateCatalogConnection applies a partial update; nil fields keep their
// value, so concurrent patches to different fields do not overwrite each other.
func (r *Repo) UpdateCatalogConnection(ctx context.Context, arg db.UpdateCatalogConnectionParams) (db.CatalogConnection, error) {
	if !arg.ID.Valid {
		return db.CatalogConnection{}, db.ErrNoRows
	}
	changes := map[string]any{"updated_at": time.Now().UTC()}
	if arg.Name != nil {
		changes["name"] = *arg.Name
	}
	if arg.Url != nil {
		changes["url"] = *arg.Url
	}
	if arg.Config != nil {
		changes["config"] = jsonOrDefault(arg.Config)
	}
	if arg.CredentialsEnc != nil {
		changes["credentials_enc"] = *arg.CredentialsEnc
	}
	if arg.CredentialsReadOnly != nil {
		changes["credentials_read_only"] = *arg.CredentialsReadOnly
	}
	res := r.Users.db.WithContext(ctx).Model(&model.CatalogConnection{}).
		Where("id = ?", UUIDString(arg.ID)).
		Updates(changes)
	if res.Error != nil {
		return db.CatalogConnection{}, res.Error
	}
	if res.RowsAffected == 0 {
		return db.CatalogConnection{}, db.ErrNoRows
	}
	return r.GetCatalogConnectionByID(ctx, arg.ID)
}

// DeleteCatalogConnection removes a connection; its catalogs cascade.
func (r *Repo) DeleteCatalogConnection(ctx context.Context, id db.UUID) error {
	return r.Users.db.WithContext(ctx).
		Where("id = ?", UUIDString(id)).
		Delete(&model.CatalogConnection{}).Error
}

// CatalogConnectionNameExists reports whether another connection has the name.
func (r *Repo) CatalogConnectionNameExists(ctx context.Context, name string) (bool, error) {
	var count int64
	err := r.Users.db.WithContext(ctx).Model(&model.CatalogConnection{}).
		Where("name = ?", name).
		Count(&count).Error
	return count > 0, err
}

// CountEncryptedConnections counts the connections with stored credentials.
func (r *Repo) CountEncryptedConnections(ctx context.Context) (int64, error) {
	var count int64
	err := r.Users.db.WithContext(ctx).Model(&model.CatalogConnection{}).
		Where("credentials_enc IS NOT NULL").
		Count(&count).Error
	return count, err
}

func toDBCatalogConnection(c model.CatalogConnection) db.CatalogConnection {
	return db.CatalogConnection{
		ID:                  PGUUID(string(c.ID)),
		Name:                string(c.Name),
		Type:                c.Type,
		AccessMode:          c.AccessMode,
		Url:                 c.URL,
		CredentialsEnc:      c.CredentialsEnc,
		CreatedAt:           PGTime(c.CreatedAt),
		UpdatedAt:           PGTime(c.UpdatedAt),
		Host:                c.Host,
		Port:                c.Port,
		CreatedBy:           uuidPtr(c.CreatedBy),
		Config:              []byte(c.Config),
		CredentialsReadOnly: c.CredentialsReadOnly,
	}
}
