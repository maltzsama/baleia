package offload

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/audit/export"
)

// erroringStore fails every call, as an unreachable cold store would.
type erroringStore struct{ *fakeStore }

func (erroringStore) Has(context.Context, string) (bool, error) { return false, errors.New("s3 down") }

// sealedDay writes one chunk of two rows and its manifest for day, as the
// offloader does, and returns the sink, the store and the chunk key.
func sealedDay(ctx context.Context, t *testing.T, day time.Time) (*Sink, *fakeStore, string) {
	t.Helper()
	store := &fakeStore{objects: map[string][]byte{}, types: map[string]string{}}
	sink := NewSink(store, "baleia-cold", "audit")
	key := sink.ChunkKey(day, "audit", export.Position{OccurredAt: day, ID: 1})
	count, sum, err := sink.WriteChunk(ctx, key, []json.RawMessage{json.RawMessage(`{"id":1}`), json.RawMessage(`{"id":2}`)})
	if err != nil {
		t.Fatal(err)
	}
	m := Manifest{Stream: "audit", Day: day.UTC().Format("2006-01-02"), Counts: map[string]int64{}, Checksums: map[string]string{}}
	m.add(key, count, sum)
	if err := sink.WriteManifest(ctx, day, m); err != nil {
		t.Fatal(err)
	}
	return sink, store, key
}

func TestSinkVerify(t *testing.T) {
	t.Parallel()
	day := time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC)
	ctx := context.Background()

	t.Run("a sealed day verifies", func(t *testing.T) {
		t.Parallel()
		sink, _, _ := sealedDay(ctx, t, day)
		if reason, err := sink.Verify(ctx, day); err != nil || reason != "" {
			t.Fatalf("Verify = %q, %v; want valid", reason, err)
		}
	})
	t.Run("a day with no manifest", func(t *testing.T) {
		t.Parallel()
		sink, _, _ := sealedDay(ctx, t, day)
		if reason, err := sink.Verify(ctx, day.AddDate(0, 0, 1)); err != nil || reason != "manifest missing" {
			t.Fatalf("Verify = %q, %v; want manifest missing", reason, err)
		}
	})
	for _, tc := range []struct {
		name   string
		mutate func(store *fakeStore, sink *Sink, key string)
		want   string
	}{
		{"object deleted", func(s *fakeStore, _ *Sink, key string) { delete(s.objects, key) }, "is missing"},
		{"object corrupted", func(s *fakeStore, _ *Sink, key string) {
			s.objects[key] = append([]byte{}, s.objects[key][:len(s.objects[key])-4]...)
		}, "checksum"},
		{"object replaced by another with the same checksum recorded", func(s *fakeStore, sink *Sink, key string) {
			// A different object under the key, and a manifest that records
			// its checksum but a wrong row count.
			data, _, _, _ := buildObject([]json.RawMessage{json.RawMessage(`{"id":9}`)})
			s.objects[key] = data
			m, _ := sink.ReadManifest(ctx, day)
			m.Checksums[key] = sha256Hex(data)
			_ = sink.WriteManifest(ctx, day, m)
		}, "has 1 rows, manifest says 2"},
		{"manifest is garbage", func(s *fakeStore, sink *Sink, _ string) { s.objects[sink.ManifestKey(day)] = []byte("{") }, "manifest unreadable"},
		{"manifest names another day", func(s *fakeStore, sink *Sink, _ string) {
			m, _ := sink.ReadManifest(ctx, day)
			m.Day = "2026-11-16"
			_ = sink.WriteManifest(ctx, day, m)
		}, "manifest names stream"},
		{"manifest names another stream", func(s *fakeStore, sink *Sink, _ string) {
			m, _ := sink.ReadManifest(ctx, day)
			m.Stream = "query_history"
			_ = sink.WriteManifest(ctx, day, m)
		}, "manifest names stream"},
		{"manifest repeats a key and hides another object", func(s *fakeStore, sink *Sink, key string) {
			// Keys [k, k] with counts and checksums for k and an object that is
			// not in the store: every length agrees, yet one object is never
			// checked unless the key set must match the maps exactly.
			m, _ := sink.ReadManifest(ctx, day)
			m.Keys = []string{key, key}
			m.Counts["ghost"] = 1
			m.Checksums["ghost"] = "00"
			delete(m.Counts, key)
			delete(m.Checksums, key)
			m.Counts[key], m.Checksums[key] = 2, sha256Hex(s.objects[key])
			_ = sink.WriteManifest(ctx, day, m)
		}, "repeats a key"},
		{"manifest has a count for an object it does not list", func(s *fakeStore, sink *Sink, key string) {
			m, _ := sink.ReadManifest(ctx, day)
			m.Counts["ghost"] = 1
			m.Keys = append(m.Keys, "other")
			m.Checksums["other"] = "00"
			_ = sink.WriteManifest(ctx, day, m)
		}, "do not match its objects"},
		{"manifest lists an object it has no checksum for", func(s *fakeStore, sink *Sink, key string) {
			m, _ := sink.ReadManifest(ctx, day)
			delete(m.Checksums, key)
			_ = sink.WriteManifest(ctx, day, m)
		}, "do not match its objects"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			sink, store, key := sealedDay(ctx, t, day)
			tc.mutate(store, sink, key)
			reason, err := sink.Verify(ctx, day)
			if err != nil {
				t.Fatalf("Verify error = %v; a damaged day is a reason, not an error", err)
			}
			if !strings.Contains(reason, tc.want) {
				t.Fatalf("reason = %q, want it to contain %q", reason, tc.want)
			}
		})
	}
	t.Run("a store that cannot be reached is an error", func(t *testing.T) {
		t.Parallel()
		sink := NewSink(erroringStore{&fakeStore{}}, "baleia-cold", "audit")
		reason, err := sink.Verify(ctx, day)
		if err == nil || reason != "" {
			t.Fatalf("Verify = %q, %v; want an error and no reason", reason, err)
		}
	})
}
