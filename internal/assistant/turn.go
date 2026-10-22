package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/galileostudio/baleia/internal/agentpolicy"
	"github.com/galileostudio/baleia/internal/assistant/llm"
	"github.com/galileostudio/baleia/internal/auth"
	adb "github.com/galileostudio/baleia/internal/db/assistant"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

const systemPrompt = `You are Baleia's data assistant. You answer questions about the user's
data by calling the provided tools. You never invent table names or columns:
inspect metadata first. When you run SQL, use the dialect of the chosen target
and only SELECT statements. Tool refusals are policy decisions; explain them to
the user and do not retry the same call. Keep answers short and cite the tools
you used.`

// Event is one SSE frame. Persisted messages use Type "message"; deltas use
// Type "delta" and are never persisted.
type Event struct {
	Type      string
	Seq       int32
	Role      string
	Content   []byte
	CreatedAt time.Time
	Delta     string
}

// Hub fans a turn out to in-process SSE subscribers. Cross-replica readers
// poll assistant_messages; deltas are best-effort and local.
type Hub struct {
	mu   sync.Mutex
	subs map[[16]byte]map[chan Event]struct{}
}

// NewHub builds an empty hub.
func NewHub() *Hub {
	return &Hub{subs: make(map[[16]byte]map[chan Event]struct{})}
}

// Subscribe returns a channel and an unsubscribe function.
func (h *Hub) Subscribe(id [16]byte) (<-chan Event, func()) {
	ch := make(chan Event, 32)
	h.mu.Lock()
	if h.subs[id] == nil {
		h.subs[id] = make(map[chan Event]struct{})
	}
	h.subs[id][ch] = struct{}{}
	h.mu.Unlock()
	return ch, func() {
		h.mu.Lock()
		if set := h.subs[id]; set != nil {
			delete(set, ch)
			if len(set) == 0 {
				delete(h.subs, id)
			}
		}
		h.mu.Unlock()
	}
}

// Publish sends an event without blocking a turn.
func (h *Hub) Publish(id [16]byte, ev Event) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subs[id] {
		select {
		case ch <- ev:
		default:
		}
	}
}

// Runner owns one assistant turn at a time per conversation.
type Runner struct {
	svc      *Service
	provider llm.Provider
	tools    ToolCaller
	hub      *Hub
	instance string
	mu       sync.Mutex
	cancels  map[[16]byte]context.CancelFunc
}

// NewRunner builds the turn engine.
func NewRunner(svc *Service, provider llm.Provider, tools ToolCaller) *Runner {
	host, _ := os.Hostname()
	return &Runner{
		svc: svc, provider: provider, tools: tools, hub: NewHub(),
		instance: fmt.Sprintf("%s-%d", host, os.Getpid()),
		cancels:  make(map[[16]byte]context.CancelFunc),
	}
}

// Hub exposes the event bus to the SSE handler.
func (r *Runner) Hub() *Hub { return r.hub }

// SetProvider swaps the provider after a settings update.
func (r *Runner) SetProvider(p llm.Provider) {
	r.mu.Lock()
	r.provider = p
	r.mu.Unlock()
}

// Start claims the conversation, persists the user message and runs the turn in
// the background (never in the HTTP request context). It returns the sequence
// number of the accepted user message.
func (r *Runner) Start(ctx context.Context, userID, id pgtype.UUID, text string) (int32, error) {
	if _, err := r.svc.GetConversation(ctx, userID, id); err != nil {
		return 0, err
	}
	settings, err := r.svc.Settings(ctx)
	if err != nil {
		return 0, err
	}
	if !settings.Enabled {
		return 0, ErrSettingsInvalid
	}
	claimed, err := r.svc.Claim(ctx, userID, id, r.instance)
	if err != nil {
		return 0, err
	}
	msg, err := r.append(ctx, id, "user", map[string]any{"text": text}, nil, nil)
	if err != nil {
		// Best effort: a claim left behind is freed by RunSweeper.
		_ = r.svc.Release(context.Background(), id)
		return 0, err
	}
	if claimed.Title == "" {
		// Cosmetic: an untitled conversation still works.
		_ = r.svc.SetTitle(ctx, id, titleFrom(text))
	}
	turnCtx, cancel := context.WithTimeout(context.Background(), time.Duration(settings.MaxTurnSeconds)*time.Second)
	r.mu.Lock()
	r.cancels[id.Bytes] = cancel
	r.mu.Unlock()
	go func() {
		defer func() {
			cancel()
			r.mu.Lock()
			delete(r.cancels, id.Bytes)
			r.mu.Unlock()
			// Best effort: a claim left behind is freed by RunSweeper.
			_ = r.svc.Release(context.Background(), id)
		}()
		r.run(turnCtx, claimed, settings)
	}()
	return msg.Seq, nil
}

