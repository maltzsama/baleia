package repo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/db"
	adb "github.com/galileostudio/baleia/internal/db/assistant"
	"github.com/galileostudio/baleia/internal/store/model"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"gorm.io/gorm"
)

// assistantFixture is a workspace, its user and the provisioned assistant
// agent, enough for the assistant store's conversation and message paths.
type assistantFixture struct {
	gdb     *gorm.DB
	r       *Repo
	user    pgtype.UUID
	ws      pgtype.UUID
	agentID pgtype.UUID
}

func newAssistantFixture(t *testing.T) *assistantFixture {
	t.Helper()
	gdb := openTestGorm(t)
	r := New(gdb)
	ctx := context.Background()

	user := createAPIKeyUser(t, gdb, fmt.Sprintf("assistant-%s@example.test", NewUUID()[:8]))
	userID := PGUUID(string(user.ID))
	ws, err := r.CreateWorkspace(ctx, db.CreateWorkspaceParams{
		Name: "Assistant", Type: "personal",
		PolarisCatalogName: "ws_assistant_" + NewUUID()[:8],
		OwnerUserID:        userID,
	})
	if err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}
	agent, err := r.CreateAssistantAgent(ctx, adb.CreateAssistantAgentParams{
		OwnerID: userID, Name: "Assistant", Description: "Embedded assistant",
	})
	if err != nil {
		t.Fatalf("CreateAssistantAgent: %v", err)
	}
	if err := r.AddAssistantAgentWorkspace(ctx, adb.AddAssistantAgentWorkspaceParams{
		AgentID: agent.ID, WorkspaceID: ws.ID,
	}); err != nil {
		t.Fatalf("AddAssistantAgentWorkspace: %v", err)
	}
	return &assistantFixture{gdb: gdb, r: r, user: userID, ws: ws.ID, agentID: agent.ID}
}

func (f *assistantFixture) conversation(t *testing.T) adb.AssistantConversation {
	t.Helper()
	row, err := f.r.CreateConversation(context.Background(), adb.CreateConversationParams{
		UserID: f.user, WorkspaceID: f.ws, AgentID: f.agentID,
	})
	if err != nil {
		t.Fatalf("CreateConversation: %v", err)
	}
	return row
}

// TestAssistantSettingsStore covers the single settings row: the seeded
// defaults and the merge update.
func TestAssistantSettingsStore(t *testing.T) {
	t.Parallel()
	f := newAssistantFixture(t)
	ctx := context.Background()

	row, err := f.r.GetAssistantSettings(ctx)
	if err != nil {
		t.Fatalf("GetAssistantSettings: %v", err)
	}
	if !row.ID || row.Enabled || row.Provider != nil || row.DataSharing != "metadata_only" {
		t.Fatalf("seeded settings = %+v", row)
	}

	provider, base, name, key := "anthropic", "https://api.example.test", "claude-test", "sealed"
	updated, err := f.r.UpdateAssistantSettings(ctx, adb.UpdateAssistantSettingsParams{
		Enabled: true, Provider: &provider, BaseUrl: &base, Model: &name, ApiKeyEnc: &key,
		DataSharing: "metadata_and_samples", MaxTurnSeconds: 60, MaxToolCallsPerTurn: 7,
		MaxOutputTokensPerTurn: 2048, DailyTokenBudgetPerUser: 1234, UpdatedBy: f.user,
	})
	if err != nil {
		t.Fatalf("UpdateAssistantSettings: %v", err)
	}
	if !updated.Enabled || updated.Provider == nil || *updated.Provider != provider {
		t.Fatalf("updated settings = %+v", updated)
	}
	if updated.MaxTurnSeconds != 60 || updated.MaxToolCallsPerTurn != 7 || updated.MaxOutputTokensPerTurn != 2048 {
		t.Fatalf("limit columns = %+v", updated)
	}
	if updated.DailyTokenBudgetPerUser != 1234 || updated.UpdatedBy != f.user {
		t.Fatalf("budget/updated_by = %+v", updated)
	}
	reloaded, err := f.r.GetAssistantSettings(ctx)
	if err != nil {
		t.Fatalf("GetAssistantSettings after update: %v", err)
	}
	if reloaded.ApiKeyEnc == nil || *reloaded.ApiKeyEnc != key || reloaded.BaseUrl == nil || *reloaded.BaseUrl != base {
		t.Fatalf("reloaded settings = %+v", reloaded)
	}
}

