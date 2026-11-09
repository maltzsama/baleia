package postgresql

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/apache/arrow-go/v18/arrow"
	"github.com/apache/arrow-go/v18/arrow/array"
	"github.com/apache/arrow-go/v18/arrow/decimal128"
	"github.com/apache/arrow-go/v18/arrow/memory"
	"github.com/galileostudio/baleia/internal/db"
	"github.com/jackc/pgx/v5/pgconn"
)

// PostgreSQL type OIDs the adapter maps explicitly (pg_catalog).
const (
	oidBool        = 16
	oidBytea       = 17
	oidName        = 19
	oidInt8        = 20
	oidInt2        = 21
	oidInt4        = 23
	oidText        = 25
	oidJSON        = 114
	oidFloat4      = 700
	oidFloat8      = 701
	oidBpchar      = 1042
	oidVarchar     = 1043
	oidDate        = 1082
	oidTime        = 1083
	oidTimestamp   = 1114
	oidTimestamptz = 1184
	oidInterval    = 1186
	oidNumeric     = 1700
	oidUUID        = 2950
	oidJSONB       = 3802
)

// column is one result column: name, Arrow type and the PostgreSQL type it
// came from (for value rendering and decimal scale).
type column struct {
	name      string
	typ       arrow.DataType
	oid       uint32
	precision int32
	scale     int32
}

// schemaOf builds the Arrow schema and the column plans from the field
// descriptions of a result.
func schemaOf(fds []pgconn.FieldDescription) (*arrow.Schema, []column, error) {
	fields := make([]arrow.Field, len(fds))
	cols := make([]column, len(fds))
	for i, fd := range fds {
		t, p, s := arrowType(fd.DataTypeOID, fd.TypeModifier)
		fields[i] = arrow.Field{Name: fd.Name, Type: t, Nullable: true}
		cols[i] = column{name: fd.Name, typ: t, oid: fd.DataTypeOID, precision: p, scale: s}
	}
	return arrow.NewSchema(fields, nil), cols, nil
}

// arrowType maps a PostgreSQL type OID (and type modifier) to an Arrow type.
// Unknown types fall back to utf8: the value renders with its text form.
func arrowType(oid uint32, typmod int32) (arrow.DataType, int32, int32) {
	switch oid {
	case oidBool:
		return arrow.FixedWidthTypes.Boolean, 0, 0
	case oidInt2:
		return arrow.PrimitiveTypes.Int16, 0, 0
	case oidInt4:
		return arrow.PrimitiveTypes.Int32, 0, 0
	case oidInt8:
		return arrow.PrimitiveTypes.Int64, 0, 0
	case oidFloat4:
		return arrow.PrimitiveTypes.Float32, 0, 0
	case oidFloat8:
		return arrow.PrimitiveTypes.Float64, 0, 0
	case oidBytea:
		return arrow.BinaryTypes.Binary, 0, 0
	case oidDate:
		return arrow.FixedWidthTypes.Date32, 0, 0
	case oidTimestamp:
		return &arrow.TimestampType{Unit: arrow.Microsecond}, 0, 0
	case oidTimestamptz:
		return &arrow.TimestampType{Unit: arrow.Microsecond, TimeZone: "UTC"}, 0, 0
	case oidTime:
		return &arrow.Time64Type{Unit: arrow.Microsecond}, 0, 0
	case oidNumeric:
		if p, s, ok := numericPrecScale(typmod); ok && p <= 38 {
			return &arrow.Decimal128Type{Precision: p, Scale: s}, p, s
		}
		return arrow.BinaryTypes.String, 0, 0
	default:
		// text, varchar, char, name, uuid, json, jsonb, interval and every
		// type without a native Arrow mapping render as utf8.
		return arrow.BinaryTypes.String, 0, 0
	}
}

