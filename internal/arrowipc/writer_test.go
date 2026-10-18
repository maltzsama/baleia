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
	values := make([][]interface{}, 0, rows)
	for i := 0; i < rows; i++ {
		values = append(values, []interface{}{json.Number(strconv.Itoa(i))})
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
func roundTrip(t *testing.T, cols []ColumnInfo, rows [][]interface{}) [][]interface{} {
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

	got := make([][]interface{}, rec.NumRows())
	for row := 0; row < int(rec.NumRows()); row++ {
		got[row] = make([]interface{}, len(cols))
		for col := 0; col < len(cols); col++ {
			got[row][col] = valueAt(rec.Column(col), row)
		}
	}
	require.False(t, r.Next(), "unexpected extra record batch")
	return got
}

func valueAt(a arrow.Array, i int) interface{} {
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
		row  []interface{}
		want []interface{}
	}{
		{
			name: "timestamp(3)",
			cols: []ColumnInfo{{"ts", "timestamp(3)"}},
			row:  []interface{}{"2024-02-29 13:45:01.123"},
			want: []interface{}{arrow.Timestamp(ts.UnixMilli())},
		},
		{
			name: "timestamp(6) with time zone",
			cols: []ColumnInfo{{"ts", "timestamp(6) with time zone"}},
			row:  []interface{}{"2024-02-29 13:45:01.123456 America/Sao_Paulo"},
			want: []interface{}{arrow.Timestamp(saoPauloTS.UnixMicro())},
		},
		{
			name: "decimal(38,10)",
			cols: []ColumnInfo{{"d", "decimal(38,10)"}},
			row:  []interface{}{"12345678901234567890.0123456789"},
			want: []interface{}{mustDecimal(t, "12345678901234567890.0123456789", 38, 10)},
		},
		{
			name: "bigint exact",
			cols: []ColumnInfo{{"n", "bigint"}},
			row:  []interface{}{json.Number("9007199254740993")},
			want: []interface{}{int64(9007199254740993)},
		},
		{
			name: "integer",
			cols: []ColumnInfo{{"n", "integer"}},
			row:  []interface{}{json.Number("42")},
			want: []interface{}{int32(42)},
		},
		{
			name: "boolean",
			cols: []ColumnInfo{{"b", "boolean"}},
			row:  []interface{}{true},
			want: []interface{}{true},
		},
		{
			name: "double",
			cols: []ColumnInfo{{"d", "double"}},
			row:  []interface{}{json.Number("3.5")},
			want: []interface{}{3.5},
		},
		{
			name: "real infinity",
			cols: []ColumnInfo{{"r", "real"}},
			row:  []interface{}{"Infinity"},
			want: []interface{}{float32(math.Inf(1))},
		},
		{
			name: "date before epoch",
			cols: []ColumnInfo{{"d", "date"}},
			row:  []interface{}{"1969-12-31"},
			want: []interface{}{arrow.Date32(-1)},
		},
		{
			name: "varbinary",
			cols: []ColumnInfo{{"v", "varbinary"}},
			row:  []interface{}{"AQID"}, // base64 of {0x01,0x02,0x03}
			want: []interface{}{string([]byte{0x01, 0x02, 0x03})},
		},
		{
			name: "varchar",
			cols: []ColumnInfo{{"s", "varchar"}},
			row:  []interface{}{"hello"},
			want: []interface{}{"hello"},
		},
		{
			name: "array(integer) as JSON",
			cols: []ColumnInfo{{"a", "array(integer)"}},
			row:  []interface{}{[]interface{}{json.Number("1"), json.Number("2")}},
			want: []interface{}{"[1,2]"},
		},
		{
			name: "map(varchar,bigint) as JSON",
			cols: []ColumnInfo{{"m", "map(varchar,bigint)"}},
			row:  []interface{}{map[string]interface{}{"a": json.Number("1"), "b": json.Number("2")}},
			want: []interface{}{`{"a":1,"b":2}`},
		},
		{
			name: "row as JSON",
			cols: []ColumnInfo{{"t", "row(a integer, b varchar)"}},
			row:  []interface{}{[]interface{}{json.Number("1"), "x"}},
			want: []interface{}{`[1,"x"]`},
		},
		{
			name: "nulls",
			cols: []ColumnInfo{{"n", "bigint"}, {"s", "varchar"}, {"d", "timestamp(3)"}},
			row:  []interface{}{nil, nil, nil},
			want: []interface{}{nil, nil, nil},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := roundTrip(t, c.cols, [][]interface{}{c.row})
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

	err = sw.AppendRows([][]interface{}{{"abc"}})
	require.Error(t, err)
	require.Contains(t, err.Error(), "n", "the error must name the offending column")
}

func TestStreamingBatches(t *testing.T) {
	const rows = 20000
	// 20000 / 8192 = 2 full batches + 3616 remainder = 3 batches.
	const wantBatches = 3

	input := make([][]interface{}, 0, rows)
	for i := 0; i < rows; i++ {
		input = append(input, []interface{}{json.Number("1")})
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
