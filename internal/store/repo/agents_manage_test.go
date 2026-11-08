package repo

import (
	"context"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/store/model"
	"github.com/galileostudio/baleia/internal/testdb"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/stdlib"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

func TestAgentManageRoundTrip(t *testing.T) {
	t.Parallel()
	pool := testdb.New(t)
	sqlDB := stdlib.OpenDBFromPool(pool)
	gdb, err := gorm.Open(postgres.New(postgres.Config{Conn: sqlDB}), &gorm.Config{
		Logger:         gormlogger.Discard,
		TranslateError: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })

	ctx := context.Background()
	now := time.Now().UTC()
	store := New(gdb)
	owner := createAPIKeyUser(t, gdb, "agent-owner@example.test")
	ownerID := PGUUID(string(owner.ID))
	ws := model.Workspace{
		ID: model.UUID(NewUUID()), Name: "Agent WS", Type: "personal",
		PolarisCatalogName: model.Text("polaris_agent"), OwnerUserID: &owner.ID,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := gdb.Create(&ws).Error; err != nil {
		t.Fatalf("workspace: %v", err)
	}

	agent, err := store.CreateAgent(ctx, db.CreateAgentParams{OwnerID: ownerID, Kind: "external", Name: "agent", Description: "d"})
	if err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	if _, err := store.ListAllAgents(ctx); err != nil {
		t.Fatalf("ListAllAgents: %v", err)
	}
	if list, err := store.ListAgentsByOwner(ctx, ownerID); err != nil || len(list) != 1 {
		t.Fatalf("ListAgentsByOwner = %v, %v", list, err)
	}
	if err := store.AddAgentWorkspace(ctx, db.AddAgentWorkspaceParams{AgentID: agent.ID, WorkspaceID: PGUUID(string(ws.ID))}); err != nil {
		t.Fatalf("AddAgentWorkspace: %v", err)
	}
	if list, err := store.ListAgentWorkspaces(ctx, agent.ID); err != nil || len(list) != 1 {
		t.Fatalf("ListAgentWorkspaces = %v, %v", list, err)
	}

	if _, err := store.CreateAgentToken(ctx, db.CreateAgentTokenParams{
		AgentID: agent.ID, Name: "t", TokenHash: "hash", Prefix: "bal_",
		ExpiresAt: pgtype.Timestamptz{Time: now.Add(time.Hour), Valid: true}, CreatedBy: ownerID,
	}); err != nil {
		t.Fatalf("CreateAgentToken: %v", err)
	}
	tokens, err := store.ListAgentTokensByAgent(ctx, agent.ID)
	if err != nil || len(tokens) != 1 {
		t.Fatalf("ListAgentTokensByAgent = %v, %v", tokens, err)
	}
	if err := store.RevokeAgentToken(ctx, db.RevokeAgentTokenParams{ID: tokens[0].ID, AgentID: agent.ID}); err != nil {
		t.Fatalf("RevokeAgentToken: %v", err)
	}
	if _, err := store.UpdateAgent(ctx, db.UpdateAgentParams{ID: agent.ID, Name: "agent2", Description: "d2"}); err != nil {
		t.Fatalf("UpdateAgent: %v", err)
	}
	if _, err := store.SetAgentStatus(ctx, db.SetAgentStatusParams{ID: agent.ID, Status: "disabled"}); err != nil {
		t.Fatalf("SetAgentStatus: %v", err)
	}
	if _, err := store.UpdateAgentPolicy(ctx, db.UpdateAgentPolicyParams{ID: agent.ID, Policy: []byte(`{"x":1}`), PolicyUpdatedBy: ownerID}); err != nil {
		t.Fatalf("UpdateAgentPolicy: %v", err)
	}
	if err := store.ClearAgentWorkspaces(ctx, agent.ID); err != nil {
		t.Fatalf("ClearAgentWorkspaces: %v", err)
	}
	if err := store.DeleteAgent(ctx, agent.ID); err != nil {
		t.Fatalf("DeleteAgent: %v", err)
	}
}
