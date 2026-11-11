package offload

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeStore records puts in memory.
type fakeStore struct {
	mu      sync.Mutex
	objects map[string][]byte
	types   map[string]string
}

func (f *fakeStore) Put(_ context.Context, key string, data []byte, contentType string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.objects[key] = data
	f.types[key] = contentType
	return nil
}

func gunzip(t *testing.T, data []byte) string {
	t.Helper()
	gz, err := gzip.NewReader(strings.NewReader(string(data)))
	if err != nil {
		t.Fatalf("gzip reader: %v", err)
	}
	defer func() { _ = gz.Close() }()
	raw, err := io.ReadAll(gz)
	if err != nil {
		t.Fatalf("read gzip: %v", err)
	}
	return string(raw)
}

func TestBuildObject(t *testing.T) {
	t.Parallel()
	rows := []json.RawMessage{
		json.RawMessage(`{"@timestamp":"2026-11-01T00:00:00Z"}`),
		json.RawMessage(`{"@timestamp":"2026-11-02T00:00:00Z"}`),
	}
	data, count, checksum, err := buildObject(rows)
	if err != nil {
		t.Fatalf("buildObject: %v", err)
	}
	if count != 2 {
		t.Fatalf("count = %d, want 2", count)
	}
	if checksum == "" || len(checksum) != 64 {
		t.Fatalf("checksum = %q", checksum)
	}
	// Deterministic: the same rows produce the same bytes and checksum.
	data2, _, checksum2, _ := buildObject(rows)
	if checksum2 != checksum {
		t.Fatalf("checksum not deterministic: %q vs %q", checksum, checksum2)
	}
	got := gunzip(t, data)
	if got != `{"@timestamp":"2026-11-01T00:00:00Z"}`+"\n"+`{"@timestamp":"2026-11-02T00:00:00Z"}`+"\n" {
		t.Fatalf("ndjson = %q", got)
	}
	_ = data2
}

func TestObjectAndManifestKeys(t *testing.T) {
	t.Parallel()
	s := NewSink(nil, "baleia-cold", "audit")
	month := time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC)
	if got, want := s.ObjectKey(month), "baleia-cold/audit/2026/11/audit-202611.ndjson.gz"; got != want {
		t.Fatalf("ObjectKey = %q, want %q", got, want)
	}
	if got, want := s.ManifestKey(month), "baleia-cold/audit/2026/11/manifest.json"; got != want {
		t.Fatalf("ManifestKey = %q, want %q", got, want)
	}
}

func TestWriteAndManifest(t *testing.T) {
	t.Parallel()
	store := &fakeStore{objects: map[string][]byte{}, types: map[string]string{}}
	s := NewSink(store, "baleia-cold", "audit")
	ctx := context.Background()
	month := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)

	key, count, checksum, err := s.Write(ctx, month, []json.RawMessage{json.RawMessage(`{"a":1}`)})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if key != s.ObjectKey(month) {
		t.Fatalf("key = %q", key)
	}
	if count != 1 || checksum == "" {
		t.Fatalf("count = %d checksum = %q", count, checksum)
	}
	if store.types[key] != "application/x-ndjson" {
		t.Fatalf("content type = %q", store.types[key])
	}
	if got := gunzip(t, store.objects[key]); got != `{"a":1}`+"\n" {
		t.Fatalf("object = %q", got)
	}

	m := Manifest{Stream: "audit", Month: "2026-11", Keys: []string{key}, Counts: map[string]int64{key: 1}, Checksums: map[string]string{key: checksum}}
	if err := s.WriteManifest(ctx, month, m); err != nil {
		t.Fatalf("WriteManifest: %v", err)
	}
	var got Manifest
	if err := json.Unmarshal(store.objects[s.ManifestKey(month)], &got); err != nil {
		t.Fatalf("manifest json: %v", err)
	}
	if got.Keys[0] != key || got.Counts[key] != 1 || got.Checksums[key] != checksum {
		t.Fatalf("manifest = %+v", got)
	}
}
