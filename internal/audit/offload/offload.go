// Package offload writes a stream's rows to an S3 cold sink as gzip NDJSON,
// one object per month with a per-month manifest (ADR 0024). The sink is the
// building block; the month-by-month loop that drives it and advances the
// cursor lives with the retention schedules.
package offload

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"
)

// objectStore is the S3 surface the sink needs. internal/storage.S3Client
// satisfies it.
type objectStore interface {
	Put(ctx context.Context, key string, data []byte, contentType string) error
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

// ObjectKey is the deterministic key of a month's object: re-running the
// offload of a month overwrites the same object (idempotent).
func (s *Sink) ObjectKey(month time.Time) string {
	m := month.UTC()
	return fmt.Sprintf("%s/%s/%s/%s-%s.ndjson.gz", s.prefix, s.stream, m.Format("2006/01"), s.stream, m.Format("200601"))
}

// ManifestKey is the deterministic key of a month's manifest.
func (s *Sink) ManifestKey(month time.Time) string {
	return fmt.Sprintf("%s/%s/%s/manifest.json", s.prefix, s.stream, month.UTC().Format("2006/01"))
}

// Manifest records what left the database for a month.
type Manifest struct {
	Stream    string            `json:"stream"`
	Month     string            `json:"month"`
	Keys      []string          `json:"keys"`
	Counts    map[string]int64  `json:"counts"`
	Checksums map[string]string `json:"checksums"`
}

// Write compresses rows into one gzip NDJSON object, stores it, and returns
// its key, row count and checksum.
func (s *Sink) Write(ctx context.Context, month time.Time, rows []json.RawMessage) (key string, count int64, checksum string, err error) {
	data, count, checksum, err := buildObject(rows)
	if err != nil {
		return "", 0, "", err
	}
	key = s.ObjectKey(month)
	if err := s.store.Put(ctx, key, data, "application/x-ndjson"); err != nil {
		return "", 0, "", fmt.Errorf("offload: put %s: %w", key, err)
	}
	return key, count, checksum, nil
}

// WriteManifest stores a month's manifest.
func (s *Sink) WriteManifest(ctx context.Context, month time.Time, m Manifest) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("offload: encode manifest: %w", err)
	}
	if err := s.store.Put(ctx, s.ManifestKey(month), data, "application/json"); err != nil {
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