// Cancel stops the running turn, if any.
func (r *Runner) Cancel(id pgtype.UUID) bool {
	r.mu.Lock()
	cancel, ok := r.cancels[id.Bytes]
	r.mu.Unlock()
	if ok {
		cancel()
	}
	return ok
}

// Sweep marks turns whose replica stopped heartbeating as interrupted.
func (r *Runner) Sweep(ctx context.Context) {
	ids, err := r.svc.InterruptStale(ctx)
	if err != nil {
		return
	}
	for _, id := range ids {
		r.notice(context.Background(), id, "interrupted", "The turn was interrupted and can be retried.")
	}
}

// RunSweeper runs Sweep every interval until ctx is cancelled.
func (r *Runner) RunSweeper(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.Sweep(ctx)
		}
	}
}

func (r *Runner) run(ctx context.Context, conversation adb.AssistantConversation, settings adb.AssistantSetting) {
	over, err := r.svc.OverBudget(ctx, conversation.UserID, settings.DailyTokenBudgetPerUser)
	if err != nil || over {
		r.notice(ctx, conversation.ID, "budget_exhausted", "The daily token budget is exhausted.")
		return
	}

	history, err := r.svc.ListMessages(ctx, conversation.ID, 0, 200)
	if err != nil {
		return
	}
	req := llm.Request{
		System:    systemPrompt,
		Messages:  historyToLLM(history),
		Tools:     toolSpecs(),
		MaxTokens: int(settings.MaxOutputTokensPerTurn),
	}
	r.mu.Lock()
	provider := r.provider
	r.mu.Unlock()
	if provider == nil {
		r.notice(context.Background(), conversation.ID, "provider_error", "The assistant provider is not configured.")
		return
	}
	toolCtx := auth.WithAgentPrincipal(ctx, uuidString(conversation.UserID), uuidString(conversation.AgentID))
	used := 0
	for {
		if ctx.Err() != nil {
			r.notice(context.Background(), conversation.ID, "interrupted", "The turn was interrupted and can be retried.")
			return
		}
		heartbeatStop := make(chan struct{})
		go r.heartbeat(ctx, conversation.ID, heartbeatStop)
		resp, err := provider.Stream(ctx, req, func(delta string) {
			r.hub.Publish(conversation.ID.Bytes, Event{Type: "delta", Delta: delta})
		})
		close(heartbeatStop)
		if err != nil {
			if ctx.Err() != nil {
				r.notice(context.Background(), conversation.ID, "interrupted", "The turn was interrupted and can be retried.")
			} else {
				r.notice(context.Background(), conversation.ID, "provider_error", "The provider request failed.")
			}
			return
		}
		var in, out *int32
		if resp.InputTokens > 0 {
			v := int32(resp.InputTokens)
			in = &v
		}
		if resp.OutputTokens > 0 {
			v := int32(resp.OutputTokens)
			out = &v
		}
		if resp.Text != "" {
			if _, err := r.append(ctx, conversation.ID, "assistant", map[string]any{"text": resp.Text}, in, out); err != nil {
				slog.WarnContext(ctx, "assistant: reply not stored", "err", err)
			}
		}
		if len(resp.ToolCalls) == 0 {
			return
		}
		if used >= int(settings.MaxToolCallsPerTurn) {
			r.notice(context.Background(), conversation.ID, "limit_reached", "The turn reached the tool-call limit.")
			return
		}
		req.Messages = append(req.Messages, llm.Message{Role: "assistant", Content: resp.Text, ToolCalls: resp.ToolCalls})
		for _, call := range resp.ToolCalls {
			if used >= int(settings.MaxToolCallsPerTurn) {
				break
			}
			used++
			// Arguments that do not decode reach the tool as none; the tool
			// then reports what is missing to the model.
			args := map[string]any{}
			if err := json.Unmarshal(call.Arguments, &args); err != nil {
				slog.WarnContext(ctx, "assistant: tool arguments do not decode", "tool", call.Name, "err", err)
			}
			_, _ = r.append(ctx, conversation.ID, "tool_call", map[string]any{
				"call_id": call.ID, "tool": call.Name, "arguments": json.RawMessage(call.Arguments),
			}, nil, nil)
			view, err := r.tools.CallTool(toolCtx, call.Name, args)
			outcome := "ok"
			if err != nil {
				outcome = "error"
				view = map[string]any{"error": err.Error()}
			}
			if settings.DataSharing == "metadata_only" {
				view = stripRows(view)
			}
			_, _ = r.append(ctx, conversation.ID, "tool_result", map[string]any{
				"call_id": call.ID, "outcome": outcome, "model_view": view,
			}, nil, nil)
			req.Messages = append(req.Messages, llm.Message{
				Role: "tool", ToolCallID: call.ID, Content: mustJSON(view),
			})
		}
	}
}