// TestAssistantAgentBootstrap covers the per-owner × workspace assistant agent:
// lookup misses until the agent is bound, then the same row comes back.
func TestAssistantAgentBootstrap(t *testing.T) {
	t.Parallel()
	gdb := openTestGorm(t)
	r := New(gdb)
	ctx := context.Background()
	user := createAPIKeyUser(t, gdb, fmt.Sprintf("agent-%s@example.test", NewUUID()[:8]))
	userID := PGUUID(string(user.ID))
	ws, err := r.CreateWorkspace(ctx, db.CreateWorkspaceParams{
		Name: "Agent", Type: "personal",
		PolarisCatalogName: "ws_agent_" + NewUUID()[:8],
		OwnerUserID:        userID,
	})
	if err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}
	other, err := r.CreateWorkspace(ctx, db.CreateWorkspaceParams{
		Name: "Other", Type: "personal",
		PolarisCatalogName: "ws_agent_other_" + NewUUID()[:8],
		OwnerUserID:        userID,
	})
	if err != nil {
		t.Fatalf("CreateWorkspace(other): %v", err)
	}

	if _, err := r.FindAssistantAgent(ctx, adb.FindAssistantAgentParams{OwnerID: userID, WorkspaceID: ws.ID}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("FindAssistantAgent before bind: err = %v, want pgx.ErrNoRows", err)
	}
	agent, err := r.CreateAssistantAgent(ctx, adb.CreateAssistantAgentParams{
		OwnerID: userID, Name: "Assistant", Description: "Embedded assistant",
	})
	if err != nil {
		t.Fatalf("CreateAssistantAgent: %v", err)
	}
	if agent.Kind != "assistant" || agent.OwnerID != userID || agent.Status != "active" {
		t.Fatalf("created agent = %+v", agent)
	}
	if err := r.AddAssistantAgentWorkspace(ctx, adb.AddAssistantAgentWorkspaceParams{
		AgentID: agent.ID, WorkspaceID: ws.ID,
	}); err != nil {
		t.Fatalf("AddAssistantAgentWorkspace: %v", err)
	}
	// Binding twice is a no-op, not a duplicate-key failure.
	if err := r.AddAssistantAgentWorkspace(ctx, adb.AddAssistantAgentWorkspaceParams{
		AgentID: agent.ID, WorkspaceID: ws.ID,
	}); err != nil {
		t.Fatalf("AddAssistantAgentWorkspace again: %v", err)
	}
	found, err := r.FindAssistantAgent(ctx, adb.FindAssistantAgentParams{OwnerID: userID, WorkspaceID: ws.ID})
	if err != nil {
		t.Fatalf("FindAssistantAgent: %v", err)
	}
	if found.ID != agent.ID {
		t.Fatalf("FindAssistantAgent = %s, want %s", found.ID, agent.ID)
	}
	// The agent is scoped to the workspace it was bound to.
	if _, err := r.FindAssistantAgent(ctx, adb.FindAssistantAgentParams{OwnerID: userID, WorkspaceID: other.ID}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("FindAssistantAgent(other workspace): err = %v, want pgx.ErrNoRows", err)
	}
}

