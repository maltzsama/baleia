package offload

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/audit/export"
)

type fakeSource struct {
	events []export.Event
}

func (s *fakeSource) Events(_ context.Context, after export.Position, before time.Time, limit int) ([]export.Event, error) {
	var out []export.Event
	for _, e := range s.events {
		if e.OccurredAt.After(after.OccurredAt) || (e.OccurredAt.Equal(after.OccurredAt) && e.ID > after.ID) {
			if e.OccurredAt.Before(before) {
				out = append(out, e)
			}
		}
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

type fakeCursor struct {
	pos export.Position
}

func (c *fakeCursor) Get(context.Context, string, string) (export.Position, error) { return c.pos, nil }
func (c *fakeCursor) Set(_ context.Context, _, _ string, p export.Position) error {
	c.pos = p
	return nil
}

func TestOffloaderMonths(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	now := time.Date(2027, 1, 15, 0, 0, 0, 0, time.UTC)

	nov := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	dec := time.Date(2026, 12, 10, 12, 0, 0, 0, time.UTC)

	src := &fakeSource{events: []export.Event{
		{OccurredAt: nov, ID: 1, Action: "a"},
		{OccurredAt: nov.Add(time.Hour), ID: 2, Action: "b"},
		{OccurredAt: dec, ID: 3, Action: "c"},
	}}
	cur := &fakeCursor{}
	store := &fakeStore{objects: map[string][]byte{}, types: map[string]string{}}
	sink := NewSink(store, "baleia-cold", "audit")

	o := &Offloader{
		Stream: export.StreamAudit, SinkName: export.SinkS3,
		Cursor: cur, Sink: sink,
		Reader: &AuditReader{Source: src, Name: export.StreamAudit, Document: func(e export.Event) map[string]any { return map[string]any{"id": e.ID} }},
		Now:    func() time.Time { return now },
	}
	n, err := o.Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if n != 3 {
		t.Fatalf("offloaded = %d, want 3", n)
	}

	// Two months, two chunks, two manifests.
	if len(store.objects) != 4 { // 2 chunks + 2 manifests
		t.Fatalf("objects = %d, want 4", len(store.objects))
	}
	novKey := sink.ChunkKey(nov, export.StreamAudit, export.Position{OccurredAt: nov, ID: 1})
	if got := gunzip(t, store.objects[novKey]); got != `{"id":1}`+"\n"+`{"id":2}`+"\n" {
		t.Fatalf("november chunk = %q", got)
	}
	decKey := sink.ChunkKey(dec, export.StreamAudit, export.Position{OccurredAt: dec, ID: 3})
	if got := gunzip(t, store.objects[decKey]); got != `{"id":3}`+"\n" {
		t.Fatalf("december chunk = %q", got)
	}

	// The cursor advanced to the start of the month after December, so the
	// retention gate sees December (and earlier) as fully offloaded.
	if !cur.pos.OccurredAt.Equal(time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)) || cur.pos.ID != 0 {
		t.Fatalf("cursor = %+v", cur.pos)
	}
}

func TestOffloaderEmptyMonthAdvances(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	now := time.Date(2027, 1, 15, 0, 0, 0, 0, time.UTC)
	dec := time.Date(2026, 12, 10, 12, 0, 0, 0, time.UTC)
	nov := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)

	src := &fakeSource{events: []export.Event{{OccurredAt: dec, ID: 1, Action: "x"}}}
	cur := &fakeCursor{pos: export.Position{OccurredAt: nov}}
	store := &fakeStore{objects: map[string][]byte{}, types: map[string]string{}}
	sink := NewSink(store, "baleia-cold", "audit")

	o := &Offloader{
		Stream: export.StreamAudit, SinkName: export.SinkS3,
		Cursor: cur, Sink: sink,
		Reader: &AuditReader{Source: src, Name: export.StreamAudit, Document: func(e export.Event) map[string]any { return map[string]any{"id": e.ID} }},
		Now:    func() time.Time { return now },
	}
	if _, err := o.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}
	// November had no rows (the cursor started there), December had one, and
	// the cursor stopped at the start of January.
	if !cur.pos.OccurredAt.Equal(time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)) || cur.pos.ID != 0 {
		t.Fatalf("cursor = %+v", cur.pos)
	}
	var m Manifest
	if err := json.Unmarshal(store.objects[sink.ManifestKey(dec)], &m); err != nil {
		t.Fatalf("december manifest: %v", err)
	}
	if m.Month != "2026-12" {
		t.Fatalf("manifest month = %q", m.Month)
	}
	// November had no rows but is fully read, so it still gets a manifest and
	// its (empty) partition stays prunable.
	if _, ok := store.objects[sink.ManifestKey(nov)]; !ok {
		t.Fatalf("november manifest missing for the empty month")
	}
}

