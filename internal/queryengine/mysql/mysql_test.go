package mysql

import (
	"testing"

	"github.com/galileostudio/baleia/internal/queryengine"
)

func TestToTimeMicros(t *testing.T) {
	cases := []struct {
		in   string
		want int64
	}{
		{"13:45:30.123456", 13*3_600_000_000 + 45*60_000_000 + 30*1_000_000 + 123456},
		{"00:00:00", 0},
		{"-01:00:00", -3_600_000_000},
		{"100:00:00", 100 * 3_600_000_000},
		{"12:30:00.5", 12*3_600_000_000 + 30*60_000_000 + 500000},
	}
	for _, c := range cases {
		got, err := toTimeMicros([]byte(c.in))
		if err != nil || got != c.want {
			t.Errorf("%q: %d (%v), want %d", c.in, got, err, c.want)
		}
	}
	if _, err := toTimeMicros("nope"); err == nil {
		t.Error("a malformed time parsed")
	}
}

func TestCloneRowCopiesBytes(t *testing.T) {
	shared := []byte("abc")
	row := []any{int64(1), shared}
	out := cloneRow(row)
	shared[0] = 'z'
	if got := string(out[1].([]byte)); got != "abc" {
		t.Errorf("clone aliases the driver buffer: %q", got)
	}
	if out[0].(int64) != 1 {
		t.Errorf("non-byte value changed: %v", out[0])
	}
}

func TestMySQLCode(t *testing.T) {
	cases := map[uint16]queryengine.ErrorCode{
		1064: queryengine.CodeSyntax,
		1146: queryengine.CodeNotFound,
		1045: queryengine.CodePermission,
		1317: queryengine.CodeCancelled,
		3024: queryengine.CodeTimeout,
		1213: queryengine.CodeConflict,
		2003: queryengine.CodeUnavailable,
		9999: queryengine.CodeInternal,
	}
	for n, want := range cases {
		if got := mysqlCode(n); got != want {
			t.Errorf("%d: %s, want %s", n, got, want)
		}
	}
}

func TestConnectionID(t *testing.T) {
	if _, err := connectionID(queryengine.QueryHandle{}); err == nil {
		t.Error("a handle without connection_id did not fail")
	}
	id, err := connectionID(queryengine.QueryHandle{Extra: map[string]string{"connection_id": "42"}})
	if err != nil || id != 42 {
		t.Errorf("id=%d err=%v", id, err)
	}
}