// TestEnsureAssistantAgentIsSerialized: concurrent first conversations for one
// owner × workspace create one agent and all receive it.
func TestEnsureAssistantAgentIsSerialized(t *testing.T) {
	t.Parallel()
	gdb := openTestGorm(t)
	r := New(gdb)
	ctx := context.Background()
	user := createAPIKeyUser(t, gdb, fmt.Sprintf("ensure-%s@example.test", NewUUID()[:8]))
	userID := PGUUID(string(user.ID))
	ws, err := r.CreateWorkspace(ctx, db.CreateWorkspaceParams{
		Name: "Ensure", Type: "personal",
		PolarisCatalogName: "ws_ensure_" + NewUUID()[:8],
		OwnerUserID:        userID,
	})
	if err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}

	const callers = 6
	ids := make(chan pgtype.UUID, callers)
	errs := make(chan error, callers)
	var wg sync.WaitGroup
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			agent, err := r.EnsureAssistantAgent(ctx, userID, ws.ID, "Assistant", "Embedded assistant")
			if err != nil {
				errs <- err
				return
			}
			ids <- agent.ID
		}()
	}
	wg.Wait()
	close(errs)
	close(ids)
	for err := range errs {
		t.Fatalf("EnsureAssistantAgent: %v", err)
	}
	var want pgtype.UUID
	seen := 0
	for id := range ids {
		if seen == 0 {
			want = id
		}
		if id != want {
			t.Fatalf("agent ids differ: %s vs %s", id, want)
		}
		seen++
	}
	if seen != callers {
		t.Fatalf("agents returned = %d, want %d", seen, callers)
	}
	var agents int64
	if err := gdb.Model(&model.Agent{}).
		Where("owner_id = ? AND kind = ?", UUIDString(userID), "assistant").
		Count(&agents).Error; err != nil {
		t.Fatalf("count agents: %v", err)
	}
	if agents != 1 {
		t.Fatalf("assistant agents = %d, want 1", agents)
	}
	found, err := r.FindAssistantAgent(ctx, adb.FindAssistantAgentParams{OwnerID: userID, WorkspaceID: ws.ID})
	if err != nil || found.ID != want {
		t.Fatalf("FindAssistantAgent = %+v, %v", found, err)
	}
}

// TestEnsureAssistantAgentRollsBackOnBindingFailure: an unknown workspace
// leaves no half-provisioned agent behind.
func TestEnsureAssistantAgentRollsBackOnBindingFailure(t *testing.T) {
	t.Parallel()
	gdb := openTestGorm(t)
	r := New(gdb)
	ctx := context.Background()
	user := createAPIKeyUser(t, gdb, fmt.Sprintf("rollback-%s@example.test", NewUUID()[:8]))
	userID := PGUUID(string(user.ID))

	if _, err := r.EnsureAssistantAgent(ctx, userID, PGUUID(NewUUID()), "Assistant", "Embedded assistant"); err == nil {
		t.Fatal("EnsureAssistantAgent with an unknown workspace returned no error")
	}
	var agents int64
	if err := gdb.Model(&model.Agent{}).
		Where("owner_id = ? AND kind = ?", UUIDString(userID), "assistant").
		Count(&agents).Error; err != nil {
		t.Fatalf("count agents: %v", err)
	}
	if agents != 0 {
		t.Fatalf("assistant agents after rollback = %d, want 0", agents)
	}
}

