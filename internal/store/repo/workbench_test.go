package repo

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/db"
	"github.com/galileostudio/baleia/internal/sqlref"
	"github.com/galileostudio/baleia/internal/store/model"
	"github.com/galileostudio/baleia/internal/testdb"
	"github.com/galileostudio/baleia/internal/workbench/folders"
	"gorm.io/gorm"
)

// workbenchFixture is a shared workspace with its default space, an owner, a
// member and a restricted space, plus a granted catalog and a binding.
type workbenchFixture struct {
	gdb     *gorm.DB
	r       *Repo
	owner   model.UUID
	member  model.UUID
	ws      model.UUID
	space   model.UUID
	private model.UUID
	catalog model.UUID
	binding model.UUID
	engine  model.UUID
}

func newWorkbenchFixture(t *testing.T) *workbenchFixture {
	t.Helper()
	gdb := testdb.New(t)
	r := New(gdb)
	ctx := context.Background()

	owner := createAPIKeyUser(t, gdb, "workbench-owner@example.test")
	member := createAPIKeyUser(t, gdb, "workbench-member@example.test")
	ownerID := PGUUID(string(owner.ID))
	memberID := PGUUID(string(member.ID))

	team, err := NewTeams(gdb).CreateTeam(ctx, db.CreateTeamParams{Name: "workbench", CreatedBy: ownerID})
	if err != nil {
		t.Fatalf("CreateTeam: %v", err)
	}
	for id, role := range map[db.UUID]string{ownerID: "owner", memberID: "member"} {
		if _, err := NewTeams(gdb).AddTeamMember(ctx, db.AddTeamMemberParams{TeamID: team.ID, UserID: id, Role: role}); err != nil {
			t.Fatalf("AddTeamMember: %v", err)
		}
	}
	ws, err := r.CreateWorkspace(ctx, db.CreateWorkspaceParams{
		Name: "Workbench", Type: "shared", PolarisCatalogName: "ws_workbench_test", OwnerTeamID: team.ID,
	})
	if err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}
	def, err := r.GetDefaultSpace(ctx, ws.ID)
	if err != nil {
		t.Fatalf("GetDefaultSpace: %v", err)
	}
	private, err := r.CreateSpace(ctx, db.CreateSpaceParams{
		WorkspaceID: ws.ID, Name: "Private", Slug: "private", Visibility: "restricted",
		DefaultRole: "viewer", CreatedBy: ownerID,
	})
	if err != nil {
		t.Fatalf("CreateSpace: %v", err)
	}

	now := time.Now().UTC()
	conn := model.CatalogConnection{
		ID: model.UUID(NewUUID()), Name: model.Text("workbench-conn-" + NewUUID()[:8]),
		Type: "polaris", URL: "http://polaris.test", CreatedAt: now, UpdatedAt: now,
	}
	if err := gdb.Create(&conn).Error; err != nil {
		t.Fatalf("create connection: %v", err)
	}
	catalog := model.Catalog{
		ID: model.UUID(NewUUID()), ConnectionID: conn.ID, Name: model.Text("lake_" + NewUUID()[:8]),
		Origin: "native", CreatedAt: now, UpdatedAt: now,
	}
	if err := gdb.Create(&catalog).Error; err != nil {
		t.Fatalf("create catalog: %v", err)
	}
	if err := gdb.Create(&model.WorkspaceCatalogGrant{
		WorkspaceID: model.UUID(UUIDString(ws.ID)), CatalogID: catalog.ID,
		GrantedBy: owner.ID, GrantedAt: now,
	}).Error; err != nil {
		t.Fatalf("create grant: %v", err)
	}
	engineConn := model.CatalogConnection{
		ID: model.UUID(NewUUID()), Name: model.Text("workbench-engine-" + NewUUID()[:8]),
		Type: "trino", URL: "http://trino.test", CreatedAt: now, UpdatedAt: now,
	}
	if err := gdb.Create(&engineConn).Error; err != nil {
		t.Fatalf("create engine connection: %v", err)
	}
	engine := model.Engine{
		ID: model.UUID(NewUUID()), Name: model.Text("trino"), Kind: "trino", Class: "federated",
		ConnectionID: engineConn.ID, CreatedAt: now, UpdatedAt: now,
	}
	if err := gdb.Create(&engine).Error; err != nil {
		t.Fatalf("create engine: %v", err)
	}
	binding := model.CatalogBinding{
		ID: model.UUID(NewUUID()), CatalogID: catalog.ID, EngineID: engine.ID,
		EngineCatalogName: "lake_t", Mode: "adopted", CreatedAt: now, UpdatedAt: now,
	}
	if err := gdb.Create(&binding).Error; err != nil {
		t.Fatalf("create binding: %v", err)
	}

	return &workbenchFixture{
		gdb: gdb, r: r, owner: owner.ID, member: member.ID,
		ws: model.UUID(UUIDString(ws.ID)), space: model.UUID(UUIDString(def.ID)),
		private: model.UUID(UUIDString(private.ID)), catalog: catalog.ID,
		binding: binding.ID, engine: engine.ID,
	}
}

