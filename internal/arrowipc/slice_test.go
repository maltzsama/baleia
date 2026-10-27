package arrowipc

import (
	"bytes"
	"encoding/json"
	"strconv"
	"testing"

	"github.com/apache/arrow-go/v18/arrow/array"
	"github.com/apache/arrow-go/v18/arrow/ipc"
	"github.com/stretchr/testify/require"
)

// int64Column reads every row of the first Int64 column from an Arrow IPC stream.
func int64Column(t *testing.T, data []byte) []int64 {
	t.Helper()
	r, err := ipc.NewReader(bytes.NewReader(data))
	require.NoError(t, err)
	defer r.Release()
	var out []int64
	for r.Next() {
		col := r.RecordBatch().Column(0).(*array.Int64)
		for i := 0; i < col.Len(); i++ {
			out = append(out, col.Value(i))
		}
	}
	require.NoError(t, r.Err())
	return out
}

// writeInt64File writes rows 0..total-1 as a bigint Arrow IPC file.
func writeInt64File(t *testing.T, total int) []byte {
	t.Helper()
	b, err := NewBatcher([]ColumnInfo{{Name: "n", Type: "bigint"}})
	require.NoError(t, err)
	defer b.Release()

	var file bytes.Buffer
	fw, err := NewFileWriter(&file, b.Schema())
	require.NoError(t, err)
	rows := make([][]any, 0, total)
	for i := 0; i < total; i++ {
		rows = append(rows, []any{json.Number(strconv.Itoa(i))})
	}
	require.NoError(t, b.Append(rows, fw.Write))
	require.NoError(t, b.Flush(fw.Write))
	require.NoError(t, fw.Close())
	return file.Bytes()
}

func TestSliceRowsAcrossBatches(t *testing.T) {
	t.Parallel()
	file := writeInt64File(t, recordBatchSize+10)

	// Starts inside batch 0 and continues into batch 1.
	page, err := SliceRows(bytes.NewReader(file), 5, 10)
	require.NoError(t, err)

	got := int64Column(t, page)
	require.Len(t, got, 10)
	require.Equal(t, int64(5), got[0])
	require.Equal(t, int64(14), got[9])
}

func TestSliceRowsAtBatchBoundary(t *testing.T) {
	t.Parallel()
	file := writeInt64File(t, 2*recordBatchSize)

	page, err := SliceRows(bytes.NewReader(file), int64(recordBatchSize), 3)
	require.NoError(t, err)

	got := int64Column(t, page)
	require.Equal(t, []int64{int64(recordBatchSize), int64(recordBatchSize) + 1, int64(recordBatchSize) + 2}, got)
}

func TestSliceRowsPastEndIsEmpty(t *testing.T) {
	t.Parallel()
	file := writeInt64File(t, 100)

	page, err := SliceRows(bytes.NewReader(file), 500, 10)
	require.NoError(t, err)

	require.Empty(t, int64Column(t, page))
}

func TestSliceStreamSlicesLegacyStream(t *testing.T) {
	t.Parallel()
	// Legacy results were written as IPC streams (no footer); they still page.
	var stream bytes.Buffer
	sw, err := NewStreamWriter(&stream, []ColumnInfo{{Name: "n", Type: "bigint"}})
	require.NoError(t, err)
	rows := make([][]any, 0, 100)
	for i := 0; i < 100; i++ {
		rows = append(rows, []any{json.Number(strconv.Itoa(i))})
	}
	require.NoError(t, sw.AppendRows(rows))
	require.NoError(t, sw.Close())

	page, err := SliceStream(stream.Bytes(), 5, 10)
	require.NoError(t, err)

	got := int64Column(t, page)
	require.Len(t, got, 10)
	require.Equal(t, int64(5), got[0])
	require.Equal(t, int64(14), got[9])
}
