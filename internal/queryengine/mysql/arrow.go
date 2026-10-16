package mysql

import (
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/apache/arrow-go/v18/arrow"
	"github.com/apache/arrow-go/v18/arrow/array"
	"github.com/apache/arrow-go/v18/arrow/decimal128"
	"github.com/apache/arrow-go/v18/arrow/memory"
)

// column is one result column: name, Arrow type and the MySQL type it came
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
		t, p, s := mysqlArrowType(ct)
		fields[i] = arrow.Field{Name: names[i], Type: t, Nullable: true}
		cols[i] = column{name: names[i], typ: t, dbType: ct.DatabaseTypeName(), precision: p, scale: s}
	}
	return arrow.NewSchema(fields, nil), cols
}

// mysqlArrowType maps a MySQL column type to an Arrow type. Unknown types fall
// back to utf8.
func mysqlArrowType(ct *sql.ColumnType) (arrow.DataType, int32, int32) {
	switch ct.DatabaseTypeName() {
	case "TINYINT":
		return arrow.PrimitiveTypes.Int8, 0, 0
	case "SMALLINT", "YEAR":
		return arrow.PrimitiveTypes.Int16, 0, 0
	case "MEDIUMINT", "INT":
		return arrow.PrimitiveTypes.Int32, 0, 0
	case "BIGINT":
		return arrow.PrimitiveTypes.Int64, 0, 0
	case "UNSIGNED TINYINT":
		return arrow.PrimitiveTypes.Uint8, 0, 0
	case "UNSIGNED SMALLINT":
		return arrow.PrimitiveTypes.Uint16, 0, 0
	case "UNSIGNED MEDIUMINT", "UNSIGNED INT":
		return arrow.PrimitiveTypes.Uint32, 0, 0
	case "UNSIGNED BIGINT":
		return arrow.PrimitiveTypes.Uint64, 0, 0
	case "FLOAT":
		return arrow.PrimitiveTypes.Float32, 0, 0
	case "DOUBLE":
		return arrow.PrimitiveTypes.Float64, 0, 0
	case "DECIMAL", "NEWDECIMAL":
		if p64, s64, ok := ct.DecimalSize(); ok && p64 <= 38 {
			p, s := int32(p64), int32(s64)
			return &arrow.Decimal128Type{Precision: p, Scale: s}, p, s
		}
		return arrow.BinaryTypes.String, 0, 0
	case "DATE":
		return arrow.FixedWidthTypes.Date32, 0, 0
	case "DATETIME":
		return &arrow.TimestampType{Unit: arrow.Microsecond}, 0, 0
	case "TIMESTAMP":
		return &arrow.TimestampType{Unit: arrow.Microsecond, TimeZone: "UTC"}, 0, 0
	case "TIME":
		return &arrow.Time64Type{Unit: arrow.Microsecond}, 0, 0
	case "BLOB", "TINYBLOB", "MEDIUMBLOB", "LONGBLOB", "BINARY", "VARBINARY", "BIT":
		return arrow.BinaryTypes.Binary, 0, 0
	default:
		// VARCHAR, CHAR, TEXT, ENUM, SET, JSON and anything else render as utf8.
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
	case *array.Int8Builder:
		bb.Append(int8(toInt(v)))
	case *array.Int16Builder:
		bb.Append(int16(toInt(v)))
	case *array.Int32Builder:
		bb.Append(int32(toInt(v)))
	case *array.Int64Builder:
		bb.Append(toInt(v))
	case *array.Uint8Builder:
		bb.Append(uint8(toUint(v)))
	case *array.Uint16Builder:
		bb.Append(uint16(toUint(v)))
	case *array.Uint32Builder:
		bb.Append(uint32(toUint(v)))
	case *array.Uint64Builder:
		bb.Append(toUint(v))
	case *array.Float32Builder:
		bb.Append(toFloat32(v))
	case *array.Float64Builder:
		bb.Append(toFloat64(v))
	case *array.StringBuilder:
		bb.Append(toString(v))
	case *array.BinaryBuilder:
		bb.Append(toBytes(v))
	case *array.Date32Builder:
		bb.Append(arrow.Date32FromTime(toTime(v)))
	case *array.TimestampBuilder:
		bb.Append(arrow.Timestamp(toTime(v).UnixMicro()))
	case *array.Time64Builder:
		us, err := toTimeMicros(v)
		if err != nil {
			return fmt.Errorf("time column %q: %w", c.name, err)
		}
		bb.Append(arrow.Time64(us))
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
	case uint64:
		return int64(x)
	case int32:
		return int64(x)
	case int:
		return int64(x)
	case []byte:
		n, _ := strconv.ParseInt(string(x), 10, 64)
		return n
	}
	return 0
}

func toUint(v any) uint64 {
	switch x := v.(type) {
	case uint64:
		return x
	case int64:
		return uint64(x)
	case []byte:
		n, _ := strconv.ParseUint(string(x), 10, 64)
		return n
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

func toString(v any) string {
	switch x := v.(type) {
	case []byte:
		return string(x)
	case string:
		return x
	case time.Time:
		return x.Format(time.RFC3339Nano)
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

// toTimeMicros parses a MySQL TIME value ("[-]HH:MM:SS[.ffffff]") into
// microseconds since midnight.
func toTimeMicros(v any) (int64, error) {
	var s string
	switch x := v.(type) {
	case []byte:
		s = string(x)
	case string:
		s = x
	default:
		return 0, fmt.Errorf("time value is %T", v)
	}
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	parts := strings.Split(s, ":")
	if len(parts) != 3 {
		return 0, fmt.Errorf("cannot parse time %q", s)
	}
	h, err1 := strconv.Atoi(parts[0])
	m, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil {
		return 0, fmt.Errorf("cannot parse time %q", s)
	}
	secStr, frac := parts[2], ""
	if i := strings.IndexByte(parts[2], '.'); i >= 0 {
		secStr, frac = parts[2][:i], parts[2][i+1:]
	}
	sec, err := strconv.Atoi(secStr)
	if err != nil {
		return 0, fmt.Errorf("cannot parse time %q", s)
	}
	micros := int64(h)*3_600_000_000 + int64(m)*60_000_000 + int64(sec)*1_000_000
	if frac != "" {
		for len(frac) < 6 {
			frac += "0"
		}
		f, err := strconv.Atoi(frac[:6])
		if err != nil {
			return 0, fmt.Errorf("cannot parse time fraction %q", frac)
		}
		micros += int64(f)
	}
	if neg {
		micros = -micros
	}
	return micros, nil
}
