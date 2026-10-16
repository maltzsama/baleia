package sqlserver

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/apache/arrow-go/v18/arrow"
	"github.com/apache/arrow-go/v18/arrow/array"
	"github.com/apache/arrow-go/v18/arrow/decimal128"
	"github.com/apache/arrow-go/v18/arrow/memory"
)

// column is one result column: name, Arrow type and the SQL Server type it came
// from (for value rendering and decimal scale).
type column struct {
	name      string
	typ       arrow.DataType
	dbType    string
	precision int32
	scale     int32
}

// schemaOf builds the Arrow schema and the column plans from the driver's
// column types.
func schemaOf(names []string, cts []*sql.ColumnType) (*arrow.Schema, []column) {
	fields := make([]arrow.Field, len(names))
	cols := make([]column, len(names))
	for i, ct := range cts {
		t, p, s := tsqlArrowType(ct)
		fields[i] = arrow.Field{Name: names[i], Type: t, Nullable: true}
		cols[i] = column{name: names[i], typ: t, dbType: ct.DatabaseTypeName(), precision: p, scale: s}
	}
	return arrow.NewSchema(fields, nil), cols
}

// tsqlArrowType maps a SQL Server column type to an Arrow type. Unknown types
// fall back to utf8.
func tsqlArrowType(ct *sql.ColumnType) (arrow.DataType, int32, int32) {
	switch ct.DatabaseTypeName() {
	case "BIT":
		return arrow.FixedWidthTypes.Boolean, 0, 0
	case "TINYINT":
		return arrow.PrimitiveTypes.Uint8, 0, 0
	case "SMALLINT":
		return arrow.PrimitiveTypes.Int16, 0, 0
	case "INT":
		return arrow.PrimitiveTypes.Int32, 0, 0
	case "BIGINT":
		return arrow.PrimitiveTypes.Int64, 0, 0
	case "DECIMAL", "NUMERIC":
		if p64, s64, ok := ct.DecimalSize(); ok && p64 <= 38 {
			p, s := int32(p64), int32(s64)
			return &arrow.Decimal128Type{Precision: p, Scale: s}, p, s
		}
		return arrow.BinaryTypes.String, 0, 0
	case "MONEY":
		return &arrow.Decimal128Type{Precision: 19, Scale: 4}, 19, 4
	case "SMALLMONEY":
		return &arrow.Decimal128Type{Precision: 10, Scale: 4}, 10, 4
	case "FLOAT":
		return arrow.PrimitiveTypes.Float64, 0, 0
	case "REAL":
		return arrow.PrimitiveTypes.Float32, 0, 0
	case "DATE":
		return arrow.FixedWidthTypes.Date32, 0, 0
	case "DATETIME", "DATETIME2", "SMALLDATETIME":
		return &arrow.TimestampType{Unit: arrow.Microsecond}, 0, 0
	case "DATETIMEOFFSET":
		return &arrow.TimestampType{Unit: arrow.Microsecond, TimeZone: "UTC"}, 0, 0
	case "TIME":
		return &arrow.Time64Type{Unit: arrow.Nanosecond}, 0, 0
	case "BINARY", "VARBINARY", "IMAGE", "TIMESTAMP": // TIMESTAMP is rowversion
		return arrow.BinaryTypes.Binary, 0, 0
	default:
		// CHAR, VARCHAR, NCHAR, NVARCHAR, TEXT, NTEXT, XML, UNIQUEIDENTIFIER,
		// SQL_VARIANT, HIERARCHYID, GEOMETRY, GEOGRAPHY render as utf8.
		return arrow.BinaryTypes.String, 0, 0
	}
}

// buildRecord builds one Arrow record from buffered rows. The caller owns the
// record and must release it.
func buildRecord(schema *arrow.Schema, cols []column, values [][]any) (arrow.RecordBatch, error) {
	b := array.NewRecordBuilder(memory.DefaultAllocator, schema)
	for _, row := range values {
		for c := range cols {
			if err := appendValue(b.Field(c), cols[c], row[c]); err != nil {
				b.Release()
				return nil, err
			}
		}
	}
	rec := b.NewRecordBatch()
	b.Release()
	return rec, nil
}

func appendValue(b array.Builder, c column, v any) error {
	if v == nil {
		b.AppendNull()
		return nil
	}
	switch bb := b.(type) {
	case *array.BooleanBuilder:
		bb.Append(v.(bool))
	case *array.Uint8Builder:
		bb.Append(uint8(toInt(v)))
	case *array.Int16Builder:
		bb.Append(int16(toInt(v)))
	case *array.Int32Builder:
		bb.Append(int32(toInt(v)))
	case *array.Int64Builder:
		bb.Append(toInt(v))
	case *array.Float32Builder:
		bb.Append(toFloat32(v))
	case *array.Float64Builder:
		bb.Append(toFloat64(v))
	case *array.StringBuilder:
		bb.Append(toText(c, v))
	case *array.BinaryBuilder:
		bb.Append(toBytes(v))
	case *array.Date32Builder:
		bb.Append(arrow.Date32FromTime(toTime(v)))
	case *array.TimestampBuilder:
		bb.Append(arrow.Timestamp(toTime(v).UnixMicro()))
	case *array.Time64Builder:
		bb.Append(arrow.Time64(nanosSinceMidnight(toTime(v))))
	case *array.Decimal128Builder:
		d, err := toDecimal(v, c.precision, c.scale)
		if err != nil {
			return fmt.Errorf("decimal column %q: %w", c.name, err)
		}
		bb.Append(d)
	default:
		b.AppendNull()
	}
	return nil
}

func toInt(v any) int64 {
	switch x := v.(type) {
	case int64:
		return x
	case int32:
		return int64(x)
	case int:
		return int64(x)
	}
	return 0
}

func toFloat32(v any) float32 {
	switch x := v.(type) {
	case float32:
		return x
	case float64:
		return float32(x)
	}
	return 0
}

func toFloat64(v any) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case float32:
		return float64(x)
	}
	return 0
}

// toText renders an utf8 column's value from its driver Go type.
func toText(c column, v any) string {
	if c.dbType == "UNIQUEIDENTIFIER" {
		if b, ok := v.([]byte); ok && len(b) == 16 {
			return formatUUID(b)
		}
	}
	switch x := v.(type) {
	case string:
		return x
	case []byte:
		return string(x)
	case time.Time:
		return x.Format(time.RFC3339Nano)
	case bool:
		if x {
			return "true"
		}
		return "false"
	}
	return fmt.Sprint(v)
}

func toBytes(v any) []byte {
	switch x := v.(type) {
	case []byte:
		return append([]byte(nil), x...)
	case string:
		return []byte(x)
	}
	return nil
}

func toTime(v any) time.Time {
	if t, ok := v.(time.Time); ok {
		return t
	}
	return time.Time{}
}

func toDecimal(v any, precision, scale int32) (decimal128.Num, error) {
	var s string
	switch x := v.(type) {
	case []byte:
		s = string(x)
	case string:
		s = x
	default:
		s = fmt.Sprint(v)
	}
	return decimal128.FromString(s, precision, scale)
}

func nanosSinceMidnight(t time.Time) int64 {
	return int64(t.Hour())*3_600_000_000_000 +
		int64(t.Minute())*60_000_000_000 +
		int64(t.Second())*1_000_000_000 +
		int64(t.Nanosecond())
}

// formatUUID renders 16 canonical-order bytes as a GUID string.
func formatUUID(b []byte) string {
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
