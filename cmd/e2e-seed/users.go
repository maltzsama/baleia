//go:build e2e

package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"

	"github.com/galileostudio/baleia/internal/db"
)

type seedUser struct {
	name  string
	email string
	role  string
}

func ensureUser(ctx context.Context, q db.Querier, u seedUser, hash string) (db.User, error) {
	existing, err := q.GetUserByEmail(ctx, u.email)
	if err == nil {
		return existing, nil
	}
	if !errors.Is(err, db.ErrNoRows) {
		return db.User{}, err
	}
	return q.CreateUser(ctx, db.CreateUserParams{
		Name:         u.name,
		Email:        u.email,
		PasswordHash: &hash,
		Role:         u.role,
		AuthProvider: "local",
	})
}

func ensurePersonalWorkspace(ctx context.Context, q db.Querier, user db.User) error {
	workspaces, err := q.ListWorkspacesByUser(ctx, user.ID)
	if err == nil {
		for _, w := range workspaces {
			if w.Type == "personal" && w.OwnerUserID.Valid && w.OwnerUserID.Bytes == user.ID.Bytes {
				return nil
			}
		}
	}

	ws, err := q.CreateWorkspace(ctx, db.CreateWorkspaceParams{
		Name:               user.Name + " - workspace",
		Type:               "personal",
		PolarisCatalogName: "e2e_personal_" + stableSuffix(user.ID),
		OwnerUserID:        user.ID,
	})
	if err != nil {
		return err
	}
	return q.UpdateUserPersonalWorkspace(ctx, db.UpdateUserPersonalWorkspaceParams{
		ID:                  user.ID,
		PersonalWorkspaceID: ws.ID,
	})
}

// stableSuffix keeps the legacy polaris_catalog_name unique and stable across
// repeated seed runs.
func stableSuffix(id db.UUID) string {
	if len(id.Bytes) == 0 {
		return randomSuffix()
	}
	return hex.EncodeToString(id.Bytes[:8])
}

func randomSuffix() string {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return "seed"
	}
	return hex.EncodeToString(buf)
}
