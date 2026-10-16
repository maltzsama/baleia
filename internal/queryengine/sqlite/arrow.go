package sqlite

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/apache/arrow-go/v18/arrow"
	"github.com/apache/arrow-go/v18/arrow/array"
	"github.com/apache/arrow-go/v18/arrow/memory"
)

// affinity is a column's declared type affinity (SQLite's five rules).
type affinity uint8

const (
	affInteger affinity = iota
	affReal
	affText
	affBlob
	affNumeric
)

// affinityOf applies SQLite's affinity rules to a declared type, in order:
// INT; CHAR/CLOB/TEXT; BLOB or empty; REAL/FLOA/DOUB; else NUMERIC.
func affinityOf(decl string) affinity {
	t := strings.ToUpper(decl)
	switch {
	case strings.Contains(t, "INT"):
		return affInteger
	case strings.Contains(t, "CHAR"), strings.Contains(t, "CLOB"), strings.Contains(t, "TEXT"):
		return affText
	case strings.Contains(t, "BLOB"), t == "":
		return affBlob
	case strings.Contains(t, "REAL"), strings.Contains(t, "FLOA"), strings.Contains(t, "DOUB"):
		return affReal
	default:
		return affNumeric
	}
}

// storageClass is the runtime class of one scanned value as Go types render
// it: INTEGER, REAL, TEXT, BLOB or NULL.
type storageClass uint8

const (
	classNull storageClass = iota
	classInt
	classReal
	classText
	classBlob
)

func classOf(v any) storageClass {
	switch v.(type) {
	case nil:
		return classNull
	case int64:
		return classInt
	case float64:
		return classReal
	case []byte:
		return classBlob
	default:
		// string, and time.Time from the driver's date parsing.
		return classText
	}
}

// column is one result column: name and Arrow type.
type column struct{ name string }

// schemaOf fixes the Arrow schema from each column's declared affinity and the
// storage classes actually observed. A value whose storage class contradicts
// the affinity makes the whole column utf8, flagged in its metadata.
func schemaOf(names, decls []string, rows [][]any) (*arrow.Schema, []column) {
	fields := make([]arrow.Field, len(names))
	cols := make([]column, len(names))
	for i := range names {
		typ, meta := resolveColumn(decls[i], observedClasses(rows, i))
		fields[i] = arrow.Field{Name: names[i], Type: typ, Nullable: true, Metadata: meta}
		cols[i] = column{name: names[i]}
	}
	return arrow.NewSchema(fields, nil), cols
}

// observedClasses collects the non-null storage classes of column i.
func observedClasses(rows [][]any, i int) map[storageClass]bool {
	seen := map[storageClass]bool{}
	for _, row := range rows {
		if c := classOf(row[i]); c != classNull {
			seen[c] = true
		}
	}
	return seen
}

func resolveColumn(decl string, observed map[storageClass]bool) (arrow.DataType, arrow.Metadata) {
	decl = strings.TrimSpace(decl)
	if decl == "" {
		// An expression has no declared type: infer from the values.
		return inferColumn(observed)
	}
	switch affinityOf(decl) {
	case affInteger:
		if only(observed, classInt) {
			return arrow.PrimitiveTypes.Int64, arrow.Metadata{}
		}
	case affReal:
		if only(observed, classReal) {
			return arrow.PrimitiveTypes.Float64, arrow.Metadata{}
		}
	case affText:
		if only(observed, classText) {
			return arrow.BinaryTypes.String, arrow.Metadata{}
		}
	case affBlob:
		if only(observed, classBlob) {
			return arrow.BinaryTypes.Binary, arrow.Metadata{}
		}
	default: // NUMERIC affinity: exact text, no precision is kept.
		return arrow.BinaryTypes.String, arrow.MetadataFrom(map[string]string{"baleia.native_type": decl})
	}
	// The storage class contradicts the affinity: fall back to utf8.
	return arrow.BinaryTypes.String, arrow.MetadataFrom(map[string]string{"baleia.mixed_types": "true"})
}

// inferColumn maps a column without a declared type from the value classes.
func inferColumn(observed map[storageClass]bool) (arrow.DataType, arrow.Metadata) {
	if len(observed) == 1 {
		for c := range observed {
			switch c {
			case classInt:
				return arrow.PrimitiveTypes.Int64, arrow.Metadata{}
			case classReal:
				return arrow.PrimitiveTypes.Float64, arrow.Metadata{}
			case classBlob:
				return arrow.BinaryTypes.Binary, arrow.Metadata{}
			case classText:
				return arrow.BinaryTypes.String, arrow.Metadata{}
			}
		}
	}
	// All null, or several classes: utf8.
	if len(observed) > 1 {
		return arrow.BinaryTypes.String, arrow.MetadataFrom(map[string]string{"baleia.mixed_types": "true"})
	}
	return arrow.BinaryTypes.String, arrow.Metadata{}
}

// only reports whether every observed class is want (an empty set passes).
func only(observed map[storageClass]bool, want storageClass) bool {
	for c := range observed {
		if c != want {
			return false
		}
	}
	return true
}

// buildRecord builds one Arrow record from buffered rows. The caller owns the
// record and must release it.
func buildRecord(schema *arrow.Schema, _ []column, values [][]any) (arrow.RecordBatch, error) {
	b := array.NewRecordBuilder(memory.DefaultAllocator, schema)
	for _, row := range values {
		for c := range schema.Fields() {
			if err := appendValue(b.Field(c), schema.Field(c).Type, row[c]); err != nil {
				b.Release()
				return nil, err
			}
		}
	}
	rec := b.NewRecordBatch()
	b.Release()
	return rec, nil
}

func appendValue(b array.Builder, t arrow.DataType, v any) error {
	if v == nil {
		b.AppendNull()
		return nil
	}
	switch bb := b.(type) {
	case *array.Int64Builder:
		n, ok := v.(int64)
		if !ok {
			return fmt.Errorf("column value %T is not an integer", v)
		}
		bb.Append(n)
	case *array.Float64Builder:
		f, ok := v.(float64)
		if !ok {
			return fmt.Errorf("column value %T is not a real", v)
		}
		bb.Append(f)
	case *array.BinaryBuilder:
		raw, ok := v.([]byte)
		if !ok {
			return fmt.Errorf("column value %T is not a blob", v)
		}
		bb.Append(append([]byte(nil), raw...))
	case *array.StringBuilder:
		bb.Append(renderText(v))
	default:
		return fmt.Errorf("unsupported Arrow type %s", t)
	}
	return nil
}

// renderText renders a value of any storage class as its exact text.
func renderText(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case int64:
		return strconv.FormatInt(x, 10)
	case float64:
		return strconv.FormatFloat(x, 'g', -1, 64)
	case []byte:
		return string(x)
	case time.Time:
		return x.UTC().Format(time.RFC3339Nano)
	default:
		return fmt.Sprint(x)
	}
}

// cloneRow copies a scanned row so a driver byte slice reused by the next row
// cannot corrupt a buffered value.
func cloneRow(row []any) []any {
	out := make([]any, len(row))
	for i, v := range row {
		if b, ok := v.([]byte); ok {
			out[i] = append([]byte(nil), b...)
			continue
		}
		out[i] = v
	}
	return out
}
