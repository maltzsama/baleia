package arrowipc

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/apache/arrow/go/v17/arrow"
	"github.com/apache/arrow/go/v17/arrow/array"
	"github.com/apache/arrow/go/v17/arrow/ipc"
	"github.com/apache/arrow/go/v17/arrow/memory"
)

type ColumnInfo struct {
	Name string
	Type string
}

func TrinoToArrow(columns []ColumnInfo, rows [][]interface{}) ([]byte, error) {
	fields := make([]arrow.Field, len(columns))
	for i, col := range columns {
		fields[i] = arrow.Field{
			Name:     col.Name,
			Type:     trinoTypeToArrow(col.Type),
			Nullable: true,
		}
	}

	schema := arrow.NewSchema(fields, nil)
	pool := memory.NewGoAllocator()

	record, err := buildRecord(pool, schema, columns, rows)
	if err != nil {
		return nil, fmt.Errorf("build record: %w", err)
	}
	defer record.Release()

	batchSize := int64(10000)
	var buf strings.Builder
	wr := ipc.NewWriter(&buf, ipc.WithSchema(schema), ipc.WithAllocator(pool))
	defer wr.Close()

	if len(rows) == 0 {
		record, _ := buildEmptyRecord(pool, schema, columns)
		if record != nil {
			defer record.Release()
			wr.Write(record)
		}
		wr.Close()
		return []byte(buf.String()), nil
	}

	for offset := int64(0); offset < record.NumRows(); offset += batchSize {
		end := offset + batchSize
		if end > record.NumRows() {
			end = record.NumRows()
		}
		slice := record.NewSlice(offset, end)
		defer slice.Release()
		if err := wr.Write(slice); err != nil {
			return nil, fmt.Errorf("write arrow batch: %w", err)
		}
	}

	wr.Close()
	return []byte(buf.String()), nil
}

func buildRecord(pool memory.Allocator, schema *arrow.Schema, columns []ColumnInfo, rows [][]interface{}) (arrow.Record, error) {
	numRows := int64(len(rows))

	cols := make([]arrow.Array, len(columns))
	for colIdx, col := range columns {
		builder := array.NewBuilder(pool, schema.Field(colIdx).Type)
		defer builder.Release()

		for rowIdx := 0; rowIdx < len(rows); rowIdx++ {
			val := rows[rowIdx][colIdx]
			if err := appendValue(builder, val); err != nil {
				_ = col.Type
				return nil, fmt.Errorf("col %q row %d: %w", col.Name, rowIdx, err)
			}
		}

		cols[colIdx] = builder.NewArray()
		defer cols[colIdx].Release()
	}

	return array.NewRecord(schema, cols, numRows), nil
}

func buildEmptyRecord(pool memory.Allocator, schema *arrow.Schema, columns []ColumnInfo) (arrow.Record, error) {
	cols := make([]arrow.Array, len(columns))
	for colIdx := range columns {
		builder := array.NewBuilder(pool, schema.Field(colIdx).Type)
		defer builder.Release()
		cols[colIdx] = builder.NewArray()
		defer cols[colIdx].Release()
	}
	return array.NewRecord(schema, cols, 0), nil
}

func trinoTypeToArrow(t string) arrow.DataType {
	t = strings.ToLower(t)
	switch {
	case strings.HasPrefix(t, "bigint"):
		return arrow.PrimitiveTypes.Int64
	case strings.HasPrefix(t, "integer"), t == "int":
		return arrow.PrimitiveTypes.Int32
	case strings.HasPrefix(t, "smallint"):
		return arrow.PrimitiveTypes.Int16
	case strings.HasPrefix(t, "double"), strings.HasPrefix(t, "decimal"):
		return arrow.PrimitiveTypes.Float64
	case strings.HasPrefix(t, "real"), t == "float":
		return arrow.PrimitiveTypes.Float32
	case strings.HasPrefix(t, "boolean"):
		return arrow.FixedWidthTypes.Boolean
	case strings.HasPrefix(t, "date"):
		return arrow.FixedWidthTypes.Date32
	case strings.HasPrefix(t, "timestamp"):
		return arrow.FixedWidthTypes.Timestamp_us
	default:
		return arrow.BinaryTypes.String
	}
}

func appendValue(builder array.Builder, val interface{}) error {
	if val == nil {
		builder.AppendNull()
		return nil
	}

	switch b := builder.(type) {
	case *array.Int64Builder:
		b.Append(toInt64(val))
	case *array.Int32Builder:
		b.Append(toInt32(val))
	case *array.Int16Builder:
		b.Append(toInt16(val))
	case *array.Float64Builder:
		b.Append(toFloat64(val))
	case *array.Float32Builder:
		b.Append(toFloat32(val))
	case *array.BooleanBuilder:
		b.Append(toBool(val))
	case *array.Date32Builder:
		b.Append(arrow.Date32(toDate32(val)))
	case *array.TimestampBuilder:
		b.Append(arrow.Timestamp(0))
	case *array.StringBuilder:
		b.Append(toString(val))
	default:
		builder.AppendNull()
	}
	return nil
}

func toInt64(v interface{}) int64 {
	switch val := v.(type) {
	case float64:
		return int64(val)
	case string:
		n, _ := strconv.ParseInt(val, 10, 64)
		return n
	case json.Number:
		n, _ := val.Int64()
		return n
	}
	return 0
}

func toInt32(v interface{}) int32  { return int32(toInt64(v)) }
func toInt16(v interface{}) int16  { return int16(toInt64(v)) }
func toFloat64(v interface{}) float64 {
	switch val := v.(type) {
	case float64:
		return val
	case string:
		f, _ := strconv.ParseFloat(val, 64)
		return f
	case json.Number:
		f, _ := val.Float64()
		return f
	}
	return 0
}
func toFloat32(v interface{}) float32 { return float32(toFloat64(v)) }
func toBool(v interface{}) bool {
	switch val := v.(type) {
	case bool:
		return val
	case string:
		b, _ := strconv.ParseBool(val)
		return b
	case float64:
		return val != 0
	}
	return false
}
func toDate32(v interface{}) int32 {
	switch val := v.(type) {
	case string:
		for _, layout := range []string{"2006-01-02", "2006-01-02T15:04:05", "2006-01-02 15:04:05"} {
			t, err := time.Parse(layout, val)
			if err == nil {
				return int32(t.Unix() / 86400)
			}
		}
	}
	return 0
}
func toString(v interface{}) string { return fmt.Sprint(v) }
