package postgresql

import (
	"math/big"
	"testing"

	"github.com/apache/arrow-go/v18/arrow"
	"github.com/galileostudio/baleia/internal/queryengine"
	"github.com/jackc/pgx/v5/pgtype"
)

func bigInt(s string) *big.Int {
	n, ok := new(big.Int).SetString(s, 10)
	if !ok {
		panic("bad int " + s)
	}
	return n
}

func TestArrowType(t *testing.T) {
	numeric := func(p, s int32) int32 { return 4 + (p << 16) + s }
	cases := []struct {
		name   string
		oid    uint32
		typmod int32
		want   arrow.DataType
	}{
		{"bool", oidBool, -1, arrow.FixedWidthTypes.Boolean},
		{"int2", oidInt2, -1, arrow.PrimitiveTypes.Int16},
		{"int4", oidInt4, -1, arrow.PrimitiveTypes.Int32},
		{"int8", oidInt8, -1, arrow.PrimitiveTypes.Int64},
		{"float4", oidFloat4, -1, arrow.PrimitiveTypes.Float32},
		{"float8", oidFloat8, -1, arrow.PrimitiveTypes.Float64},
		{"text", oidText, -1, arrow.BinaryTypes.String},
		{"varchar", oidVarchar, 14, arrow.BinaryTypes.String},
		{"uuid", oidUUID, -1, arrow.BinaryTypes.String},
		{"json", oidJSON, -1, arrow.BinaryTypes.String},
		{"jsonb", oidJSONB, -1, arrow.BinaryTypes.String},
		{"bytea", oidBytea, -1, arrow.BinaryTypes.Binary},
		{"date", oidDate, -1, arrow.FixedWidthTypes.Date32},
		{"timestamp", oidTimestamp, -1, &arrow.TimestampType{Unit: arrow.Microsecond}},
		{"timestamptz", oidTimestamptz, -1, &arrow.TimestampType{Unit: arrow.Microsecond, TimeZone: "UTC"}},
		{"time", oidTime, -1, &arrow.Time64Type{Unit: arrow.Microsecond}},
		{"interval", oidInterval, -1, arrow.BinaryTypes.String},
		{"numeric(38,10)", oidNumeric, numeric(38, 10), &arrow.Decimal128Type{Precision: 38, Scale: 10}},
		{"numeric(10,2)", oidNumeric, numeric(10, 2), &arrow.Decimal128Type{Precision: 10, Scale: 2}},
		{"numeric unconstrained", oidNumeric, -1, arrow.BinaryTypes.String},
		{"unknown", 999999, -1, arrow.BinaryTypes.String},
	}
	for _, c := range cases {
		got, _, _ := arrowType(c.oid, c.typmod)
		if !arrow.TypeEqual(got, c.want) {
			t.Errorf("%s: arrowType = %s, want %s", c.name, got, c.want)
		}
	}
}

func TestNumericPrecScale(t *testing.T) {
	if p, s, ok := numericPrecScale(4 + (38 << 16) + 10); !ok || p != 38 || s != 10 {
		t.Errorf("numeric(38,10): p=%d s=%d ok=%v", p, s, ok)
	}
	if _, _, ok := numericPrecScale(-1); ok {
		t.Error("unconstrained numeric decoded a precision")
	}
}

func TestNumericString(t *testing.T) {
	cases := []struct {
		name string
		n    pgtype.Numeric
		want string
	}{
		{"scale", pgtype.Numeric{Int: bigInt("1234"), Exp: -2, Valid: true}, "12.34"},
		{"integer", pgtype.Numeric{Int: bigInt("42"), Exp: 0, Valid: true}, "42"},
		{"padding", pgtype.Numeric{Int: bigInt("5"), Exp: -3, Valid: true}, "0.005"},
		{"expand", pgtype.Numeric{Int: bigInt("7"), Exp: 2, Valid: true}, "700"},
		{"negative", pgtype.Numeric{Int: bigInt("-1234"), Exp: -2, Valid: true}, "-12.34"},
		{"unconstrained", pgtype.Numeric{Int: bigInt("12345678901234567890123456789"), Exp: -9, Valid: true}, "12345678901234567890.123456789"},
	}
	for _, c := range cases {
		if got := numericString(c.n); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}

func TestFormatUUID(t *testing.T) {
	var b [16]byte
	for i := range b {
		b[i] = byte(i)
	}
	if got := formatUUID(b); got != "00010203-0405-0607-0809-0a0b0c0d0e0f" {
		t.Errorf("formatUUID = %q", got)
	}
}

func TestIsoInterval(t *testing.T) {
	cases := []struct {
		name string
		i    pgtype.Interval
		want string
	}{
		{"day hour", pgtype.Interval{Months: 0, Days: 1, Microseconds: 7200000000, Valid: true}, "P1DT2H"},
		{"minute second", pgtype.Interval{Microseconds: 90000000, Valid: true}, "PT1M30S"},
		{"month", pgtype.Interval{Months: 2, Valid: true}, "P2M"},
	}
	for _, c := range cases {
		if got := isoInterval(c.i); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}

func TestSQLState(t *testing.T) {
	cases := map[string]queryengine.ErrorCode{
		"42601": queryengine.CodeSyntax,
		"42804": queryengine.CodeSyntax,
		"42P01": queryengine.CodeNotFound,
		"42703": queryengine.CodeNotFound,
		"3D000": queryengine.CodeNotFound,
		"42501": queryengine.CodePermission,
		"25006": queryengine.CodePermission,
		"57014": queryengine.CodeCancelled,
		"40001": queryengine.CodeConflict,
		"53300": queryengine.CodeResource,
		"08006": queryengine.CodeUnavailable,
		"XX000": queryengine.CodeInternal,
	}
	for code, want := range cases {
		if got := sqlState(code); got != want {
			t.Errorf("%s: %s, want %s", code, got, want)
		}
	}
}

func TestApplicationName(t *testing.T) {
	if got := applicationName(queryengine.Tags{}); got != "baleia" {
		t.Errorf("no tags: %q", got)
	}
	if got := applicationName(queryengine.Tags{Source: "editor", ExecutionID: "e1"}); got != "baleia:editor:e1" {
		t.Errorf("tags: %q", got)
	}
	long := applicationName(queryengine.Tags{Source: "editor", ExecutionID: string(make([]byte, 200))})
	if len(long) > 63 {
		t.Errorf("application name not truncated: %d bytes", len(long))
	}
}

func TestBackendPID(t *testing.T) {
	if _, err := backendPID(queryengine.QueryHandle{}); err == nil {
		t.Error("a handle without backend_pid did not fail")
	}
	pid, err := backendPID(queryengine.QueryHandle{Extra: map[string]string{"backend_pid": "4242"}})
	if err != nil || pid != 4242 {
		t.Errorf("pid=%d err=%v", pid, err)
	}
}
