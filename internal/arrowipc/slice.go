package arrowipc

import (
	"bytes"
	"fmt"
	"math"

	"github.com/apache/arrow-go/v18/arrow/ipc"
	"github.com/apache/arrow-go/v18/arrow/memory"
)

// SliceRows writes rows [offset, offset+limit) of an Arrow IPC file to a new
// IPC stream. The file's batches hold recordBatchSize rows (except the last),
// so it seeks straight to the batch that covers offset and reads only the
// batches it needs — never the whole result (#238). limit <= 0 means "to the
// end".
func SliceRows(ra ipc.ReadAtSeeker, offset, limit int64) ([]byte, error) {
	fr, err := ipc.NewFileReader(ra)
	if err != nil {
		return nil, fmt.Errorf("open arrow file: %w", err)
	}
	defer fr.Close()

	var buf bytes.Buffer
	sw := ipc.NewWriter(&buf, ipc.WithSchema(fr.Schema()), ipc.WithAllocator(memory.NewGoAllocator()))

	batchSize := int64(recordBatchSize)
	idx := offset / batchSize
	within := offset % batchSize
	remaining := limit
	if remaining <= 0 {
		remaining = math.MaxInt64
	}

	for idx < int64(fr.NumRecords()) && remaining > 0 {
		rec, err := fr.RecordBatchAt(int(idx))
		if err != nil {
			return nil, fmt.Errorf("read batch %d: %w", idx, err)
		}
		rows := rec.NumRows()
		if within >= rows {
			// offset is past the last (short) batch: nothing left to read.
			rec.Release()
			break
		}
		take := rows - within
		if take > remaining {
			take = remaining
		}
		if within == 0 && take == rows {
			err = sw.Write(rec)
		} else {
			// NewSlice takes [start, end), so pass within+take as the end.
			slice := rec.NewSlice(within, within+take)
			err = sw.Write(slice)
			slice.Release()
		}
		rec.Release()
		if err != nil {
			return nil, fmt.Errorf("write batch %d: %w", idx, err)
		}
		remaining -= take
		within = 0
		idx++
	}

	if err := sw.Close(); err != nil {
		return nil, fmt.Errorf("close arrow stream: %w", err)
	}
	return buf.Bytes(), nil
}

// SliceStream writes rows [offset, offset+limit) of an Arrow IPC *stream* (the
// legacy result format, no footer) to a new stream. Streams are sequential, so
// it decodes until it reaches the range. limit <= 0 means "to the end".
func SliceStream(data []byte, offset, limit int64) ([]byte, error) {
	r, err := ipc.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("open arrow stream: %w", err)
	}
	defer r.Release()

	var buf bytes.Buffer
	sw := ipc.NewWriter(&buf, ipc.WithSchema(r.Schema()), ipc.WithAllocator(memory.NewGoAllocator()))

	remaining := limit
	if remaining <= 0 {
		remaining = math.MaxInt64
	}
	pos := int64(0)
	for r.Next() && remaining > 0 {
		rec := r.RecordBatch()
		rows := rec.NumRows()
		if pos+rows <= offset {
			pos += rows
			continue
		}
		within := int64(0)
		if offset > pos {
			within = offset - pos
		}
		take := rows - within
		if take > remaining {
			take = remaining
		}
		if within == 0 && take == rows {
			if err := sw.Write(rec); err != nil {
				return nil, fmt.Errorf("write batch: %w", err)
			}
		} else {
			slice := rec.NewSlice(within, within+take)
			err := sw.Write(slice)
			slice.Release()
			if err != nil {
				return nil, fmt.Errorf("write batch: %w", err)
			}
		}
		remaining -= take
		pos += rows
	}
	if err := r.Err(); err != nil {
		return nil, fmt.Errorf("read arrow stream: %w", err)
	}
	if err := sw.Close(); err != nil {
		return nil, fmt.Errorf("close arrow stream: %w", err)
	}
	return buf.Bytes(), nil
}