func (f *workbenchFixture) wsID() db.UUID      { return PGUUID(string(f.ws)) }
func (f *workbenchFixture) spaceID() db.UUID   { return PGUUID(string(f.space)) }
func (f *workbenchFixture) privateID() db.UUID { return PGUUID(string(f.private)) }
func (f *workbenchFixture) ownerID() db.UUID   { return PGUUID(string(f.owner)) }
func (f *workbenchFixture) memberID() db.UUID  { return PGUUID(string(f.member)) }
func (f *workbenchFixture) catalogID() db.UUID { return PGUUID(string(f.catalog)) }
func (f *workbenchFixture) bindingID() db.UUID { return PGUUID(string(f.binding)) }
func (f *workbenchFixture) engineID() db.UUID  { return PGUUID(string(f.engine)) }

// TestWorkbenchFolders covers folder creation, slug uniqueness, reads,
// updates, a transactional subtree move with its saved queries, visibility
// and delete (#770).
func TestWorkbenchFolders(t *testing.T) {
	t.Parallel()
	f := newWorkbenchFixture(t)
	ctx := context.Background()

	root, err := f.r.CreateQueryFolder(ctx, db.CreateQueryFolderParams{
		WorkspaceID: f.wsID(), SpaceID: f.spaceID(), Name: "Reports", Slug: "reports",
	})
	if err != nil {
		t.Fatalf("CreateQueryFolder: %v", err)
	}
	if _, err := f.r.CreateQueryFolder(ctx, db.CreateQueryFolderParams{
		WorkspaceID: f.wsID(), SpaceID: f.spaceID(), Name: "Duplicado", Slug: "reports",
	}); err == nil {
		t.Fatal("duplicate folder slug must fail")
	} else {
		pgErr, ok := db.AsPGError(err)
		if !ok || pgErr.Code != "23505" {
			t.Fatalf("duplicate slug = %v, want a unique violation", err)
		}
	}
	exists, err := f.r.FolderSlugExists(ctx, db.FolderSlugExistsParams{SpaceID: f.spaceID(), Slug: "reports"})
	if err != nil || !exists {
		t.Fatalf("FolderSlugExists = %v, %v", exists, err)
	}

	child, err := f.r.CreateQueryFolder(ctx, db.CreateQueryFolderParams{
		WorkspaceID: f.wsID(), SpaceID: f.spaceID(), ParentID: root.ID, Name: "Q1", Slug: "q1",
	})
	if err != nil {
		t.Fatalf("CreateQueryFolder(child): %v", err)
	}
	got, err := f.r.GetQueryFolderByID(ctx, db.GetQueryFolderByIDParams{ID: child.ID, WorkspaceID: f.wsID()})
	if err != nil || got.ParentID != root.ID {
		t.Fatalf("GetQueryFolderByID = %+v, %v", got, err)
	}
	grandchild, err := f.r.CreateQueryFolder(ctx, db.CreateQueryFolderParams{
		WorkspaceID: f.wsID(), SpaceID: f.spaceID(), ParentID: child.ID, Name: "Deep", Slug: "deep",
	})
	if err != nil {
		t.Fatalf("CreateQueryFolder(grandchild): %v", err)
	}

	// Moving the child to the restricted space takes its saved query along.
	saved, err := f.r.CreateSavedQuery(ctx, db.CreateSavedQueryParams{
		WorkspaceID: f.wsID(), SpaceID: f.spaceID(), FolderID: child.ID, CreatedBy: f.ownerID(),
		UpdatedBy: f.ownerID(), Name: "Monthly", Slug: "monthly", Sql: "select 1",
	})
	if err != nil {
		t.Fatalf("CreateSavedQuery: %v", err)
	}
	newSlug := "q1_moved"
	err = f.r.TxFolders(ctx, func(q folders.Mover) error {
		move := db.MoveQueryFolderSubtreeParams{ID: child.ID, WorkspaceID: f.wsID(), SpaceID: f.privateID()}
		if err := q.MoveQueryFolderSubtree(ctx, move); err != nil {
			return err
		}
		if err := q.MoveSavedQueriesInFolderSubtree(ctx, db.MoveSavedQueriesInFolderSubtreeParams(move)); err != nil {
			return err
		}
		_, err := q.UpdateQueryFolder(ctx, db.UpdateQueryFolderParams{
			ID: child.ID, WorkspaceID: f.wsID(), Name: "Q1 moved", Slug: &newSlug,
			SpaceID: f.privateID(),
		})
		return err
	})
	if err != nil {
		t.Fatalf("TxFolders: %v", err)
	}
	moved, err := f.r.GetQueryFolderByID(ctx, db.GetQueryFolderByIDParams{ID: child.ID, WorkspaceID: f.wsID()})
	if err != nil || moved.SpaceID != f.privateID() || moved.Slug != newSlug {
		t.Fatalf("moved folder = %+v, %v", moved, err)
	}
	movedQuery, err := f.r.GetSavedQueryByID(ctx, db.GetSavedQueryByIDParams{ID: saved.ID, WorkspaceID: f.wsID()})
	if err != nil || movedQuery.SpaceID != f.privateID() {
		t.Fatalf("moved saved query = %+v, %v", movedQuery, err)
	}
	deep, err := f.r.GetQueryFolderByID(ctx, db.GetQueryFolderByIDParams{ID: grandchild.ID, WorkspaceID: f.wsID()})
	if err != nil || deep.SpaceID != f.privateID() || deep.ParentID != child.ID {
		t.Fatalf("descendant folder = %+v, %v", deep, err)
	}

	// The owner sees both spaces; the member sees only the default one until
	// the restricted space grants them access.
	all, err := f.r.ListAllVisibleQueryFolders(ctx, db.ListAllVisibleQueryFoldersParams{
		WorkspaceID: f.wsID(), UserID: f.ownerID(),
	})
	if err != nil || len(all) != 3 {
		t.Fatalf("owner folders = %d, %v", len(all), err)
	}
	memberAll, err := f.r.ListAllVisibleQueryFolders(ctx, db.ListAllVisibleQueryFoldersParams{
		WorkspaceID: f.wsID(), UserID: f.memberID(),
	})
	if err != nil || len(memberAll) != 1 || memberAll[0].ID != root.ID {
		t.Fatalf("member folders = %+v, %v", memberAll, err)
	}
	if _, err := f.r.SetSpaceMember(ctx, db.SetSpaceMemberParams{
		SpaceID: f.privateID(), PrincipalType: "team", PrincipalID: PGUUID(string(f.teamID(t))),
		Role: "viewer", GrantedBy: f.ownerID(),
	}); err != nil {
		t.Fatalf("SetSpaceMember: %v", err)
	}
	memberAll, err = f.r.ListAllVisibleQueryFolders(ctx, db.ListAllVisibleQueryFoldersParams{
		WorkspaceID: f.wsID(), UserID: f.memberID(),
	})
	if err != nil || len(memberAll) != 3 {
		t.Fatalf("member folders after grant = %d, %v", len(memberAll), err)
	}
	children, err := f.r.ListVisibleQueryFolders(ctx, db.ListVisibleQueryFoldersParams{
		WorkspaceID: f.wsID(), UserID: f.ownerID(), SpaceID: f.privateID(),
	})
	if err != nil || len(children) != 1 || children[0].ID != child.ID {
		t.Fatalf("private roots = %+v, %v", children, err)
	}

	deleted, err := f.r.DeleteQueryFolder(ctx, db.DeleteQueryFolderParams{ID: child.ID, WorkspaceID: f.wsID()})
	if err != nil || deleted != 1 {
		t.Fatalf("DeleteQueryFolder = %d, %v", deleted, err)
	}
	if _, err := f.r.GetQueryFolderByID(ctx, db.GetQueryFolderByIDParams{ID: child.ID, WorkspaceID: f.wsID()}); !errors.Is(err, db.ErrNoRows) {
		t.Fatalf("deleted folder lookup = %v, want ErrNoRows", err)
	}
}

