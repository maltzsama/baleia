package offload

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/audit/export"
	"github.com/galileostudio/baleia/internal/store/model"
)

type fakeQueryHistoryStore struct {
	execs   []model.QueryExecution
	stmts   map[string][]model.QueryStatement
	pending bool
}

func (f *fakeQueryHistoryStore) OffloadQueryExecutions(_ context.Context, afterAt time.Time, afterID string, before time.Time, limit int) ([]model.QueryExecution, error) {
	var out []model.QueryExecution
	for _, e := range f.execs {
		if !e.StartedAt.Before(before) {
			continue
		}
		if !afterAt.IsZero() {
			if afterID != "" {
				if e.StartedAt.Before(afterAt) || (e.StartedAt.Equal(afterAt) && string(e.ID) <= afterID) {
					continue
				}
			} else if e.StartedAt.Before(afterAt) {
				continue
			}
		}
		out = append(out, e)
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (f *fakeQueryHistoryStore) OffloadQueryStatements(_ context.Context, ids []model.UUID, _, _ time.Time, _ int) ([]model.QueryStatement, bool, error) {
	var out []model.QueryStatement
	for _, id := range ids {
		out = append(out, f.stmts[string(id)]...)
	}
	return out, false, nil
}

func (f *fakeQueryHistoryStore) PendingExecutions(context.Context, time.Time, time.Time) (bool, error) {
	return f.pending, nil
}

func TestQueryHistoryReaderWritesPair(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	now := time.Date(2027, 1, 15, 0, 0, 0, 0, time.UTC)
	oct := time.Date(2026, 10, 5, 10, 0, 0, 0, time.UTC)

	e1 := model.QueryExecution{ID: "00000000-0000-0000-0000-000000000001", WorkspaceID: "w1", UserID: "u1", Status: "finished", Origin: "editor", SQL: "select 1", StartedAt: oct}
	e2 := model.QueryExecution{ID: "00000000-0000-0000-0000-000000000002", WorkspaceID: "w1", UserID: "u1", Status: "failed", Origin: "editor", SQL: "select 2", StartedAt: oct.Add(time.Hour)}
	stmts := map[string][]model.QueryStatement{
		string(e1.ID): {{ID: "s1", ExecutionID: e1.ID, ExecutionStartedAt: oct, Position: 1, SQL: "select 1", Status: "finished", StartedAt: oct}},
		string(e2.ID): {{ID: "s2", ExecutionID: e2.ID, ExecutionStartedAt: oct, Position: 1, SQL: "select 2", Status: "failed", StartedAt: oct}},
	}
	store := &fakeQueryHistoryStore{execs: []model.QueryExecution{e1, e2}, stmts: stmts}
	objects := &fakeStore{objects: map[string][]byte{}, types: map[string]string{}}
	cur := &fakeCursor{}
	sink := NewSink(objects, "baleia-cold", export.StreamQueryHistory)

	o := &Offloader{
		Stream: export.StreamQueryHistory, SinkName: export.SinkS3,
		Cursor: cur, Sink: sink,
		Reader: &QueryHistoryReader{Store: store},
		Now:    func() time.Time { return now },
	}
	n, err := o.Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if n != 4 { // 2 executions + 2 statements
		t.Fatalf("offloaded = %d, want 4", n)
	}

	execKey := sink.ChunkKey(oct, ExecutionsObject, export.Position{OccurredAt: oct, Key: string(e1.ID)})
	stmtKey := sink.ChunkKey(oct, StatementsObject, statementPos(stmts[string(e1.ID)][0]))
	if _, ok := objects.objects[execKey]; !ok {
		t.Fatalf("executions object missing")
	}
	if got := gunzip(t, objects.objects[execKey]); got == "" {
		t.Fatalf("executions object empty")
	}
	var m Manifest
	if err := json.Unmarshal(objects.objects[sink.ManifestKey(oct)], &m); err != nil {
		t.Fatalf("manifest: %v", err)
	}
	if len(m.Keys) != 2 || m.Counts[execKey] != 2 || m.Counts[stmtKey] != 2 {
		t.Fatalf("manifest = %+v", m)
	}
	if !cur.pos.OccurredAt.Equal(time.Date(2027, 1, 14, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("cursor = %+v", cur.pos)
	}
}

// TestQueryHistoryReaderWaitsForFinal: a day with a queued or running
// execution is not sealed; Run returns 0, writes nothing and leaves the cursor
// untouched so the next pass retries.
func TestQueryHistoryReaderWaitsForFinal(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	now := time.Date(2027, 1, 15, 0, 0, 0, 0, time.UTC)
	oct := time.Date(2026, 10, 5, 10, 0, 0, 0, time.UTC)
	e := model.QueryExecution{ID: "00000000-0000-0000-0000-0000000000aa", WorkspaceID: "w", UserID: "u", Status: "running", Origin: "editor", SQL: "select 1", StartedAt: oct}
	store := &fakeQueryHistoryStore{execs: []model.QueryExecution{e}, pending: true}
	objects := &fakeStore{objects: map[string][]byte{}, types: map[string]string{}}
	cur := &fakeCursor{}
	sink := NewSink(objects, "baleia-cold", export.StreamQueryHistory)
	o := &Offloader{
		Stream: export.StreamQueryHistory, SinkName: export.SinkS3,
		Cursor: cur, Sink: sink, Reader: &QueryHistoryReader{Store: store},
		Lag: QueryHistorySafetyLag, Now: func() time.Time { return now },
	}
	n, err := o.Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if n != 0 {
		t.Fatalf("offloaded = %d, want 0 (not quiescent)", n)
	}
	if len(objects.objects) != 0 {
		t.Fatalf("objects = %d, want none", len(objects.objects))
	}
	if cur.pos.After() {
		t.Fatalf("cursor advanced to %+v", cur.pos)
	}
}
