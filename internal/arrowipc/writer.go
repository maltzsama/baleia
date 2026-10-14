package arrowipc

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/apache/arrow-go/v18/arrow"
	"github.com/apache/arrow-go/v18/arrow/array"
	"github.com/apache/arrow-go/v18/arrow/decimal128"
	"github.com/apache/arrow-go/v18/arrow/ipc"
	"github.com/apache/arrow-go/v18/arrow/memory"
)

// recordBatchSize bounds a record batch. The writer flushes a batch every time
// it reaches this many rows so a huge result is never held in memory at once.
const recordBatchSize = 8192

// ColumnInfo describes a result column as reported by Trino.
type ColumnInfo struct {
	Name string
	Type string
}

// StreamWriter serializes Trino result rows to the Arrow IPC stream format,
// preserving exact types (decimal128, exact integers, timestamps with unit).
type StreamWriter struct {
	writer   *ipc.Writer
	builders []array.Builder
	types    []arrow.DataType
	names    []string
	schema   *arrow.Schema
	rowCount int
	total    int64
}

// NewStreamWriter builds a writer for the given columns. The stream header is
// written immediately so the caller can start appending rows as they arrive.
func NewStreamWriter(w io.Writer, cols []ColumnInfo) (*StreamWriter, error) {
	fields := make([]arrow.Field, len(cols))
	types := make([]arrow.DataType, len(cols))
	names := make([]string, len(cols))
	for i, c := range cols {
		typ, err := TrinoTypeToArrow(c.Type)
		if err != nil {
			return nil, fmt.Errorf("column %q: %w", c.Name, err)
		}
		types[i] = typ
		names[i] = c.Name
		fields[i] = arrow.Field{Name: c.Name, Type: typ, Nullable: true}
	}

	schema := arrow.NewSchema(fields, nil)
	pool := memory.NewGoAllocator()

	builders := make([]array.Builder, len(fields))
	for i := range fields {
		builders[i] = array.NewBuilder(pool, fields[i].Type)
	}

	s := &StreamWriter{
		writer:   ipc.NewWriter(w, ipc.WithSchema(schema), ipc.WithAllocator(pool)),
		builders: builders,
		types:    types,
		names:    names,
		schema:   schema,
	}
	return s, nil
}

// AppendRows appends rows to the current batch, flushing a record batch every
// recordBatchSize rows. It never holds more than one batch in memory.
func (s *StreamWriter) AppendRows(rows [][]interface{}) error {
	for _, row := range rows {
		if len(row) != len(s.builders) {
			return fmt.Errorf("row %d: got %d values, expected %d columns",
				s.total, len(row), len(s.builders))
		}
		for i, val := range row {
			if err := appendValue(s.builders[i], s.types[i], val); err != nil {
				return fmt.Errorf("column %q row %d: %w", s.names[i], s.total, err)
			}
		}
		s.rowCount++
		s.total++
		if s.rowCount >= recordBatchSize {
			if err := s.flushBatch(); err != nil {
				return err
			}
		}
	}
	return nil
}

// flushBatch writes the pending record and resets the builders for reuse.
func (s *StreamWriter) flushBatch() error {
	if s.rowCount == 0 {
		return nil
	}

	cols := make([]arrow.Array, len(s.builders))
	for i, b := range s.builders {
		cols[i] = b.NewArray()
	}
	rec := array.NewRecord(s.schema, cols, int64(s.rowCount))
	if err := s.writer.Write(rec); err != nil {
		return fmt.Errorf("write arrow batch: %w", err)
	}
	rec.Release()

	s.rowCount = 0
	return nil
}

// Close flushes the remainder and writes the end-of-stream marker.
func (s *StreamWriter) Close() error {
	if err := s.flushBatch(); err != nil {
		return err
	}
	return s.writer.Close()
}

// RowCount reports the total number of rows appended so far.
func (s *StreamWriter) RowCount() int64 { return s.total }

