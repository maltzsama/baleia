// Package results reads stored Arrow results as JSON rows for agent tools
// (#58). It is the read-only counterpart of the result writer (#89) and shares
// the Arrow slicing used by the HTTP result viewer (#26).
package results

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"math"
	"strconv"
	"time"

	"github.com/apache/arrow-go/v18/arrow/ipc"
	"github.com/galileostudio/baleia/internal/arrowipc"
	"github.com/galileostudio/baleia/internal/storage"
)

// Column is one result column.
type Column struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

// Result is a bounded page of rows. RowCount is the number of rows returned;
// Truncated reports that a row or byte cap stopped the read.
type Result struct {
	Columns   []Column `json:"columns"`
	Rows      [][]any  `json:"rows"`
	RowCount  int64    `json:"row_count"`
	Truncated bool     `json:"truncated"`
}

// Read reads rows [offset, offset+limit) from the stored Arrow result at key.
// maxRows and maxBytes bound the returned page (0 = no bound); limit is capped
// by maxRows.
func Read(ctx context.Context, s3 *storage.S3Client, key string, offset, limit, maxRows, maxBytes int) (Result, error) {
	if limit <= 0 || (maxRows > 0 && limit > maxRows) {
		limit = maxRows
	}
	if limit <= 0 {
		limit = 1000
	}
	ra, err := s3.NewReaderAt(ctx, key)
	if err != nil {
		return Result{}, err
	}
	data, err := arrowipc.SliceRows(ra, int64(offset), int64(limit))
	if err != nil {
		return Result{}, err
	}
	reader, err := ipc.NewReader(bytes.NewReader(data))
	if err != nil {
		return Result{}, err
	}
	defer reader.Release()

	out := Result{Columns: make([]Column, 0, reader.Schema().NumFields())}
	for _, f := range reader.Schema().Fields() {
		out.Columns = append(out.Columns, Column{Name: f.Name, Type: arrowipc.NativeType(f)})
	}
	used := 0
	for reader.Next() {
		rec := reader.Record()
		cols := rec.Columns()
		for i := 0; i < int(rec.NumRows()); i++ {
			if maxRows > 0 && len(out.Rows) >= maxRows {
				out.Truncated = true
				out.RowCount = int64(len(out.Rows))
				return out, nil
			}
			row := make([]any, len(cols))
			for j, col := range cols {
				row[j] = normalize(col.GetOneForMarshal(i))
			}
			encoded, _ := json.Marshal(row)
			if maxBytes > 0 && used+len(encoded) > maxBytes {
				out.Truncated = true
				out.RowCount = int64(len(out.Rows))
				return out, nil
			}
			used += len(encoded)
			out.Rows = append(out.Rows, row)
		}
	}
	if err := reader.Err(); err != nil {
		return Result{}, err
	}
	out.RowCount = int64(len(out.Rows))
	return out, nil
}

// normalize applies the JSON rules from #58: integers beyond 2^53 and decimals
// are strings, binary is base64, timestamps are ISO-8601.
func normalize(v any) any {
	switch x := v.(type) {
	case nil, bool, string:
		return x
	case int:
		return x
	case int32:
		return x
	case int64:
		if x > 1<<53 || x < -(1<<53) {
			return strconv.FormatInt(x, 10)
		}
		return x
	case uint64:
		if x > 1<<53 {
			return strconv.FormatUint(x, 10)
		}
		return x
	case json.Number:
		return x.String()
	case float32:
		return x
	case float64:
		if math.IsInf(x, 0) || math.IsNaN(x) {
			return nil
		}
		return x
	case []byte:
		return base64.StdEncoding.EncodeToString(x)
	case time.Time:
		return x.Format(time.RFC3339Nano)
	}
	return v
}