// teamID returns the workspace's owner team id.
func (f *workbenchFixture) teamID(t *testing.T) model.UUID {
	t.Helper()
	var ws model.Workspace
	if err := f.gdb.Where("id = ?", string(f.ws)).First(&ws).Error; err != nil {
		t.Fatalf("load workspace: %v", err)
	}
	if ws.OwnerTeamID == nil {
		t.Fatal("workspace has no owner team")
	}
	return *ws.OwnerTeamID
}

// TestWorkbenchSavedQueries covers the visible listing, folder filter, search,
// versions and get (#770).
func TestWorkbenchSavedQueries(t *testing.T) {
	t.Parallel()
	f := newWorkbenchFixture(t)
	ctx := context.Background()

	folder, err := f.r.CreateQueryFolder(ctx, db.CreateQueryFolderParams{
		WorkspaceID: f.wsID(), SpaceID: f.spaceID(), Name: "Reports", Slug: "reports",
	})
	if err != nil {
		t.Fatalf("CreateQueryFolder: %v", err)
	}
	rootQuery, err := f.r.CreateSavedQuery(ctx, db.CreateSavedQueryParams{
		WorkspaceID: f.wsID(), SpaceID: f.spaceID(), CreatedBy: f.ownerID(), UpdatedBy: f.ownerID(),
		Name: "Monthly revenue", Slug: "monthly_revenue", Sql: "select 1",
	})
	if err != nil {
		t.Fatalf("CreateSavedQuery(root): %v", err)
	}
	folderQuery, err := f.r.CreateSavedQuery(ctx, db.CreateSavedQueryParams{
		WorkspaceID: f.wsID(), SpaceID: f.spaceID(), FolderID: folder.ID, CreatedBy: f.ownerID(),
		UpdatedBy: f.ownerID(), Name: "Monthly costs", Slug: "monthly_costs", Sql: "select 2",
	})
	if err != nil {
		t.Fatalf("CreateSavedQuery(folder): %v", err)
	}
	privateQuery, err := f.r.CreateSavedQuery(ctx, db.CreateSavedQueryParams{
		WorkspaceID: f.wsID(), SpaceID: f.privateID(), CreatedBy: f.ownerID(), UpdatedBy: f.ownerID(),
		Name: "Secret monthly", Slug: "secret_monthly", Sql: "select 3",
	})
	if err != nil {
		t.Fatalf("CreateSavedQuery(private): %v", err)
	}

	inFolder, err := f.r.ListVisibleSavedQueries(ctx, db.ListVisibleSavedQueriesParams{
		WorkspaceID: f.wsID(), UserID: f.ownerID(), FolderID: folder.ID,
	})
	if err != nil || len(inFolder) != 1 || inFolder[0].ID != folderQuery.ID {
		t.Fatalf("folder listing = %+v, %v", inFolder, err)
	}
	rootOnly, err := f.r.ListVisibleSavedQueries(ctx, db.ListVisibleSavedQueriesParams{
		WorkspaceID: f.wsID(), UserID: f.ownerID(), SpaceID: f.spaceID(),
	})
	if err != nil || len(rootOnly) != 1 || rootOnly[0].ID != rootQuery.ID {
		t.Fatalf("root listing = %+v, %v", rootOnly, err)
	}
	search := "monthly"
	limit := int32(1)
	searched, err := f.r.SearchVisibleSavedQueries(ctx, db.SearchVisibleSavedQueriesParams{
		WorkspaceID: f.wsID(), UserID: f.ownerID(), Q: &search, Lim: &limit,
	})
	if err != nil || len(searched) != 1 {
		t.Fatalf("search = %+v, %v; want one limited hit", searched, err)
	}
	if _, err := f.r.SearchVisibleSavedQueries(ctx, db.SearchVisibleSavedQueriesParams{
		WorkspaceID: f.wsID(), UserID: f.memberID(), Q: &search,
	}); err != nil {
		t.Fatalf("member search: %v", err)
	}
	memberHits, err := f.r.SearchVisibleSavedQueries(ctx, db.SearchVisibleSavedQueriesParams{
		WorkspaceID: f.wsID(), UserID: f.memberID(), Q: &search,
	})
	if err != nil || len(memberHits) != 2 {
		t.Fatalf("member visible hits = %d, %v; want the two default-space hits", len(memberHits), err)
	}

	// Versions are reduced to the latest per entity in Go.
	for version, id := range map[int32]db.UUID{1: rootQuery.ID, 2: rootQuery.ID, 4: folderQuery.ID} {
		if _, err := f.r.InsertContentVersion(ctx, db.InsertContentVersionParams{
			WorkspaceID: f.wsID(), SpaceID: f.spaceID(), EntityType: "saved_query", EntityID: id,
			Version: version, ChangeKind: "update", Snapshot: []byte(`{}`),
		}); err != nil {
			t.Fatalf("InsertContentVersion(%d): %v", version, err)
		}
	}
	versions, err := f.r.ListSavedQueryVersions(ctx, f.wsID())
	if err != nil {
		t.Fatalf("ListSavedQueryVersions: %v", err)
	}
	byID := map[[16]byte]int32{}
	for _, v := range versions {
		byID[v.EntityID.Bytes] = v.Version
	}
	if byID[rootQuery.ID.Bytes] != 2 || byID[folderQuery.ID.Bytes] != 4 {
		t.Fatalf("versions = %+v, want root 2 and folder 4", versions)
	}

	if got, err := f.r.GetSavedQueryByID(ctx, db.GetSavedQueryByIDParams{ID: privateQuery.ID, WorkspaceID: f.wsID()}); err != nil || got.Name != "Secret monthly" {
		t.Fatalf("GetSavedQueryByID = %+v, %v", got, err)
	}
	if exists, err := f.r.SavedQuerySlugExists(ctx, db.SavedQuerySlugExistsParams{WorkspaceID: f.wsID(), Slug: "monthly_revenue"}); err != nil || !exists {
		t.Fatalf("SavedQuerySlugExists = %v, %v", exists, err)
	}
}

