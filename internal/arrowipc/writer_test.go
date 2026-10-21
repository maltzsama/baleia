package arrowipc

import (
	"bytes"
	"encoding/json"
	"math"
	"strconv"
	"testing"
	"time"
	_ "time/tzdata"

	"github.com/apache/arrow-go/v18/arrow"
	"github.com/apache/arrow-go/v18/arrow/array"
	"github.com/apache/arrow-go/v18/arrow/decimal128"
	"github.com/apache/arrow-go/v18/arrow/ipc"
	"github.com/stretchr/testify/require"
)

// TestFileWriterIndexesBatches verifies the IPC file writer emits a footer that
// a file reader can seek through, which is what makes block pagination (#238)
// possible.
func TestFileWriterIndexesBatches(t *testing.T) {
	cols := []ColumnInfo{{Name: "n", Type: "bigint"}}
	b, err := NewBatcher(cols)
	require.NoError(t, err)
	defer b.Release()

	var buf bytes.Buffer
	fw, err := NewFileWriter(&buf, b.Schema())
	require.NoError(t, err)

	const rows = recordBatchSize + 10 // two batches
	values := make([][]any, 0, rows)
	for i := 0; i < rows; i++ {
		values = append(values, []any{json.Number(strconv.Itoa(i))})
	}
	require.NoError(t, b.Append(values, fw.Write))
	require.NoError(t, b.Flush(fw.Write))
	require.NoError(t, fw.Close())

	fr, err := ipc.NewFileReader(bytes.NewReader(buf.Bytes()))
	require.NoError(t, err)
	defer fr.Close()

	require.Equal(t, 2, fr.NumRecords())
	// Random access by batch index works without reading prior batches.
	second, err := fr.RecordBatchAt(1)
	require.NoError(t, err)
	require.Equal(t, int64(10), second.NumRows())
}

// roundTrip writes rows through the streaming writer and reads them back with
// an IPC reader, returning the exact per-cell values.
func roundTrip(t *testing.T, cols []ColumnInfo, rows [][]any) [][]any {
	t.Helper()

	var buf bytes.Buffer
	sw, err := NewStreamWriter(&buf, cols)
	require.NoError(t, err)
	require.NoError(t, sw.AppendRows(rows))
	require.NoError(t, sw.Close())

	r, err := ipc.NewReader(bytes.NewReader(buf.Bytes()))
	require.NoError(t, err)
	defer r.Release()

	require.True(t, r.Next(), "expected at least one record batch")
	rec := r.Record()

	got := make([][]any, rec.NumRows())
	for row := 0; row < int(rec.NumRows()); row++ {
		got[row] = make([]any, len(cols))
		for col := 0; col < len(cols); col++ {
			got[row][col] = valueAt(rec.Column(col), row)
		}
	}
	require.False(t, r.Next(), "unexpected extra record batch")
	return got
}

func valueAt(a arrow.Array, i int) any {
	if a.IsNull(i) {
		return nil
	}
	switch arr := a.(type) {
	case *array.Int8:
		return arr.Value(i)
	case *array.Int16:
		return arr.Value(i)
	case *array.Int32:
		return arr.Value(i)
	case *array.Int64:
		return arr.Value(i)
	case *array.Float32:
		return arr.Value(i)
	case *array.Float64:
		return arr.Value(i)
	case *array.Boolean:
		return arr.Value(i)
	case *array.String:
		return arr.Value(i)
	case *array.Binary:
		return string(arr.Value(i))
	case *array.Date32:
		return arrow.Date32(arr.Value(i))
	case *array.Timestamp:
		return arrow.Timestamp(arr.Value(i))
	case *array.Decimal128:
		return arr.Value(i)
	}
	return nil
}

func mustDecimal(t *testing.T, s string, p, sc int32) decimal128.Num {
	t.Helper()
	n, err := decimal128.FromString(s, p, sc)
	require.NoError(t, err)
	return n
}

