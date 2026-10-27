package api

import (
	"bufio"
	"bytes"
	"encoding/csv"
	"encoding/json"
	"io"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/apache/arrow-go/v18/arrow"
	"github.com/apache/arrow-go/v18/arrow/array"
	"github.com/apache/arrow-go/v18/arrow/ipc"
	"github.com/apache/arrow-go/v18/arrow/memory"
)

// arrowStream builds an Arrow IPC stream with one string column.
func arrowStream(t *testing.T, values []string) *bytes.Buffer {
	t.Helper()
	schema := arrow.NewSchema([]arrow.Field{{Name: "value", Type: arrow.BinaryTypes.String}}, nil)
	builder := array.NewStringBuilder(memory.NewGoAllocator())
	defer builder.Release()
	for _, value := range values {
		builder.Append(value)
	}
	column := builder.NewArray()
	defer column.Release()
	record := array.NewRecordBatch(schema, []arrow.Array{column}, int64(len(values)))
	defer record.Release()

	var out bytes.Buffer
	writer := ipc.NewWriter(&out, ipc.WithSchema(schema))
	if err := writer.Write(record); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return &out
}

func TestExportCSV_StreamsAndQuotes(t *testing.T) {
	t.Parallel()
	values := []string{"a,b", `he said "hi"`, "line1\nline2", "plain"}
	stream := arrowStream(t, values)

	var out bytes.Buffer
	if err := convertResult(&out, stream, exportCSV); err != nil {
		t.Fatal(err)
	}

	rows, err := csv.NewReader(strings.NewReader(out.String())).ReadAll()
	if err != nil {
		t.Fatalf("the CSV must be valid: %v", err)
	}
	if len(rows) != len(values)+1 {
		t.Fatalf("got %d rows, want %d", len(rows), len(values)+1)
	}
	if rows[0][0] != "value" {
		t.Fatalf("header = %q", rows[0])
	}
	for i, want := range values {
		if rows[i+1][0] != want {
			t.Errorf("row %d = %q, want %q", i+1, rows[i+1][0], want)
		}
	}
}

func TestExportTSVAndJSONL(t *testing.T) {
	t.Parallel()
	stream := arrowStream(t, []string{"a\tb", `q"q`})

	var tsv bytes.Buffer
	if err := convertResult(&tsv, stream, exportTSV); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(tsv.String(), "value\n") {
		t.Fatalf("missing header: %q", tsv.String())
	}

	stream = arrowStream(t, []string{"a", "b"})
	var jsonl bytes.Buffer
	if err := convertResult(&jsonl, stream, exportJSONL); err != nil {
		t.Fatal(err)
	}
	scanner := bufio.NewScanner(strings.NewReader(jsonl.String()))
	var rows []map[string]any
	for scanner.Scan() {
		var row map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &row); err != nil {
			t.Fatalf("invalid JSONL: %v", err)
		}
		rows = append(rows, row)
	}
	if len(rows) != 2 || rows[0]["value"] != "a" || rows[1]["value"] != "b" {
		t.Fatalf("rows = %+v", rows)
	}
}

func TestResultKeyFor(t *testing.T) {
	t.Parallel()
	fallback := "query-results/e/1/0.arrow"
	sets := []byte(`[{"result_index":0,"result_key":"query-results/e/1/0.arrow"},{"result_index":1,"result_key":"query-results/e/1/1.arrow"}]`)

	if got := resultKeyFor(sets, &fallback, 0); got != fallback {
		t.Errorf("index 0 = %q", got)
	}
	if got := resultKeyFor(sets, &fallback, 1); got != "query-results/e/1/1.arrow" {
		t.Errorf("index 1 = %q", got)
	}
	if got := resultKeyFor(sets, &fallback, 2); got != "" {
		t.Errorf("missing index = %q", got)
	}
	if got := resultKeyFor(nil, &fallback, 0); got != fallback {
		t.Errorf("fallback = %q", got)
	}
}

// TestExportCSV_OneMillionRows streams a million rows through the CSV writer in
// batches and checks the count stays bounded: the converter never materializes
// the result.
func TestExportCSV_OneMillionRows(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("million-row export in short mode")
	}

	const (
		batchSize = 10_000
		batches   = 100
	)
	schema := arrow.NewSchema([]arrow.Field{{Name: "n", Type: arrow.PrimitiveTypes.Int64}}, nil)

	counter := &lineCounter{}
	pr, pw := io.Pipe()
	var writeErr error
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer pw.Close()
		writer := ipc.NewWriter(pw, ipc.WithSchema(schema))
		defer writer.Close()
		for batch := 0; batch < batches; batch++ {
			builder := array.NewInt64Builder(memory.NewGoAllocator())
			for i := 0; i < batchSize; i++ {
				builder.Append(int64(batch*batchSize + i))
			}
			column := builder.NewArray()
			builder.Release()
			record := array.NewRecordBatch(schema, []arrow.Array{column}, batchSize)
			writeErr = writer.Write(record)
			record.Release()
			column.Release()
			if writeErr != nil {
				return
			}
		}
	}()

	var before runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)

	if err := convertResult(counter, pr, exportCSV); err != nil {
		t.Fatal(err)
	}
	wg.Wait()
	if writeErr != nil {
		t.Fatal(writeErr)
	}

	var after runtime.MemStats
	runtime.ReadMemStats(&after)
	// Bounded by a batch (plus the pipe); far below one row per allocation.
	if after.HeapAlloc > before.HeapAlloc {
		if growth := after.HeapAlloc - before.HeapAlloc; growth > 128<<20 {
			t.Errorf("heap grew by %d bytes while exporting 1M rows", growth)
		}
	}
	if want := batches*batchSize + 1; counter.lines != want {
		t.Errorf("lines = %d, want %d", counter.lines, want)
	}
}

type lineCounter struct {
	mu    sync.Mutex
	lines int
}

func (c *lineCounter) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lines += bytes.Count(p, []byte{'\n'})
	return len(p), nil
}