// numericPrecScale decodes a numeric type modifier: PostgreSQL packs
// ((precision << 16) | scale) and adds VARHDRSZ (4). ok is false for an
// unconstrained numeric.
func numericPrecScale(typmod int32) (p, s int32, ok bool) {
	if typmod < 4 {
		return 0, 0, false
	}
	m := typmod - 4
	return m >> 16, m & 0xFFFF, true
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

// appendValue appends v (a pgx value) to the builder for its column.
func appendValue(b array.Builder, c column, v any) error {
	if v == nil {
		b.AppendNull()
		return nil
	}
	switch bb := b.(type) {
	case *array.BooleanBuilder:
		bb.Append(v.(bool))
	case *array.Int16Builder:
		bb.Append(v.(int16))
	case *array.Int32Builder:
		bb.Append(v.(int32))
	case *array.Int64Builder:
		bb.Append(v.(int64))
	case *array.Float32Builder:
		bb.Append(v.(float32))
	case *array.Float64Builder:
		bb.Append(v.(float64))
	case *array.BinaryBuilder:
		raw, _ := v.([]byte)
		bb.Append(append([]byte(nil), raw...))
	case *array.StringBuilder:
		bb.Append(stringValue(v, c.oid))
	case *array.Date32Builder:
		bb.Append(arrow.Date32FromTime(v.(time.Time)))
	case *array.TimestampBuilder:
		bb.Append(arrow.Timestamp(v.(time.Time).UnixMicro()))
	case *array.Time64Builder:
		micros, err := timeMicros(v)
		if err != nil {
			return fmt.Errorf("time column %q: %w", c.name, err)
		}
		bb.Append(arrow.Time64(micros))
	case *array.Decimal128Builder:
		decimal, ok := decimalString(v)
		if !ok {
			return fmt.Errorf("decimal column %q: value is %T", c.name, v)
		}
		d, err := decimal128.FromString(decimal, c.precision, c.scale)
		if err != nil {
			return fmt.Errorf("decimal column %q: %w", c.name, err)
		}
		bb.Append(d)
	default:
		b.AppendNull()
	}
	return nil
}

// stringValue renders an utf8 column's value from its pgx Go type. PostgreSQL
// types without a native Arrow mapping arrive as driver values; numeric and
// interval are rendered from their text form.
func stringValue(v any, oid uint32) string {
	switch x := v.(type) {
	case string:
		return x
	case [16]byte:
		return formatUUID(x)
	case map[string]any:
		return jsonString(x)
	case []any:
		return jsonString(x)
	case time.Time:
		return x.Format(time.RFC3339Nano)
	}
	if s, ok := textValue(v); ok {
		if oid == oidInterval {
			return isoInterval(parseInterval(s))
		}
		return s
	}
	return fmt.Sprint(v)
}

// textValue asks a driver.Valuer (pgx returns one for numeric, interval and
// time) for its text form, so Baleia never names a pgx type.
func textValue(v any) (string, bool) {
	valuer, ok := v.(driver.Valuer)
	if !ok {
		return "", false
	}
	value, err := valuer.Value()
	if err != nil {
		return "", false
	}
	switch x := value.(type) {
	case string:
		return x, true
	case []byte:
		return string(x), true
	}
	return fmt.Sprint(value), true
}

// decimalString renders a numeric value as a plain decimal string.
func decimalString(v any) (string, bool) {
	if s, ok := textValue(v); ok {
		return s, true
	}
	if n, ok := v.(db.Numeric); ok {
		return numericString(n), true
	}
	return "", false
}

// timeMicros converts a time value's text form to microseconds since midnight.
func timeMicros(v any) (int64, error) {
	s, ok := textValue(v)
	if !ok {
		return 0, fmt.Errorf("value is %T", v)
	}
	return clockMicros(s)
}

func clockMicros(s string) (int64, error) {
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	clock := strings.SplitN(s, ".", 2)
	hms := strings.Split(clock[0], ":")
	if len(hms) != 3 {
		return 0, fmt.Errorf("invalid time %q", s)
	}
	var total int64
	for i, mult := range []int64{3_600_000_000, 60_000_000, 1_000_000} {
		n, err := strconv.ParseInt(hms[i], 10, 64)
		if err != nil {
			return 0, fmt.Errorf("invalid time %q: %w", s, err)
		}
		total += n * mult
	}
	if len(clock) == 2 {
		frac := clock[1]
		if len(frac) > 6 {
			frac = frac[:6]
		}
		for len(frac) < 6 {
			frac += "0"
		}
		us, err := strconv.ParseInt(frac, 10, 64)
		if err != nil {
			return 0, fmt.Errorf("invalid time %q: %w", s, err)
		}
		total += us
	}
	if neg {
		total = -total
	}
	return total, nil
}

func jsonString(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(b)
}

func formatUUID(b [16]byte) string {
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// numericString renders a db.Numeric as a plain decimal string.
func numericString(n db.Numeric) string {
	if !n.Valid {
		return ""
	}
	if n.NaN {
		return "NaN"
	}
	switch n.InfinityModifier {
	case db.Infinity:
		return "Infinity"
	case db.NegativeInfinity:
		return "-Infinity"
	}
	if n.Int == nil {
		return "0"
	}
	digits := n.Int.String()
	neg := strings.HasPrefix(digits, "-")
	if neg {
		digits = digits[1:]
	}
	var s string
	if n.Exp >= 0 {
		s = digits + strings.Repeat("0", int(n.Exp))
	} else {
		scale := int(-n.Exp)
		if len(digits) <= scale {
			s = "0." + strings.Repeat("0", scale-len(digits)) + digits
		} else {
			s = digits[:len(digits)-scale] + "." + digits[len(digits)-scale:]
		}
	}
	if neg {
		s = "-" + s
	}
	return s
}

// parseInterval reads the text interval pgx renders: an optional "<n> mon",
// an optional "<n> day" and a [-]HH:MM:SS[.ffffff] clock.
func parseInterval(s string) db.Interval {
	i := db.Interval{Valid: true}
	fields := strings.Fields(s)
	for n, field := range fields {
		switch field {
		case "mon":
			if n > 0 {
				if v, err := strconv.ParseInt(fields[n-1], 10, 32); err == nil {
					i.Months = int32(v)
				}
			}
		case "day":
			if n > 0 {
				if v, err := strconv.ParseInt(fields[n-1], 10, 32); err == nil {
					i.Days = int32(v)
				}
			}
		default:
			if strings.Contains(field, ":") {
				if v, err := clockMicros(field); err == nil {
					i.Microseconds = v
				}
			}
		}
	}
	return i
}

// isoInterval renders a db.Interval as an ISO 8601 duration.
func isoInterval(i db.Interval) string {
	if !i.Valid {
		return ""
	}
	var b strings.Builder
	b.WriteByte('P')
	if i.Months != 0 {
		fmt.Fprintf(&b, "%dM", i.Months)
	}
	if i.Days != 0 {
		fmt.Fprintf(&b, "%dD", i.Days)
	}
	micros := i.Microseconds
	neg := micros < 0
	if neg {
		micros = -micros
	}
	h := micros / 3_600_000_000
	micros %= 3_600_000_000
	m := micros / 60_000_000
	micros %= 60_000_000
	sec := micros / 1_000_000
	us := micros % 1_000_000
	if h != 0 || m != 0 || sec != 0 || us != 0 || b.Len() == 1 {
		b.WriteByte('T')
		if neg {
			b.WriteByte('-')
		}
		if h != 0 {
			fmt.Fprintf(&b, "%dH", h)
		}
		if m != 0 {
			fmt.Fprintf(&b, "%dM", m)
		}
		switch {
		case us != 0:
			fmt.Fprintf(&b, "%d.%06dS", sec, us)
		case sec != 0:
			fmt.Fprintf(&b, "%dS", sec)
		case h == 0 && m == 0:
			b.WriteString("0S")
		}
	}
	return b.String()
}

// updateCount reports the affected-row count of a DML statement.
func updateCount(ct pgconn.CommandTag) (int64, bool) {
	if ct.Insert() || ct.Update() || ct.Delete() {
		return ct.RowsAffected(), true
	}
	return 0, false
}
