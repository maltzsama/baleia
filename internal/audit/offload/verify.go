package offload

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"
)

// Verify says why a day's manifest cannot gate dropping the day's partition, or
// returns "" when the day left the database in full (ADR 0026): the manifest
// exists and names this stream and day, and every object it lists is in the
// store with the recorded checksum and row count. An empty day has a manifest
// with no objects and verifies. A failure to reach the store is an error, never
// a reason: the gate stays closed either way, but only a reason is a finding
// about the data.
func (s *Sink) Verify(ctx context.Context, day time.Time) (string, error) {
	key := s.ManifestKey(day)
	ok, err := s.store.Has(ctx, key)
	if err != nil {
		return "", err
	}
	if !ok {
		return "manifest missing", nil
	}
	data, _, err := s.store.Get(ctx, key)
	if err != nil {
		return "", err
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return "manifest unreadable", nil
	}
	if m.Stream != s.stream || m.Day != day.UTC().Format("2006-01-02") {
		return fmt.Sprintf("manifest names stream %q day %q", m.Stream, m.Day), nil
	}
	if len(m.Keys) != len(m.Counts) || len(m.Keys) != len(m.Checksums) {
		return "manifest counts and checksums do not match its objects", nil
	}
	for _, obj := range m.Keys {
		count, hasCount := m.Counts[obj]
		sum, hasSum := m.Checksums[obj]
		if !hasCount || !hasSum {
			return fmt.Sprintf("manifest lists %s without a count and checksum", obj), nil
		}
		present, err := s.store.Has(ctx, obj)
		if err != nil {
			return "", err
		}
		if !present {
			return fmt.Sprintf("object %s is missing", obj), nil
		}
		body, _, err := s.store.Get(ctx, obj)
		if err != nil {
			return "", err
		}
		if got := sha256Hex(body); got != sum {
			return fmt.Sprintf("object %s has checksum %s, manifest says %s", obj, got, sum), nil
		}
		rows, err := countRows(body)
		if err != nil {
			return fmt.Sprintf("object %s is not valid gzip", obj), nil
		}
		if rows != count {
			return fmt.Sprintf("object %s has %d rows, manifest says %d", obj, rows, count), nil
		}
	}
	return "", nil
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// countRows counts the NDJSON rows of a gzip object without holding the
// decompressed text.
func countRows(gz []byte) (int64, error) {
	r, err := gzip.NewReader(bytes.NewReader(gz))
	if err != nil {
		return 0, err
	}
	defer func() { _ = r.Close() }()
	var rows int64
	buf := make([]byte, 64<<10)
	for {
		n, err := r.Read(buf)
		rows += int64(bytes.Count(buf[:n], []byte{'\n'}))
		if errors.Is(err, io.EOF) {
			return rows, nil
		}
		if err != nil {
			return 0, err
		}
	}
}
