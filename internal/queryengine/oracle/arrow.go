package oracle

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/apache/arrow-go/v18/arrow"
	"github.com/apache/arrow-go/v18/arrow/array"
	"github.com/apache/arrow-go/v18/arrow/decimal128"
	"github.com/apache/arrow-go/v18/arrow/memory"
)

// column is one result column: name, Arrow type and the Oracle type it came
// from (for value rendering, decimal scale and the LOB cap).
type column struct {
	name      string
	typ       arrow.DataType
	dbType    string
	precision int32
	scale     int32
	lob       bool
}

// schemaOf builds the Arrow schema and the column plans from the driver's
// column types.
func schemaOf(names []string, cts []*sql.ColumnType) (*arrow.Schema, []column) {
	fields := make([]arrow.Field, len(names))
	cols := make([]column, len(names))
	for i, ct := range cts {
		dbType := ct.DatabaseTypeName()
		t, p, s := oracleArrowType(ct, dbType)
		lob := dbType == "LongVarChar" || dbType == "LongRaw"
		f := arrow.Field{Name: names[i], Type: t, Nullable: true}
		if lob {
			f.Metadata = arrow.MetadataFrom(map[string]string{"baleia.lob": "true"})
		}
		fields[i] = f
		cols[i] = column{name: names[i], typ: t, dbType: dbType, precision: p, scale: s, lob: lob}
	}
	return arrow.NewSchema(fields, nil), cols
}

// withLobTruncation returns a schema whose LOB fields carry the truncation flag.
func withLobTruncation(base *arrow.Schema, cols []column) *arrow.Schema {
	fields := make([]arrow.Field, base.NumFields())
	for i := range fields {
		f := base.Field(i)
		if cols[i].lob {
			f.Metadata = arrow.MetadataFrom(map[string]string{
				"baleia.lob":           "true",
				"baleia.lob_truncated": "true",
			})
		}
		fields[i] = f
	}
	return arrow.NewSchema(fields, nil)
}

// oracleArrowType maps an Oracle column type to an Arrow type. Unknown types
// fall back to utf8.
func oracleArrowType(ct *sql.ColumnType, dbType string) (arrow.DataType, int32, int32) {
	switch dbType {
	case "NUMBER":
		if p64, s64, ok := ct.DecimalSize(); ok && p64 >= 1 && p64 <= 38 && s64 >= 0 && s64 <= p64 {
			p, s := int32(p64), int32(s64)
			return &arrow.Decimal128Type{Precision: p, Scale: s}, p, s
		}
		// NUMBER without a scale: exact decimal text, never float64.
		return arrow.BinaryTypes.String, 0, 0
	case "IBFloat":
		return arrow.PrimitiveTypes.Float32, 0, 0
	case "IBDouble":
		return arrow.PrimitiveTypes.Float64, 0, 0
	case "DATE":
		// Oracle DATE carries a time to the second.
		return &arrow.TimestampType{Unit: arrow.Second}, 0, 0
	case "TimeStampDTY":
		return &arrow.TimestampType{Unit: arrow.Microsecond}, 0, 0
	case "TimeStampTZ_DTY", "TimeStampLTZ_DTY":
		return &arrow.TimestampType{Unit: arrow.Microsecond, TimeZone: "UTC"}, 0, 0
	case "RAW", "LongRaw":
		return arrow.BinaryTypes.Binary, 0, 0
	default:
		// VARCHAR2, NVARCHAR2, CHAR, NCHAR, CLOB, NCLOB, INTERVAL, ROWID, JSON.
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
	case *array.Float32Builder:
		bb.Append(toFloat32(v))
	case *array.Float64Builder:
		bb.Append(toFloat64(v))
	case *array.StringBuilder:
		bb.Append(toString(v))
	case *array.BinaryBuilder:
		bb.Append(toBytes(v))
	case *array.TimestampBuilder:
		bb.Append(arrow.Timestamp(timestampValue(c, toTime(v))))
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

// timestampValue is the unit-appropriate value of a timestamp column.
func timestampValue(c column, t time.Time) int64 {
	if tt, ok := c.typ.(*arrow.TimestampType); ok && tt.Unit == arrow.Second {
		return t.Unix()
	}
	return t.UnixMicro()
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
	case string:
		return x
	case []byte:
		return string(x)
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
	case string:
		s = x
	case []byte:
		s = string(x)
	default:
		s = fmt.Sprint(v)
	}
	return decimal128.FromString(s, precision, scale)
}