// TestAssistantConversationStore covers create, scoped read, newest-first list,
// the one-shot title and delete with its messages.
func TestAssistantConversationStore(t *testing.T) {
	t.Parallel()
	f := newAssistantFixture(t)
	ctx := context.Background()
	first := f.conversation(t)
	second := f.conversation(t)

	if first.State != "idle" || first.LastSeq != 0 || first.Title != "" || first.AgentID != f.agentID {
		t.Fatalf("created conversation = %+v", first)
	}
	got, err := f.r.GetConversation(ctx, adb.GetConversationParams{ID: first.ID, UserID: f.user})
	if err != nil || got.ID != first.ID || got.WorkspaceID != f.ws {
		t.Fatalf("GetConversation = %+v, %v", got, err)
	}
	other := createAPIKeyUser(t, f.gdb, fmt.Sprintf("intruder-%s@example.test", NewUUID()[:8]))
	if _, err := f.r.GetConversation(ctx, adb.GetConversationParams{ID: first.ID, UserID: PGUUID(string(other.ID))}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("GetConversation(foreign user): err = %v, want pgx.ErrNoRows", err)
	}

	// Make the ordering deterministic: the first conversation is older.
	if err := f.gdb.Model(&model.AssistantConversation{}).
		Where("id = ?", UUIDString(first.ID)).
		Update("updated_at", time.Now().UTC().Add(-time.Hour)).Error; err != nil {
		t.Fatalf("age conversation: %v", err)
	}
	list, err := f.r.ListConversations(ctx, adb.ListConversationsParams{
		UserID: f.user, WorkspaceID: f.ws, Limit: 50,
	})
	if err != nil {
		t.Fatalf("ListConversations: %v", err)
	}
	if len(list) != 2 || list[0].ID != second.ID || list[1].ID != first.ID {
		t.Fatalf("ListConversations order = %+v", list)
	}
	limited, err := f.r.ListConversations(ctx, adb.ListConversationsParams{
		UserID: f.user, WorkspaceID: f.ws, Limit: 1,
	})
	if err != nil || len(limited) != 1 || limited[0].ID != second.ID {
		t.Fatalf("ListConversations(limit 1) = %+v, %v", limited, err)
	}

	// The title is set only while empty.
	if err := f.r.SetConversationTitle(ctx, adb.SetConversationTitleParams{ID: first.ID, Title: "first title"}); err != nil {
		t.Fatalf("SetConversationTitle: %v", err)
	}
	if err := f.r.SetConversationTitle(ctx, adb.SetConversationTitleParams{ID: first.ID, Title: "second title"}); err != nil {
		t.Fatalf("SetConversationTitle again: %v", err)
	}
	titled, err := f.r.GetConversation(ctx, adb.GetConversationParams{ID: first.ID, UserID: f.user})
	if err != nil || titled.Title != "first title" {
		t.Fatalf("title = %+v, %v", titled, err)
	}

	if _, err := f.r.AppendMessage(ctx, adb.AppendMessageParams{
		ConversationID: first.ID, Role: "user", Content: []byte(`{"text":"hello"}`),
	}); err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}
	if err := f.r.DeleteConversation(ctx, adb.DeleteConversationParams{ID: first.ID, UserID: f.user}); err != nil {
		t.Fatalf("DeleteConversation: %v", err)
	}
	if _, err := f.r.GetConversation(ctx, adb.GetConversationParams{ID: first.ID, UserID: f.user}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("GetConversation after delete: err = %v, want pgx.ErrNoRows", err)
	}
	if _, err := f.r.LatestConversationMessage(ctx, first.ID); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("LatestConversationMessage after delete: err = %v, want pgx.ErrNoRows", err)
	}
}

// TestAssistantTurnLifecycle covers claim exclusivity, release, the heartbeat
// and the interrupted state.
func TestAssistantTurnLifecycle(t *testing.T) {
	t.Parallel()
	f := newAssistantFixture(t)
	ctx := context.Background()
	conv := f.conversation(t)
	owner := "replica-1"

	claimed, err := f.r.ClaimConversation(ctx, adb.ClaimConversationParams{ID: conv.ID, UserID: f.user, TurnOwner: &owner})
	if err != nil {
		t.Fatalf("ClaimConversation: %v", err)
	}
	if claimed.State != "running" || claimed.TurnOwner == nil || *claimed.TurnOwner != owner || !claimed.TurnHeartbeatAt.Valid {
		t.Fatalf("claimed = %+v", claimed)
	}
	if _, err := f.r.ClaimConversation(ctx, adb.ClaimConversationParams{ID: conv.ID, UserID: f.user, TurnOwner: &owner}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("second claim: err = %v, want pgx.ErrNoRows", err)
	}

	time.Sleep(5 * time.Millisecond)
	if err := f.r.HeartbeatConversation(ctx, conv.ID); err != nil {
		t.Fatalf("HeartbeatConversation: %v", err)
	}
	beaten, err := f.r.GetConversation(ctx, adb.GetConversationParams{ID: conv.ID, UserID: f.user})
	if err != nil || !beaten.TurnHeartbeatAt.Valid || !beaten.TurnHeartbeatAt.Time.After(claimed.TurnHeartbeatAt.Time) {
		t.Fatalf("heartbeat = %+v, %v", beaten.TurnHeartbeatAt, err)
	}
	if !beaten.UpdatedAt.Time.Equal(claimed.UpdatedAt.Time.Truncate(time.Microsecond)) {
		t.Fatalf("heartbeat moved updated_at: %v -> %v", claimed.UpdatedAt.Time, beaten.UpdatedAt.Time)
	}

	// A stale runner must not release a turn it no longer owns.
	if err := f.r.ReleaseConversation(ctx, conv.ID, "replica-2"); err != nil {
		t.Fatalf("ReleaseConversation(stale token): %v", err)
	}
	held, err := f.r.GetConversation(ctx, adb.GetConversationParams{ID: conv.ID, UserID: f.user})
	if err != nil || held.State != "running" || held.TurnOwner == nil || *held.TurnOwner != owner {
		t.Fatalf("stale release changed the turn = %+v, %v", held, err)
	}
	if err := f.r.ReleaseConversation(ctx, conv.ID, owner); err != nil {
		t.Fatalf("ReleaseConversation: %v", err)
	}
	released, err := f.r.GetConversation(ctx, adb.GetConversationParams{ID: conv.ID, UserID: f.user})
	if err != nil || released.State != "idle" || released.TurnOwner != nil || released.TurnHeartbeatAt.Valid {
		t.Fatalf("released = %+v, %v", released, err)
	}

	// An idle conversation can be claimed again; an interrupted one can too.
	if _, err := f.r.ClaimConversation(ctx, adb.ClaimConversationParams{ID: conv.ID, UserID: f.user, TurnOwner: &owner}); err != nil {
		t.Fatalf("claim after release: %v", err)
	}
	if err := f.r.SetConversationInterrupted(ctx, conv.ID); err != nil {
		t.Fatalf("SetConversationInterrupted: %v", err)
	}
	interrupted, err := f.r.GetConversation(ctx, adb.GetConversationParams{ID: conv.ID, UserID: f.user})
	if err != nil || interrupted.State != "interrupted" || interrupted.TurnOwner != nil || interrupted.TurnHeartbeatAt.Valid {
		t.Fatalf("interrupted = %+v, %v", interrupted, err)
	}
	// A heartbeat outside a running turn leaves the lease alone.
	if err := f.r.HeartbeatConversation(ctx, conv.ID); err != nil {
		t.Fatalf("HeartbeatConversation while interrupted: %v", err)
	}
	still, err := f.r.GetConversation(ctx, adb.GetConversationParams{ID: conv.ID, UserID: f.user})
	if err != nil || still.TurnHeartbeatAt.Valid {
		t.Fatalf("heartbeat while interrupted = %+v, %v", still.TurnHeartbeatAt, err)
	}
	if _, err := f.r.ClaimConversation(ctx, adb.ClaimConversationParams{ID: conv.ID, UserID: f.user, TurnOwner: &owner}); err != nil {
		t.Fatalf("claim after interrupt: %v", err)
	}
}

