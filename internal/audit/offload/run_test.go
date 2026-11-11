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
		Source: src, Cursor: cur, Sink: sink,
		Document: func(e export.Event) map[string]any { return map[string]any{"id": e.ID} },
		Now:      func() time.Time { return now },
	}
	n, err := o.Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if n != 3 {
		t.Fatalf("offloaded = %d, want 3", n)
	}

	// Two months, two objects, two manifests.
	if len(store.objects) != 4 { // 2 objects + 2 manifests
		t.Fatalf("objects = %d, want 4", len(store.objects))
	}
	if got := gunzip(t, store.objects[sink.ObjectKey(nov)]); got != `{"id":1}`+"\n"+`{"id":2}`+"\n" {
		t.Fatalf("november object = %q", got)
	}
	if got := gunzip(t, store.objects[sink.ObjectKey(dec)]); got != `{"id":3}`+"\n" {
		t.Fatalf("december object = %q", got)
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

	src := &fakeSource{events: []export.Event{{OccurredAt: dec, ID: 1, Action: "x"}}}
	cur := &fakeCursor{pos: export.Position{OccurredAt: time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)}}
	store := &fakeStore{objects: map[string][]byte{}, types: map[string]string{}}
	sink := NewSink(store, "baleia-cold", "audit")

	o := &Offloader{
		Stream: export.StreamAudit, SinkName: export.SinkS3,
		Source: src, Cursor: cur, Sink: sink,
		Document: func(e export.Event) map[string]any { return map[string]any{"id": e.ID} },
		Now:      func() time.Time { return now },
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
}