// TrinoTypeToArrow maps a Trino type name to the exact Arrow type used for
// storage. Look at the type table in issue #89 for the mapping and the rationale.
func TrinoTypeToArrow(t string) (arrow.DataType, error) {
	t = strings.ToLower(strings.TrimSpace(t))
	switch {
	case t == "boolean":
		return arrow.FixedWidthTypes.Boolean, nil
	case t == "tinyint":
		return arrow.PrimitiveTypes.Int8, nil
	case t == "smallint":
		return arrow.PrimitiveTypes.Int16, nil
	case t == "integer" || t == "int":
		return arrow.PrimitiveTypes.Int32, nil
	case t == "bigint":
		return arrow.PrimitiveTypes.Int64, nil
	case t == "real" || t == "float":
		return arrow.PrimitiveTypes.Float32, nil
	case t == "double":
		return arrow.PrimitiveTypes.Float64, nil
	case strings.HasPrefix(t, "decimal("):
		p, sc, err := parseDecimalParams(t)
		if err != nil {
			return nil, err
		}
		return &arrow.Decimal128Type{Precision: p, Scale: sc}, nil
	case strings.HasPrefix(t, "varchar"), strings.HasPrefix(t, "char"),
		t == "uuid", t == "ipaddress", t == "json",
		t == "time", strings.HasPrefix(t, "time("):
		return arrow.BinaryTypes.String, nil
	case t == "varbinary":
		return arrow.BinaryTypes.Binary, nil
	case t == "date":
		return arrow.FixedWidthTypes.Date32, nil
	case strings.HasPrefix(t, "timestamp"):
		unit, withTZ := parseTimestampInfo(t)
		if withTZ {
			return &arrow.TimestampType{Unit: unit, TimeZone: "UTC"}, nil
		}
		return &arrow.TimestampType{Unit: unit}, nil
	case strings.HasPrefix(t, "array("), strings.HasPrefix(t, "map("), strings.HasPrefix(t, "row("):
		return arrow.BinaryTypes.String, nil
	default:
		// Unknown types degrade to JSON text rather than silently losing data.
		return arrow.BinaryTypes.String, nil
	}
}

// parseDecimalParams extracts the precision and scale from "decimal(38,10)".
func parseDecimalParams(t string) (int32, int32, error) {
	inner := strings.TrimSuffix(strings.TrimPrefix(t, "decimal("), ")")
	parts := strings.Split(inner, ",")
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("cannot parse decimal type %q", t)
	}
	p, err := strconv.ParseInt(strings.TrimSpace(parts[0]), 10, 32)
	if err != nil {
		return 0, 0, fmt.Errorf("cannot parse decimal precision in %q", t)
	}
	sc, err := strconv.ParseInt(strings.TrimSpace(parts[1]), 10, 32)
	if err != nil {
		return 0, 0, fmt.Errorf("cannot parse decimal scale in %q", t)
	}
	if p < 1 || p > 38 {
		return 0, 0, fmt.Errorf("decimal precision %d out of range (1..38) in %q", p, t)
	}
	return int32(p), int32(sc), nil
}

// parseTimestampInfo derives the Arrow time unit and whether the type carries a
// time zone. Trino's default timestamp precision is 6 (microseconds).
func parseTimestampInfo(t string) (arrow.TimeUnit, bool) {
	withTZ := strings.Contains(t, "with time zone")
	openP := strings.Index(t, "(")
	closeP := strings.Index(t, ")")
	if openP < 0 || closeP <= openP {
		return arrow.Microsecond, withTZ
	}
	p, err := strconv.Atoi(strings.TrimSpace(t[openP+1 : closeP]))
	if err != nil {
		return arrow.Microsecond, withTZ
	}
	switch {
	case p <= 3:
		return arrow.Millisecond, withTZ
	case p <= 6:
		return arrow.Microsecond, withTZ
	default:
		return arrow.Nanosecond, withTZ
	}
}