// TestAssistantInterruptStale covers the sweeper: only turns whose heartbeat is
// older than the lease are interrupted, and their ids come back.
func TestAssistantInterruptStale(t *testing.T) {
	t.Parallel()
	f := newAssistantFixture(t)
	ctx := context.Background()
	stale := f.conversation(t)
	fresh := f.conversation(t)
	owner := "replica-1"
	for _, id := range []pgtype.UUID{stale.ID, fresh.ID} {
		if _, err := f.r.ClaimConversation(ctx, adb.ClaimConversationParams{ID: id, UserID: f.user, TurnOwner: &owner}); err != nil {
			t.Fatalf("ClaimConversation: %v", err)
		}
	}
	if err := f.gdb.Model(&model.AssistantConversation{}).
		Where("id = ?", UUIDString(stale.ID)).
		Update("turn_heartbeat_at", time.Now().UTC().Add(-2*time.Minute)).Error; err != nil {
		t.Fatalf("age heartbeat: %v", err)
	}

	ids, err := f.r.InterruptStaleConversations(ctx)
	if err != nil {
		t.Fatalf("InterruptStaleConversations: %v", err)
	}
	if len(ids) != 1 || ids[0] != stale.ID {
		t.Fatalf("interrupted ids = %v, want [%s]", ids, stale.ID)
	}
	gotStale, err := f.r.GetConversation(ctx, adb.GetConversationParams{ID: stale.ID, UserID: f.user})
	if err != nil || gotStale.State != "interrupted" || gotStale.TurnOwner != nil {
		t.Fatalf("stale conversation = %+v, %v", gotStale, err)
	}
	gotFresh, err := f.r.GetConversation(ctx, adb.GetConversationParams{ID: fresh.ID, UserID: f.user})
	if err != nil || gotFresh.State != "running" {
		t.Fatalf("fresh conversation = %+v, %v", gotFresh, err)
	}
}