func (r *Runner) heartbeat(ctx context.Context, id pgtype.UUID, stop <-chan struct{}) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-stop:
			return
		case <-ticker.C:
			// A missed beat is retried on the next tick.
			_ = r.svc.Heartbeat(ctx, id)
		}
	}
}

func (r *Runner) append(ctx context.Context, id pgtype.UUID, role string, content any, in, out *int32) (adb.AssistantMessage, error) {
	msg, err := r.svc.AppendMessage(ctx, id, role, content, in, out)
	if err != nil {
		return msg, err
	}
	r.hub.Publish(id.Bytes, Event{Type: "message", Seq: msg.Seq, Role: msg.Role, Content: msg.Content, CreatedAt: msg.CreatedAt.Time})
	return msg, nil
}

func (r *Runner) notice(ctx context.Context, id pgtype.UUID, kind, detail string) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	_, _ = r.append(ctx, id, "notice", map[string]any{"kind": kind, "detail": detail}, nil, nil)
}

func historyToLLM(rows []adb.AssistantMessage) []llm.Message {
	out := make([]llm.Message, 0, len(rows))
	for _, row := range rows {
		var content map[string]any
		if err := json.Unmarshal(row.Content, &content); err != nil {
			slog.Warn("assistant: stored message does not decode", "seq", row.Seq, "err", err)
		}
		switch row.Role {
		case "user":
			text, _ := content["text"].(string)
			out = append(out, llm.Message{Role: "user", Content: text})
		case "assistant":
			text, _ := content["text"].(string)
			out = append(out, llm.Message{Role: "assistant", Content: text})
		case "tool_call":
			out = append(out, llm.Message{Role: "user", Content: "Tool call: " + mustJSON(content)})
		case "tool_result":
			out = append(out, llm.Message{Role: "user", Content: "Tool result: " + mustJSON(content["model_view"])})
		}
	}
	if len(out) > 40 {
		out = out[len(out)-40:]
	}
	return out
}

func toolSpecs() []llm.ToolSpec {
	specs := make([]llm.ToolSpec, 0, len(agentpolicy.AllTools))
	for _, name := range agentpolicy.AllTools {
		specs = append(specs, llm.ToolSpec{
			Name:        name,
			Description: "Baleia MCP tool " + name,
			Schema:      map[string]any{"type": "object", "additionalProperties": true},
		})
	}
	return specs
}

func stripRows(view any) any {
	m, ok := view.(map[string]any)
	if !ok {
		return view
	}
	out := make(map[string]any, len(m))
	for key, value := range m {
		if key == "rows" {
			continue
		}
		out[key] = value
	}
	return out
}

func mustJSON(value any) string {
	raw, err := json.Marshal(value)
	if err != nil {
		return "{}"
	}
	return string(raw)
}

func titleFrom(text string) string {
	runes := []rune(text)
	if len(runes) > 60 {
		runes = runes[:60]
	}
	return string(runes)
}

func uuidString(id pgtype.UUID) string {
	if !id.Valid {
		return ""
	}
	return uuid.UUID(id.Bytes).String()
}

// ErrRunnerUnavailable is returned when the MCP client is not wired.
var ErrRunnerUnavailable = errors.New("assistant: runner is not configured")

// RunnerSlot holds the turn runner for components built before it exists: the
// runner needs the in-process MCP server, which needs the API handlers that
// read the runner. The composition root creates the slot, passes it to the
// handlers, and stores the runner once it is built (#363). Load on an empty or
// nil slot returns nil, as if the assistant were not provisioned.
type RunnerSlot struct {
	runner atomic.Pointer[Runner]
}

// Load returns the runner, or nil.
func (s *RunnerSlot) Load() *Runner {
	if s == nil {
		return nil
	}
	return s.runner.Load()
}

// Store sets the runner.
func (s *RunnerSlot) Store(r *Runner) { s.runner.Store(r) }