// TestOffloaderChunksLargeMonth: a month larger than ChunkRows is written as
// several chunks, all recorded in the month's manifest, instead of one object.
//
//nolint:paralleltest // mutates the package-level ChunkRows
func TestOffloaderChunksLargeMonth(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 12, 15, 0, 0, 0, 0, time.UTC)
	base := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)

	src := &fakeSource{events: make([]export.Event, 5)}
	for i := range src.events {
		src.events[i] = export.Event{OccurredAt: base.Add(time.Duration(i) * time.Minute), ID: int64(i + 1), Action: "x"}
	}

	old := ChunkRows
	ChunkRows = 2
	defer func() { ChunkRows = old }()

	cur := &fakeCursor{}
	store := &fakeStore{objects: map[string][]byte{}, types: map[string]string{}}
	sink := NewSink(store, "baleia-cold", "audit")
	o := &Offloader{
		Stream: export.StreamAudit, SinkName: export.SinkS3,
		Cursor: cur, Sink: sink,
		Reader: &AuditReader{Source: src, Name: export.StreamAudit, Document: func(e export.Event) map[string]any { return map[string]any{"id": e.ID} }},
		Now:    func() time.Time { return now },
	}
	n, err := o.Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if n != 5 {
		t.Fatalf("offloaded = %d, want 5", n)
	}
	var m Manifest
	if err := json.Unmarshal(store.objects[sink.ManifestKey(base)], &m); err != nil {
		t.Fatalf("manifest: %v", err)
	}
	if len(m.Keys) != 3 {
		t.Fatalf("chunks = %d, want 3", len(m.Keys))
	}
	var total int64
	for k, c := range m.Counts {
		total += c
		if m.Checksums[k] == "" {
			t.Fatalf("missing checksum for %s", k)
		}
	}
	if total != 5 {
		t.Fatalf("total rows = %d, want 5", total)
	}
}

// TestOffloaderResumesPartialMonth: a month with a partial manifest and a
// mid-month cursor resumes from the cursor and merges the new chunks into the
// manifest, instead of re-reading the whole month.
//
//nolint:paralleltest // mutates the package-level ChunkRows
func TestOffloaderResumesPartialMonth(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 12, 15, 0, 0, 0, 0, time.UTC)
	base := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)

	src := &fakeSource{events: make([]export.Event, 5)}
	for i := range src.events {
		src.events[i] = export.Event{OccurredAt: base.Add(time.Duration(i) * time.Minute), ID: int64(i + 1), Action: "x"}
	}

	old := ChunkRows
	ChunkRows = 2
	defer func() { ChunkRows = old }()

	cur := &fakeCursor{}
	store := &fakeStore{objects: map[string][]byte{}, types: map[string]string{}}
	sink := NewSink(store, "baleia-cold", "audit")

	// Simulate a prior partial pass: one chunk (events 1 and 2) written and the
	// cursor advanced past event 2.
	first := export.Position{OccurredAt: base, ID: 1}
	key1 := sink.ChunkKey(base, export.StreamAudit, first)
	count, sum, err := sink.WriteChunk(ctx, key1, []json.RawMessage{json.RawMessage(`{"id":1}`), json.RawMessage(`{"id":2}`)})
	if err != nil {
		t.Fatalf("WriteChunk: %v", err)
	}
	m := Manifest{Stream: export.StreamAudit, Month: base.Format("2006-01"), Counts: map[string]int64{}, Checksums: map[string]string{}}
	m.add(key1, count, sum)
	if err := sink.WriteManifest(ctx, base, m); err != nil {
		t.Fatalf("WriteManifest: %v", err)
	}
	cur.pos = export.Position{OccurredAt: base.Add(time.Minute), ID: 2}

	o := &Offloader{
		Stream: export.StreamAudit, SinkName: export.SinkS3,
		Cursor: cur, Sink: sink,
		Reader: &AuditReader{Source: src, Name: export.StreamAudit, Document: func(e export.Event) map[string]any { return map[string]any{"id": e.ID} }},
		Now:    func() time.Time { return now },
	}
	n, err := o.Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if n != 3 { // events 3, 4, 5
		t.Fatalf("offloaded = %d, want 3", n)
	}
	var got Manifest
	if err := json.Unmarshal(store.objects[sink.ManifestKey(base)], &got); err != nil {
		t.Fatalf("manifest: %v", err)
	}
	// The prior chunk plus two new chunks (events 3-4 and 5).
	if len(got.Keys) != 3 {
		t.Fatalf("chunks = %d, want 3", len(got.Keys))
	}
	var total int64
	for _, c := range got.Counts {
		total += c
	}
	if total != 5 {
		t.Fatalf("total rows = %d, want 5", total)
	}
}