// TestAssistantMessageStore covers append/list/latest and the JSON round-trip.
func TestAssistantMessageStore(t *testing.T) {
	t.Parallel()
	f := newAssistantFixture(t)
	ctx := context.Background()
	conv := f.conversation(t)

	if rows, err := f.r.ListMessages(ctx, adb.ListMessagesParams{ConversationID: conv.ID, Seq: 0, Limit: 10}); err != nil || len(rows) != 0 {
		t.Fatalf("ListMessages(empty) = %+v, %v", rows, err)
	}
	in, out := int32(3), int32(4)
	first, err := f.r.AppendMessage(ctx, adb.AppendMessageParams{
		ConversationID: conv.ID, Role: "user", Content: []byte(`{"text":"hello"}`), InputTokens: &in,
	})
	if err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}
	var firstContent map[string]any
	if err := json.Unmarshal(first.Content, &firstContent); err != nil {
		t.Fatalf("first message content %s: %v", first.Content, err)
	}
	if first.Seq != 1 || first.Role != "user" || firstContent["text"] != "hello" {
		t.Fatalf("first message = %+v (content %s)", first, first.Content)
	}
	second, err := f.r.AppendMessage(ctx, adb.AppendMessageParams{
		ConversationID: conv.ID, Role: "assistant", Content: []byte(`{"text":"hi"}`), InputTokens: &in, OutputTokens: &out,
	})
	if err != nil || second.Seq != 2 {
		t.Fatalf("second message = %+v, %v", second, err)
	}
	if _, err := f.r.AppendMessage(ctx, adb.AppendMessageParams{
		ConversationID: conv.ID, Role: "notice", Content: []byte(`{"kind":"x","detail":"y"}`),
	}); err != nil {
		t.Fatalf("AppendMessage(notice): %v", err)
	}

	updated, err := f.r.GetConversation(ctx, adb.GetConversationParams{ID: conv.ID, UserID: f.user})
	if err != nil || updated.LastSeq != 3 {
		t.Fatalf("last_seq = %d, %v", updated.LastSeq, err)
	}
	rows, err := f.r.ListMessages(ctx, adb.ListMessagesParams{ConversationID: conv.ID, Seq: 1, Limit: 10})
	if err != nil || len(rows) != 2 || rows[0].Seq != 2 || rows[1].Seq != 3 {
		t.Fatalf("ListMessages(after 1) = %+v, %v", rows, err)
	}
	one, err := f.r.ListMessages(ctx, adb.ListMessagesParams{ConversationID: conv.ID, Seq: 0, Limit: 1})
	if err != nil || len(one) != 1 || one[0].Seq != 1 {
		t.Fatalf("ListMessages(limit 1) = %+v, %v", one, err)
	}
	latest, err := f.r.LatestConversationMessage(ctx, conv.ID)
	if err != nil || latest.Seq != 3 || latest.Role != "notice" {
		t.Fatalf("LatestConversationMessage = %+v, %v", latest, err)
	}
	if _, err := f.r.AppendMessage(ctx, adb.AppendMessageParams{
		ConversationID: PGUUID(NewUUID()), Role: "user", Content: []byte(`{"text":"x"}`),
	}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("AppendMessage(missing conversation): err = %v, want pgx.ErrNoRows", err)
	}
}

// TestAssistantMessageAppendIsAtomic: concurrent appends get distinct,
// contiguous sequence numbers; last_seq matches the highest one.
func TestAssistantMessageAppendIsAtomic(t *testing.T) {
	t.Parallel()
	f := newAssistantFixture(t)
	ctx := context.Background()
	conv := f.conversation(t)

	const writers = 6
	var wg sync.WaitGroup
	errs := make(chan error, writers)
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := f.r.AppendMessage(ctx, adb.AppendMessageParams{
				ConversationID: conv.ID, Role: "user",
				Content: []byte(fmt.Sprintf(`{"text":"%d"}`, i)),
			})
			errs <- err
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("AppendMessage: %v", err)
		}
	}
	rows, err := f.r.ListMessages(ctx, adb.ListMessagesParams{ConversationID: conv.ID, Seq: 0, Limit: 100})
	if err != nil || len(rows) != writers {
		t.Fatalf("ListMessages = %d rows, %v", len(rows), err)
	}
	for i, row := range rows {
		if row.Seq != int32(i+1) {
			t.Fatalf("seq[%d] = %d, want %d", i, row.Seq, i+1)
		}
	}
	updated, err := f.r.GetConversation(ctx, adb.GetConversationParams{ID: conv.ID, UserID: f.user})
	if err != nil || updated.LastSeq != int32(writers) {
		t.Fatalf("last_seq = %d, %v", updated.LastSeq, err)
	}
}

