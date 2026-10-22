package clickhouse

import (
	"testing"

	"github.com/galileostudio/baleia/internal/dialect"
	"github.com/galileostudio/baleia/internal/queryengine"
)

func TestSpec(t *testing.T) {
	if s := Spec(dialect.NewRegistry().MustGet(dialect.ClickHouse)); s.Name != Kind || s.Factory == nil {
		t.Fatalf("spec = %+v, want kind %q with a factory", s, Kind)
	}
}

func TestExceptionCode(t *testing.T) {
	cases := map[int]queryengine.ErrorCode{
		62:  queryengine.CodeSyntax,
		60:  queryengine.CodeNotFound,
		81:  queryengine.CodeNotFound,
		47:  queryengine.CodeNotFound,
		497: queryengine.CodePermission,
		164: queryengine.CodePermission,
		516: queryengine.CodePermission,
		159: queryengine.CodeTimeout,
		209: queryengine.CodeTimeout,
		394: queryengine.CodeCancelled,
		241: queryengine.CodeResource,
		160: queryengine.CodeResource,
		210: queryengine.CodeUnavailable,
		-1:  queryengine.CodeInternal,
	}
	for code, want := range cases {
		if got := exceptionCode(code); got != want {
			t.Errorf("exceptionCode(%d) = %s, want %s", code, got, want)
		}
	}
}

func TestLogComment(t *testing.T) {
	if got := logComment(queryengine.Tags{}); got != "" {
		t.Fatalf("empty tags = %q, want empty", got)
	}
	got := logComment(queryengine.Tags{Source: "editor", ExecutionID: "e1"})
	if got != `{"source":"editor","execution_id":"e1"}` {
		t.Fatalf("logComment = %q", got)
	}
}