// appendValue appends one cell to its builder according to the column's Arrow
// type. A value that cannot be parsed for its declared type is an error,
// never a silent zero.
func appendValue(b array.Builder, typ arrow.DataType, val interface{}) error {
	if val == nil {
		b.AppendNull()
		return nil
	}

	switch t := typ.(type) {
	case *arrow.BooleanType:
		v, err := toBool(val)
		if err != nil {
			return err
		}
		b.(*array.BooleanBuilder).Append(v)

	case *arrow.Int8Type:
		n, err := toInt64Exact(val)
		if err != nil {
			return err
		}
		b.(*array.Int8Builder).Append(int8(n))
	case *arrow.Int16Type:
		n, err := toInt64Exact(val)
		if err != nil {
			return err
		}
		b.(*array.Int16Builder).Append(int16(n))
	case *arrow.Int32Type:
		n, err := toInt64Exact(val)
		if err != nil {
			return err
		}
		b.(*array.Int32Builder).Append(int32(n))
	case *arrow.Int64Type:
		n, err := toInt64Exact(val)
		if err != nil {
			return err
		}
		b.(*array.Int64Builder).Append(n)

	case *arrow.Float32Type:
		f, err := toFloat64Exact(val)
		if err != nil {
			return err
		}
		b.(*array.Float32Builder).Append(float32(f))
	case *arrow.Float64Type:
		f, err := toFloat64Exact(val)
		if err != nil {
			return err
		}
		b.(*array.Float64Builder).Append(f)

	case *arrow.Decimal128Type:
		s, ok := val.(string)
		if !ok {
			return fmt.Errorf("cannot parse %v as decimal", val)
		}
		num, err := decimal128.FromString(s, t.Precision, t.Scale)
		if err != nil {
			return fmt.Errorf("cannot parse %q as decimal(%d,%d)", s, t.Precision, t.Scale)
		}
		b.(*array.Decimal128Builder).Append(num)

	case *arrow.Date32Type:
		s, ok := val.(string)
		if !ok {
			return fmt.Errorf("cannot parse %v as date", val)
		}
		days, err := toDate32(s)
		if err != nil {
			return err
		}
		b.(*array.Date32Builder).Append(arrow.Date32(days))

	case *arrow.TimestampType:
		s, ok := val.(string)
		if !ok {
			return fmt.Errorf("cannot parse %v as timestamp", val)
		}
		v, err := toTimestamp(s, t)
		if err != nil {
			return err
		}
		b.(*array.TimestampBuilder).Append(v)

	case *arrow.BinaryType:
		s, ok := val.(string)
		if !ok {
			return fmt.Errorf("cannot parse %v as varbinary", val)
		}
		raw, err := base64.StdEncoding.DecodeString(s)
		if err != nil {
			return fmt.Errorf("cannot base64-decode varbinary %q", s)
		}
		b.(*array.BinaryBuilder).Append(raw)

	case *arrow.StringType:
		text, err := toJSONString(val)
		if err != nil {
			return err
		}
		b.(*array.StringBuilder).Append(text)

	default:
		return fmt.Errorf("unsupported arrow type %s", typ)
	}
	return nil
}

// toInt64Exact decodes an exact integer. With UseNumber, Trino integers arrive
// as json.Number so bigint precision is never lost.
func toInt64Exact(val interface{}) (int64, error) {
	switch v := val.(type) {
	case json.Number:
		return v.Int64()
	case string:
		return strconv.ParseInt(v, 10, 64)
	default:
		return 0, fmt.Errorf("cannot parse %v as an integer", val)
	}
}

// toFloat64Exact decodes a float. Trino sends reals as JSON numbers or the
// literal strings NaN, Infinity and -Infinity.
func toFloat64Exact(val interface{}) (float64, error) {
	switch v := val.(type) {
	case json.Number:
		return v.Float64()
	case string:
		switch v {
		case "NaN":
			return math.NaN(), nil
		case "Infinity":
			return math.Inf(1), nil
		case "-Infinity":
			return math.Inf(-1), nil
		}
		return strconv.ParseFloat(v, 64)
	default:
		return 0, fmt.Errorf("cannot parse %v as a float", val)
	}
}

