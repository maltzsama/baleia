//go:build e2e

package main

import (
	"context"
	"log"
	"os"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/identity/bikeys"
	"github.com/galileostudio/baleia/internal/store/repo"
)

// e2eBIKeyFile is where ensureBIKey writes the key secret the Trino JDBC e2e
// reads. The secret is shown once by the API too; a file is how a second
// process gets it.
const e2eBIKeyFile = "/tmp/baleia-e2e-bi-key.secret"

// ensureBIKey issues a workspace-bound BI key for the admin on their personal
// workspace and the default Trino engine, then writes the secret for the e2e.
// The admin's workspace is granted e2e_tpch by ensureTarget, so the gateway's
// scope check passes for that catalog and fails for any other.
func ensureBIKey(ctx context.Context, q *repo.Repo, admin db.User) error {
	workspaceID, err := q.PersonalWorkspaceID(ctx, admin.ID)
	if err != nil {
		return err
	}
	engine, err := q.GetDefaultTrinoEngine(ctx)
	if err != nil {
		return err
	}
	key, secret, err := bikeys.New(q).Create(ctx, bikeys.CreateParams{
		WorkspaceID: workspaceID,
		EngineID:    engine.Engine.ID,
		ActorID:     admin.ID,
		Name:        "e2e BI gateway",
	})
	if err != nil {
		return err
	}
	path := os.Getenv("E2E_BI_KEY_FILE")
	if path == "" {
		path = e2eBIKeyFile
	}
	if err := os.WriteFile(path, []byte(secret+"\n"), 0o600); err != nil {
		return err
	}
	log.Printf("e2e-seed: BI key %s for %s in workspace %s (secret in %s)", key.ID, admin.Email, workspaceID, path)
	return nil
}