// TestWorkbenchExecutions covers the execution lifecycle, queue transitions,
// statement reads and the history listing (#770).
func TestWorkbenchExecutions(t *testing.T) {
	t.Parallel()
	f := newWorkbenchFixture(t)
	ctx := context.Background()

	create := func(sqlText string) db.QueryExecution {
		t.Helper()
		e, err := f.r.CreateQueryExecution(ctx, db.CreateQueryExecutionParams{
			WorkspaceID: f.wsID(), UserID: f.ownerID(), Status: "running", Sql: sqlText,
			EngineID: f.engineID(), TargetBindingID: f.bindingID(), TargetCatalogID: f.catalogID(),
			TargetSchema: stringPtr("sales"), TargetSnapshot: []byte(`{"engine":"trino"}`),
		})
		if err != nil {
			t.Fatalf("CreateQueryExecution: %v", err)
		}
		return e
	}

	// running → finished, and a terminal row never transitions again.
	finished := create("select 1")
	got, err := f.r.GetQueryExecutionByIDAndWorkspace(ctx, db.GetQueryExecutionByIDAndWorkspaceParams{ID: finished.ID, WorkspaceID: f.wsID()})
	if err != nil || got.Status != "running" {
		t.Fatalf("GetQueryExecutionByIDAndWorkspace = %+v, %v", got, err)
	}
	engineQuery := "20260101_000000_00000_abcde"
	rows, duration := int64(3), int64(12)
	done, err := f.r.UpdateQueryExecution(ctx, db.UpdateQueryExecutionParams{
		ID: finished.ID, Status: "finished", EngineQueryID: &engineQuery,
		RowsReturned: &rows, DurationMs: &duration, FinishedAt: PGTime(time.Now().UTC()),
	})
	if err != nil || done.Status != "finished" || done.RowsReturned == nil || *done.RowsReturned != 3 {
		t.Fatalf("UpdateQueryExecution = %+v, %v", done, err)
	}
	if _, err := f.r.UpdateQueryExecution(ctx, db.UpdateQueryExecutionParams{ID: finished.ID, Status: "failed"}); !errors.Is(err, db.ErrNoRows) {
		t.Fatalf("second completion = %v, want ErrNoRows", err)
	}

	// running → cancelled.
	cancelled := create("select 2")
	if c, err := f.r.CancelQueryExecution(ctx, db.CancelQueryExecutionParams{ID: cancelled.ID, WorkspaceID: f.wsID()}); err != nil || c.Status != "cancelled" || !c.FinishedAt.Valid {
		t.Fatalf("CancelQueryExecution = %+v, %v", c, err)
	}
	if _, err := f.r.CancelQueryExecution(ctx, db.CancelQueryExecutionParams{ID: cancelled.ID, WorkspaceID: f.wsID()}); !errors.Is(err, db.ErrNoRows) {
		t.Fatalf("second cancel = %v, want ErrNoRows", err)
	}
	if _, err := f.r.CancelQueryExecution(ctx, db.CancelQueryExecutionParams{ID: cancelled.ID, WorkspaceID: PGUUID(NewUUID())}); !errors.Is(err, db.ErrNoRows) {
		t.Fatalf("foreign cancel = %v, want ErrNoRows", err)
	}

	// running → failed with the admission error code.
	failed := create("select 3")
	code := "internal"
	if e, err := f.r.FailQueryExecution(ctx, db.FailQueryExecutionParams{ID: failed.ID, ErrorCode: &code}); err != nil || e.Status != "failed" || e.ErrorCode == nil || *e.ErrorCode != code {
		t.Fatalf("FailQueryExecution = %+v, %v", e, err)
	}

	// running → queued → running, recording the wait.
	queued := create("select 4")
	if e, err := f.r.QueueQueryExecution(ctx, queued.ID); err != nil || e.Status != "queued" || !e.QueuedAt.Valid {
		t.Fatalf("QueueQueryExecution = %+v, %v", e, err)
	}
	admitted, err := f.r.AdmitQueryExecution(ctx, queued.ID)
	if err != nil || admitted.Status != "running" || !admitted.AdmittedAt.Valid || admitted.QueueWaitMs == nil {
		t.Fatalf("AdmitQueryExecution = %+v, %v", admitted, err)
	}
	if _, err := f.r.AdmitQueryExecution(ctx, queued.ID); !errors.Is(err, db.ErrNoRows) {
		t.Fatalf("second admit = %v, want ErrNoRows", err)
	}

	page, err := f.r.ListExecutionHistory(ctx, db.ListExecutionHistoryParams{
		WorkspaceID: f.wsID(), UserID: f.ownerID(), Lim: 10,
	})
	if err != nil || len(page) != 4 {
		t.Fatalf("history = %d, %v; want 4", len(page), err)
	}
	if page[0].UserName == "" {
		t.Fatal("history must include the runner name")
	}
	other, err := f.r.ListExecutionHistory(ctx, db.ListExecutionHistoryParams{
		WorkspaceID: f.wsID(), UserID: f.memberID(), Lim: 10,
	})
	if err != nil || len(other) != 0 {
		t.Fatalf("member history = %d, %v; want none", len(other), err)
	}
	if owner, err := f.r.IsWorkspaceStructuralOwner(ctx, db.IsWorkspaceStructuralOwnerParams{ID: f.wsID(), UserID: f.ownerID()}); err != nil || !owner {
		t.Fatalf("owner check = %v, %v", owner, err)
	}
	if owner, err := f.r.IsWorkspaceStructuralOwner(ctx, db.IsWorkspaceStructuralOwnerParams{ID: f.wsID(), UserID: f.memberID()}); err != nil || owner {
		t.Fatalf("member owner check = %v, %v", owner, err)
	}
	if _, err := f.r.ListQueryExecutionsByWorkspace(ctx, db.ListQueryExecutionsByWorkspaceParams{WorkspaceID: f.wsID(), Limit: 2}); err != nil {
		t.Fatalf("ListQueryExecutionsByWorkspace: %v", err)
	}
}

