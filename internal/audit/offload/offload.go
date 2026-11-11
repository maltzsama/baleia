// Package offload writes a stream's rows to an S3 cold sink as gzip NDJSON,
// one or more bounded chunks per UTC day with a per-day manifest (ADR 0024,
// amended by ADR 0026). The sink is the building block; the day-by-day loop
// that drives it and advances the cursor lives with the retention schedules.
package offload

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"time"

	"github.com/galileostudio/baleia/internal/audit/export"
)

// objectStore is the S3 surface the sink needs. internal/storage.S3Client
// satisfies it.
type objectStore interface {
	Put(ctx context.Context, key string, data []byte, contentType string) error
	Get(ctx context.Context, key string) ([]byte, string, error)
	Has(ctx context.Context, key string) (bool, error)
}

// Sink writes gzip NDJSON objects and their manifests to a cold store.
type Sink struct {
	store  objectStore
	prefix string
	stream string
}

// NewSink builds the cold sink for one stream.
func NewSink(store objectStore, prefix, stream string) *Sink {
	return &Sink{store: store, prefix: prefix, stream: stream}
}

// ManifestKey is the deterministic key of a day's manifest:
// <prefix>/<stream>/<yyyy>/<mm>/<dd>/manifest.json.
func (s *Sink) ManifestKey(day time.Time) string {
	return fmt.Sprintf("%s/%s/%s/manifest.json", s.prefix, s.stream, day.UTC().Format("2006/01/02"))
}

// ChunkKey is the deterministic key of one bounded chunk of a day's object:
// <prefix>/<stream>/<yyyy>/<mm>/<dd>/<object>-<yyyymmdd>-<hash>.ndjson.gz. The
// chunk's first row pins the key, so re-running or resuming a day overwrites
// the same chunk (idempotent) instead of duplicating it.
func (s *Sink) ChunkKey(day time.Time, object string, first export.Position) string {
	d := day.UTC()
	tie := first.OccurredAt.UTC().Format("20060102150405.000000000")
	if first.Key != "" {
		tie += "-" + first.Key
	} else if first.ID != 0 {
		tie += "-" + strconv.FormatInt(first.ID, 10)
	}
	sum := sha256.Sum256([]byte(tie))
	return fmt.Sprintf("%s/%s/%s/%s-%s-%s.ndjson.gz", s.prefix, s.stream, d.Format("2006/01/02"), object, d.Format("20060102"), hex.EncodeToString(sum[:8]))
}

// Manifest records what left the database for a day.
type Manifest struct {
	Stream    string            `json:"stream"`
	Day       string            `json:"day"`
	Keys      []string          `json:"keys"`
	Counts    map[string]int64  `json:"counts"`
	Checksums map[string]string `json:"checksums"`
}

// add records one chunk and rebuilds the sorted, deduplicated key list.
func (m *Manifest) add(key string, count int64, checksum string) {
	m.Counts[key] = count
	m.Checksums[key] = checksum
	keys := make([]string, 0, len(m.Counts))
	for k := range m.Counts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	m.Keys = keys
}

// WriteChunk compresses one chunk and stores it under key, returning its row
// count and checksum.
func (s *Sink) WriteChunk(ctx context.Context, key string, rows []json.RawMessage) (count int64, checksum string, err error) {
	data, count, checksum, err := buildObject(rows)
	if err != nil {
		return 0, "", err
	}
	if err := s.store.Put(ctx, key, data, "application/x-ndjson"); err != nil {
		return 0, "", fmt.Errorf("offload: put %s: %w", key, err)
	}
	return count, checksum, nil
}

// ReadManifest returns the day's manifest, or an empty one when none exists
// yet (a resumed day starts from whatever was already written).
func (s *Sink) ReadManifest(ctx context.Context, day time.Time) (Manifest, error) {
	ok, err := s.store.Has(ctx, s.ManifestKey(day))
	if err != nil {
		return Manifest{}, err
	}
	if !ok {
		return Manifest{
			Stream:    s.stream,
			Day:       day.UTC().Format("2006-01-02"),
			Counts:    map[string]int64{},
			Checksums: map[string]string{},
		}, nil
	}
	data, _, err := s.store.Get(ctx, s.ManifestKey(day))
	if err != nil {
		return Manifest{}, err
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return Manifest{}, fmt.Errorf("offload: decode manifest: %w", err)
	}
	if m.Counts == nil {
		m.Counts = map[string]int64{}
	}
	if m.Checksums == nil {
		m.Checksums = map[string]string{}
	}
	return m, nil
}

// WriteManifest stores a day's manifest.
func (s *Sink) WriteManifest(ctx context.Context, day time.Time, m Manifest) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("offload: encode manifest: %w", err)
	}
	if err := s.store.Put(ctx, s.ManifestKey(day), data, "application/json"); err != nil {
		return fmt.Errorf("offload: put manifest: %w", err)
	}
	return nil
}

// buildObject gzips the NDJSON rows and returns the bytes, count and checksum.
func buildObject(rows []json.RawMessage) (data []byte, count int64, checksum string, err error) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	for _, r := range rows {
		if _, err := gz.Write(r); err != nil {
			return nil, 0, "", err
		}
		if _, err := gz.Write([]byte("\n")); err != nil {
			return nil, 0, "", err
		}
	}
	if err := gz.Close(); err != nil {
		return nil, 0, "", err
	}
	data = buf.Bytes()
	sum := sha256.Sum256(data)
	return data, int64(len(rows)), hex.EncodeToString(sum[:]), nil
}
