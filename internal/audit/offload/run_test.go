package offload

import (
	"context"
	"encoding/json"
	"strings"
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

func TestOffloaderDays(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	now := time.Date(2026, 12, 13, 0, 0, 0, 0, time.UTC)

	d10 := time.Date(2026, 12, 10, 12, 0, 0, 0, time.UTC)
	d11 := time.Date(2026, 12, 11, 8, 0, 0, 0, time.UTC)

	src := &fakeSource{events: []export.Event{
		{OccurredAt: d10, ID: 1, Action: "a"},
		{OccurredAt: d10.Add(time.Hour), ID: 2, Action: "b"},
		{OccurredAt: d11, ID: 3, Action: "c"},
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

	// Two days, two chunks, two manifests.
	if len(store.objects) != 4 { // 2 chunks + 2 manifests
		t.Fatalf("objects = %d, want 4", len(store.objects))
	}
	k10 := sink.ChunkKey(d10, export.StreamAudit, export.Position{OccurredAt: d10, ID: 1})
	if got := gunzip(t, store.objects[k10]); got != `{"id":1}`+"\n"+`{"id":2}`+"\n" {
		t.Fatalf("Dec 10 chunk = %q", got)
	}
	if want := "baleia-cold/audit/2026/12/10/audit-20261210-"; !strings.HasPrefix(k10, want) {
		t.Fatalf("Dec 10 chunk key = %q, want prefix %q", k10, want)
	}
	k11 := sink.ChunkKey(d11, export.StreamAudit, export.Position{OccurredAt: d11, ID: 3})
	if got := gunzip(t, store.objects[k11]); got != `{"id":3}`+"\n" {
		t.Fatalf("Dec 11 chunk = %q", got)
	}

	// The cursor advanced to the start of the day after the last sealed day, so
	// the retention gate sees Dec 11 (and earlier) as fully offloaded. Dec 12 is
	// not sealed yet: it only ends inside the safety lag.
	if !cur.pos.OccurredAt.Equal(time.Date(2026, 12, 12, 0, 0, 0, 0, time.UTC)) || cur.pos.ID != 0 {
		t.Fatalf("cursor = %+v", cur.pos)
	}
	for _, day := range []time.Time{d10, d11} {
		if reason, err := sink.Verify(ctx, day); err != nil || reason != "" {
			t.Fatalf("Verify(%s) = %q, %v; want a valid manifest", day.Format("2006-01-02"), reason, err)
		}
	}
	if _, ok := store.objects[sink.ManifestKey(time.Date(2026, 12, 12, 0, 0, 0, 0, time.UTC))]; ok {
		t.Fatalf("Dec 12 must not be sealed yet")
	}
}

func TestOffloaderEmptyDayAdvances(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	now := time.Date(2026, 12, 13, 0, 0, 0, 0, time.UTC)
	d11 := time.Date(2026, 12, 11, 12, 0, 0, 0, time.UTC)
	d9 := time.Date(2026, 12, 9, 0, 0, 0, 0, time.UTC)

	src := &fakeSource{events: []export.Event{{OccurredAt: d11, ID: 1, Action: "x"}}}
	cur := &fakeCursor{pos: export.Position{OccurredAt: d9}}
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
	// Dec 9 and Dec 10 had no rows (the cursor started on Dec 9), Dec 11 had
	// one, and the cursor stopped at the start of Dec 12.
	if !cur.pos.OccurredAt.Equal(time.Date(2026, 12, 12, 0, 0, 0, 0, time.UTC)) || cur.pos.ID != 0 {
		t.Fatalf("cursor = %+v", cur.pos)
	}
	var m Manifest
	if err := json.Unmarshal(store.objects[sink.ManifestKey(d11)], &m); err != nil {
		t.Fatalf("Dec 11 manifest: %v", err)
	}
	if m.Day != "2026-12-11" {
		t.Fatalf("manifest day = %q", m.Day)
	}
	// The empty days are fully read, so each still gets a manifest and its
	// (empty) partition stays droppable, and a manifest with no objects verifies.
	for _, day := range []time.Time{d9, d9.AddDate(0, 0, 1)} {
		if _, ok := store.objects[sink.ManifestKey(day)]; !ok {
			t.Fatalf("manifest missing for the empty day %s", day.Format("2006-01-02"))
		}
		if reason, err := sink.Verify(ctx, day); err != nil || reason != "" {
			t.Fatalf("Verify(empty day) = %q, %v", reason, err)
		}
	}
}

// TestOffloaderChunksLargeDay: a day larger than ChunkRows is written as
// several chunks, all recorded in the day's manifest, instead of one object.
//
//nolint:paralleltest // mutates the package-level ChunkRows
func TestOffloaderChunksLargeDay(t *testing.T) {
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

// TestOffloaderResumesPartialDay: a day with a partial manifest and a mid-day
// cursor resumes from the cursor and merges the new chunks into the manifest,
// instead of re-reading the whole day.
//
//nolint:paralleltest // mutates the package-level ChunkRows
func TestOffloaderResumesPartialDay(t *testing.T) {
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
	m := Manifest{Stream: export.StreamAudit, Day: base.Format("2006-01-02"), Counts: map[string]int64{}, Checksums: map[string]string{}}
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