// TestWorkbenchStatementsAndReferences covers the statement transitions,
// handles, workspace-scoped reads and the references/joins round trip (#770).
func TestWorkbenchStatementsAndReferences(t *testing.T) {
	t.Parallel()
	f := newWorkbenchFixture(t)
	ctx := context.Background()

	exec, err := f.r.CreateQueryExecution(ctx, db.CreateQueryExecutionParams{
		WorkspaceID: f.wsID(), UserID: f.ownerID(), Status: "running", Sql: "select 1",
		EngineID: f.engineID(), TargetCatalogID: f.catalogID(), TargetSchema: stringPtr("sales"),
	})
	if err != nil {
		t.Fatalf("CreateQueryExecution: %v", err)
	}
	first, err := f.r.CreateQueryStatement(ctx, db.CreateQueryStatementParams{
		ExecutionID: exec.ID, Position: 1, Sql: "select 1", Status: "pending",
	})
	if err != nil {
		t.Fatalf("CreateQueryStatement(1): %v", err)
	}
	second, err := f.r.CreateQueryStatement(ctx, db.CreateQueryStatementParams{
		ExecutionID: exec.ID, Position: 2, Sql: "select 2", Status: "pending",
	})
	if err != nil {
		t.Fatalf("CreateQueryStatement(2): %v", err)
	}
	if stmts, err := f.r.ListQueryStatementsByWorkspace(ctx, db.ListQueryStatementsByWorkspaceParams{
		ExecutionID: exec.ID, WorkspaceID: f.wsID(),
	}); err != nil || len(stmts) != 2 || stmts[0].ID != first.ID || stmts[1].ID != second.ID {
		t.Fatalf("statements = %+v, %v", stmts, err)
	}
	if _, err := f.r.ListQueryStatementsByWorkspace(ctx, db.ListQueryStatementsByWorkspaceParams{
		ExecutionID: exec.ID, WorkspaceID: PGUUID(NewUUID()),
	}); err != nil {
		t.Fatalf("foreign statement listing: %v", err)
	}
	if _, err := f.r.GetQueryStatementByPosition(ctx, db.GetQueryStatementByPositionParams{
		ExecutionID: exec.ID, WorkspaceID: PGUUID(NewUUID()), Position: 1,
	}); !errors.Is(err, db.ErrNoRows) {
		t.Fatalf("foreign statement = %v, want ErrNoRows", err)
	}

	n, err := f.r.StartQueryStatement(ctx, db.StartQueryStatementParams{ExecutionID: exec.ID, Position: 1})
	if err != nil || n != 1 {
		t.Fatalf("StartQueryStatement = %d, %v", n, err)
	}
	if err := f.r.SetStatementQueryHandle(ctx, db.SetStatementQueryHandleParams{
		ExecutionID: exec.ID, Position: 1, QueryHandle: []byte(`{"id":"q1"}`),
	}); err != nil {
		t.Fatalf("SetStatementQueryHandle: %v", err)
	}
	handles, err := f.r.ListRunningStatementHandles(ctx, exec.ID)
	if err != nil || len(handles) != 1 || !sameJSON(t, handles[0].QueryHandle, `{"id": "q1"}`) {
		t.Fatalf("handles = %+v, %v", handles, err)
	}
	finishedAt := PGTime(time.Now().UTC())
	finished, err := f.r.UpdateQueryStatement(ctx, db.UpdateQueryStatementParams{
		ID: first.ID, Status: "finished", ResultSets: []byte(`[]`), FinishedAt: finishedAt,
	})
	if err != nil || finished.Status != "finished" {
		t.Fatalf("UpdateQueryStatement = %+v, %v", finished, err)
	}
	if _, err := f.r.UpdateQueryStatement(ctx, db.UpdateQueryStatementParams{ID: first.ID, Status: "failed"}); !errors.Is(err, db.ErrNoRows) {
		t.Fatalf("second statement completion = %v, want ErrNoRows", err)
	}
	if err := f.r.SetExecutionResult(ctx, db.SetExecutionResultParams{ID: exec.ID, ResultKey: stringPtr("result/1")}); err != nil {
		t.Fatalf("SetExecutionResult: %v", err)
	}

	// The second statement carries a join; persist its references atomically.
	if n, err := f.r.StartQueryStatement(ctx, db.StartQueryStatementParams{ExecutionID: exec.ID, Position: 2}); err != nil || n != 1 {
		t.Fatalf("StartQueryStatement(2) = %d, %v", n, err)
	}
	joinSQL := "SELECT o.id FROM sales.orders o JOIN sales.customers c ON o.customer_id = c.id"
	target, err := f.r.GetStatementReferenceTarget(ctx, db.GetStatementReferenceTargetParams{
		ExecutionID: exec.ID, Position: 2,
	})
	if err != nil || target.StatementID != second.ID || target.WorkspaceID != f.wsID() {
		t.Fatalf("GetStatementReferenceTarget = %+v, %v", target, err)
	}
	if err := f.r.TxWorkbench(ctx, func(tx *Repo) error {
		return sqlref.Persist(ctx, tx, target.StatementID, target.WorkspaceID, f.catalogID(), "sales", joinSQL)
	}); err != nil {
		t.Fatalf("Persist references: %v", err)
	}
	if _, err := f.r.UpdateQueryStatement(ctx, db.UpdateQueryStatementParams{
		ID: second.ID, Status: "finished", ResultSets: []byte(`[]`), FinishedAt: PGTime(time.Now().UTC()),
	}); err != nil {
		t.Fatalf("finish joined statement: %v", err)
	}
	var refs, joins int64
	if err := f.gdb.Model(&model.StatementReference{}).Where("statement_id = ?", UUIDString(second.ID)).Count(&refs).Error; err != nil || refs != 2 {
		t.Fatalf("references = %d, %v; want 2", refs, err)
	}
	if err := f.gdb.Model(&model.StatementJoin{}).Where("statement_id = ?", UUIDString(second.ID)).Count(&joins).Error; err != nil || joins != 1 {
		t.Fatalf("joins = %d, %v; want 1", joins, err)
	}

	suggestions, err := f.r.ListJoinSuggestions(ctx, db.ListJoinSuggestionsParams{
		Lim: 10, WorkspaceID: f.wsID(), CatalogID: f.catalogID(),
		LeftNs: "sales", LeftTable: "orders", RightNs: "sales", RightTable: "customers",
	})
	if err != nil || len(suggestions) != 1 || suggestions[0].LeftColumn != "customer_id" || suggestions[0].RightColumn != "id" {
		t.Fatalf("join suggestions = %+v, %v", suggestions, err)
	}
	if suggestions[0].UseCount != 1 || !suggestions[0].LastUsedAt.Valid || suggestions[0].Score <= 0 {
		t.Fatalf("join suggestion aggregates = %+v", suggestions[0])
	}

	// Foreign keys between the two tables, oriented left→right.
	now := time.Now().UTC()
	orders := model.MetadataTable{
		ID: model.UUID(NewUUID()), CatalogID: f.catalog, Namespace: model.StringArray{"sales"},
		Name: "orders", Kind: "table", Fingerprint: "fp-orders", Properties: model.JSON(`{}`),
		LastSeenAt: now, LastChangedAt: now,
	}
	customers := model.MetadataTable{
		ID: model.UUID(NewUUID()), CatalogID: f.catalog, Namespace: model.StringArray{"sales"},
		Name: "customers", Kind: "table", Fingerprint: "fp-customers", Properties: model.JSON(`{}`),
		LastSeenAt: now, LastChangedAt: now,
	}
	for _, table := range []*model.MetadataTable{&orders, &customers} {
		if err := f.gdb.Create(table).Error; err != nil {
			t.Fatalf("create metadata table: %v", err)
		}
	}
	if err := f.gdb.Create(&model.MetadataForeignKey{
		CatalogID: f.catalog, TableID: orders.ID, ColumnName: "customer_id",
		RefNamespace: model.StringArray{"sales"}, RefTable: "customers", RefColumn: "id",
		LastSeenAt: now,
	}).Error; err != nil {
		t.Fatalf("create foreign key: %v", err)
	}
	fks, err := f.r.ListForeignKeyJoinSuggestions(ctx, db.ListForeignKeyJoinSuggestionsParams{
		LeftTableID: PGUUID(string(orders.ID)), LeftNamespace: []string{"sales"}, LeftTable: "orders",
		RightTableID: PGUUID(string(customers.ID)), RightNamespace: []string{"sales"}, RightTable: "customers",
	})
	if err != nil || len(fks) != 1 || fks[0].LeftColumn != "customer_id" || fks[0].RightColumn != "id" {
		t.Fatalf("foreign-key suggestions = %+v, %v", fks, err)
	}
	granted, err := f.r.GetGrantedMetadataTable(ctx, db.GetGrantedMetadataTableParams{
		TableID: PGUUID(string(orders.ID)), WorkspaceID: f.wsID(),
	})
	if err != nil || granted.Name != "orders" || granted.Namespace[0] != "sales" {
		t.Fatalf("GetGrantedMetadataTable = %+v, %v", granted, err)
	}

	// A rerun of Persist replaces the rows instead of duplicating them.
	if err := f.r.TxWorkbench(ctx, func(tx *Repo) error {
		return sqlref.Persist(ctx, tx, target.StatementID, target.WorkspaceID, f.catalogID(), "sales", joinSQL)
	}); err != nil {
		t.Fatalf("second Persist: %v", err)
	}
	if err := f.gdb.Model(&model.StatementReference{}).Where("statement_id = ?", UUIDString(second.ID)).Count(&refs).Error; err != nil || refs != 2 {
		t.Fatalf("references after rerun = %d, %v", refs, err)
	}
	if err := f.r.DeleteStatementReferences(ctx, second.ID); err != nil {
		t.Fatalf("DeleteStatementReferences: %v", err)
	}
	if err := f.r.DeleteStatementJoins(ctx, second.ID); err != nil {
		t.Fatalf("DeleteStatementJoins: %v", err)
	}
}

func stringPtr(s string) *string { return &s }