func TestTypeMapping(t *testing.T) {
	ts := time.Date(2024, 2, 29, 13, 45, 1, 123000000, time.UTC)
	saoPauloTS := time.Date(2024, 2, 29, 16, 45, 1, 123456000, time.UTC) // 13:45 -0300 == 16:45 UTC

	cases := []struct {
		name string
		cols []ColumnInfo
		row  []any
		want []any
	}{
		{
			name: "timestamp(3)",
			cols: []ColumnInfo{{"ts", "timestamp(3)"}},
			row:  []any{"2024-02-29 13:45:01.123"},
			want: []any{arrow.Timestamp(ts.UnixMilli())},
		},
		{
			name: "timestamp(6) with time zone",
			cols: []ColumnInfo{{"ts", "timestamp(6) with time zone"}},
			row:  []any{"2024-02-29 13:45:01.123456 America/Sao_Paulo"},
			want: []any{arrow.Timestamp(saoPauloTS.UnixMicro())},
		},
		{
			name: "decimal(38,10)",
			cols: []ColumnInfo{{"d", "decimal(38,10)"}},
			row:  []any{"12345678901234567890.0123456789"},
			want: []any{mustDecimal(t, "12345678901234567890.0123456789", 38, 10)},
		},
		{
			name: "bigint exact",
			cols: []ColumnInfo{{"n", "bigint"}},
			row:  []any{json.Number("9007199254740993")},
			want: []any{int64(9007199254740993)},
		},
		{
			name: "integer",
			cols: []ColumnInfo{{"n", "integer"}},
			row:  []any{json.Number("42")},
			want: []any{int32(42)},
		},
		{
			name: "boolean",
			cols: []ColumnInfo{{"b", "boolean"}},
			row:  []any{true},
			want: []any{true},
		},
		{
			name: "double",
			cols: []ColumnInfo{{"d", "double"}},
			row:  []any{json.Number("3.5")},
			want: []any{3.5},
		},
		{
			name: "real infinity",
			cols: []ColumnInfo{{"r", "real"}},
			row:  []any{"Infinity"},
			want: []any{float32(math.Inf(1))},
		},
		{
			name: "date before epoch",
			cols: []ColumnInfo{{"d", "date"}},
			row:  []any{"1969-12-31"},
			want: []any{arrow.Date32(-1)},
		},
		{
			name: "varbinary",
			cols: []ColumnInfo{{"v", "varbinary"}},
			row:  []any{"AQID"}, // base64 of {0x01,0x02,0x03}
			want: []any{string([]byte{0x01, 0x02, 0x03})},
		},
		{
			name: "varchar",
			cols: []ColumnInfo{{"s", "varchar"}},
			row:  []any{"hello"},
			want: []any{"hello"},
		},
		{
			name: "array(integer) as JSON",
			cols: []ColumnInfo{{"a", "array(integer)"}},
			row:  []any{[]any{json.Number("1"), json.Number("2")}},
			want: []any{"[1,2]"},
		},
		{
			name: "map(varchar,bigint) as JSON",
			cols: []ColumnInfo{{"m", "map(varchar,bigint)"}},
			row:  []any{map[string]any{"a": json.Number("1"), "b": json.Number("2")}},
			want: []any{`{"a":1,"b":2}`},
		},
		{
			name: "row as JSON",
			cols: []ColumnInfo{{"t", "row(a integer, b varchar)"}},
			row:  []any{[]any{json.Number("1"), "x"}},
			want: []any{`[1,"x"]`},
		},
		{
			name: "nulls",
			cols: []ColumnInfo{{"n", "bigint"}, {"s", "varchar"}, {"d", "timestamp(3)"}},
			row:  []any{nil, nil, nil},
			want: []any{nil, nil, nil},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := roundTrip(t, c.cols, [][]any{c.row})
			require.Equal(t, 1, len(got))
			for i := range c.want {
				require.Equal(t, c.want[i], got[0][i], "column %d", i)
			}
		})
	}
}

func TestParseFailureIsError(t *testing.T) {
	var buf bytes.Buffer
	sw, err := NewStreamWriter(&buf, []ColumnInfo{{"n", "bigint"}})
	require.NoError(t, err)

	err = sw.AppendRows([][]any{{"abc"}})
	require.Error(t, err)
	require.Contains(t, err.Error(), "n", "the error must name the offending column")
}

func TestStreamingBatches(t *testing.T) {
	const rows = 20000
	// 20000 / 8192 = 2 full batches + 3616 remainder = 3 batches.
	const wantBatches = 3

	input := make([][]any, 0, rows)
	for i := 0; i < rows; i++ {
		input = append(input, []any{json.Number("1")})
	}

	var buf bytes.Buffer
	sw, err := NewStreamWriter(&buf, []ColumnInfo{{"id", "bigint"}})
	require.NoError(t, err)
	require.NoError(t, sw.AppendRows(input))
	require.NoError(t, sw.Close())
	require.Equal(t, int64(rows), sw.RowCount())

	r, err := ipc.NewReader(bytes.NewReader(buf.Bytes()))
	require.NoError(t, err)
	defer r.Release()

	batches := 0
	for r.Next() {
		batches++
	}
	require.Equal(t, wantBatches, batches)
}