// TestAssistantTokenCount covers the 24 h usage sum: only the user's
// conversations, only messages with both token sides.
func TestAssistantTokenCount(t *testing.T) {
	t.Parallel()
	f := newAssistantFixture(t)
	ctx := context.Background()
	conv := f.conversation(t)
	other := f.conversation(t)

	appendTokens := func(id pgtype.UUID, in, out *int32) {
		t.Helper()
		if _, err := f.r.AppendMessage(ctx, adb.AppendMessageParams{
			ConversationID: id, Role: "assistant", Content: []byte(`{"text":"x"}`),
			InputTokens: in, OutputTokens: out,
		}); err != nil {
			t.Fatalf("AppendMessage: %v", err)
		}
	}
	ten, five, twenty, two, one := int32(10), int32(5), int32(20), int32(2), int32(1)
	appendTokens(conv.ID, &ten, &five)
	appendTokens(conv.ID, &twenty, &two)
	appendTokens(other.ID, &one, &one)
	// A one-sided message contributes nothing, as a NULL summand did.
	appendTokens(conv.ID, &ten, nil)

	// Another user's conversation never counts.
	stranger := createAPIKeyUser(t, f.gdb, fmt.Sprintf("tokens-%s@example.test", NewUUID()[:8]))
	strangerID := PGUUID(string(stranger.ID))
	strangerWS, err := f.r.CreateWorkspace(ctx, db.CreateWorkspaceParams{
		Name: "Stranger", Type: "personal",
		PolarisCatalogName: "ws_tokens_" + NewUUID()[:8],
		OwnerUserID:        strangerID,
	})
	if err != nil {
		t.Fatalf("CreateWorkspace(stranger): %v", err)
	}
	agent, err := f.r.CreateAssistantAgent(ctx, adb.CreateAssistantAgentParams{OwnerID: strangerID, Name: "Assistant"})
	if err != nil {
		t.Fatalf("CreateAssistantAgent(stranger): %v", err)
	}
	strangerConv, err := f.r.CreateConversation(ctx, adb.CreateConversationParams{
		UserID: strangerID, WorkspaceID: strangerWS.ID, AgentID: agent.ID,
	})
	if err != nil {
		t.Fatalf("CreateConversation(stranger): %v", err)
	}
	hundred := int32(100)
	appendTokens(strangerConv.ID, &hundred, &hundred)

	// A message older than the window does not count, even in the user's own
	// conversation.
	thousand := int32(1000)
	old, err := f.r.AppendMessage(ctx, adb.AppendMessageParams{
		ConversationID: conv.ID, Role: "assistant", Content: []byte(`{"text":"old"}`),
		InputTokens: &thousand, OutputTokens: &thousand,
	})
	if err != nil {
		t.Fatalf("AppendMessage(old): %v", err)
	}
	if err := f.gdb.Model(&model.AssistantMessage{}).
		Where("conversation_id = ? AND seq = ?", UUIDString(conv.ID), old.Seq).
		Update("created_at", time.Now().UTC().Add(-2*time.Hour)).Error; err != nil {
		t.Fatalf("age message: %v", err)
	}

	used, err := f.r.CountUserTokensSince(ctx, adb.CountUserTokensSinceParams{
		UserID: f.user, CreatedAt: pgtype.Timestamptz{Time: time.Now().Add(-time.Hour), Valid: true},
	})
	if err != nil {
		t.Fatalf("CountUserTokensSince: %v", err)
	}
	if want := int64(10 + 5 + 20 + 2 + 1 + 1); used != want {
		t.Fatalf("used = %d, want %d", used, want)
	}
	future, err := f.r.CountUserTokensSince(ctx, adb.CountUserTokensSinceParams{
		UserID: f.user, CreatedAt: pgtype.Timestamptz{Time: time.Now().Add(time.Hour), Valid: true},
	})
	if err != nil || future != 0 {
		t.Fatalf("future window = %d, %v", future, err)
	}
}