func toBool(val interface{}) (bool, error) {
	switch v := val.(type) {
	case bool:
		return v, nil
	case string:
		return strconv.ParseBool(v)
	default:
		return false, fmt.Errorf("cannot parse %v as a boolean", val)
	}
}

// toDate32 converts a Trino date string to days since the epoch using floor
// division, which is correct for dates before 1970.
func toDate32(s string) (int32, error) {
	t, err := time.Parse("2006-01-02", strings.TrimSpace(s))
	if err != nil {
		return 0, fmt.Errorf("cannot parse %q as a date", s)
	}
	unix := t.Unix()
	days := unix / 86400
	if unix < 0 && unix%86400 != 0 {
		days--
	}
	return int32(days), nil
}

// toTimestamp converts a Trino timestamp string to the Arrow value for the
// declared unit. Types carrying a time zone are converted to UTC.
func toTimestamp(s string, t *arrow.TimestampType) (arrow.Timestamp, error) {
	s = strings.TrimSpace(s)
	if t.TimeZone != "" {
		utc, err := parseTimestampWithZone(s)
		if err != nil {
			return 0, fmt.Errorf("cannot parse %q as a timestamp with time zone", s)
		}
		return timestampAt(utc.UTC(), t.Unit), nil
	}
	tm, err := time.Parse("2006-01-02 15:04:05.999999999", s)
	if err != nil {
		return 0, fmt.Errorf("cannot parse %q as a timestamp", s)
	}
	return timestampAt(tm, t.Unit), nil
}

func timestampAt(t time.Time, unit arrow.TimeUnit) arrow.Timestamp {
	switch unit {
	case arrow.Millisecond:
		return arrow.Timestamp(t.UnixMilli())
	case arrow.Second:
		return arrow.Timestamp(t.Unix())
	case arrow.Nanosecond:
		return arrow.Timestamp(t.UnixNano())
	default:
		return arrow.Timestamp(t.UnixMicro())
	}
}

// parseTimestampWithZone accepts Trino's "<datetime> <offset>" and
// "<datetime> <location>" forms.
func parseTimestampWithZone(s string) (time.Time, error) {
	if t, err := time.Parse("2006-01-02 15:04:05.999999999 -07:00", s); err == nil {
		return t, nil
	}

	idx := strings.LastIndex(s, " ")
	if idx < 0 {
		return time.Time{}, errors.New("missing time zone")
	}
	dtPart := strings.TrimSpace(s[:idx])
	zone := strings.TrimSpace(s[idx+1:])

	tm, err := time.Parse("2006-01-02 15:04:05.999999999", dtPart)
	if err != nil {
		return time.Time{}, err
	}
	if strings.HasPrefix(zone, "+") || strings.HasPrefix(zone, "-") {
		return time.Parse("2006-01-02 15:04:05.999999999 -07:00", dtPart+" "+zone)
	}
	loc, err := time.LoadLocation(zone)
	if err != nil {
		return time.Time{}, fmt.Errorf("unknown time zone %q", zone)
	}
	return time.Date(tm.Year(), tm.Month(), tm.Day(), tm.Hour(), tm.Minute(), tm.Second(), tm.Nanosecond(), loc), nil
}

// toJSONString renders a cell as text: strings stay as-is, structured values
// (array/map/row) are marshaled to JSON so Go map syntax never leaks into
// results.
func toJSONString(val interface{}) (string, error) {
	if s, ok := val.(string); ok {
		return s, nil
	}
	data, err := json.Marshal(val)
	if err != nil {
		return "", fmt.Errorf("cannot encode %v as JSON text", val)
	}
	return string(data), nil
}
