package clickhouse

import (
	"errors"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/dialect"
	"github.com/galileostudio/baleia/internal/queryengine"
)

func TestSpec(t *testing.T) {
	t.Parallel()
	if s := Spec(dialect.NewRegistry().MustGet(dialect.ClickHouse)); s.Name != Kind || s.Factory == nil {
		t.Fatalf("spec = %+v, want kind %q with a factory", s, Kind)
	}
}

func TestExceptionCode(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
	if got := logComment(queryengine.Tags{}); got != "" {
		t.Fatalf("empty tags = %q, want empty", got)
	}
	got := logComment(queryengine.Tags{Source: "editor", ExecutionID: "e1"})
	if got != `{"source":"editor","execution_id":"e1"}` {
		t.Fatalf("logComment = %q", got)
	}
}

// TestKillUntilFound: a KILL that finds nothing is retried, since the query
// may not be registered yet; one that keeps finding nothing gives up without
// an error after the window.
func TestKillUntilFound(t *testing.T) {
	t.Parallel()
	t.Run("found on a later try", func(t *testing.T) {
		t.Parallel()
		calls := 0
		err := killUntilFound(time.Second, func(time.Duration) (bool, error) {
			calls++
			return calls == 3, nil
		})
		if err != nil || calls != 3 {
			t.Fatalf("calls = %d, err = %v; want 3, nil", calls, err)
		}
	})
	t.Run("gone", func(t *testing.T) {
		t.Parallel()
		start := time.Now()
		calls := 0
		err := killUntilFound(200*time.Millisecond, func(time.Duration) (bool, error) {
			calls++
			return false, nil
		})
		if err != nil || calls < 2 || time.Since(start) > time.Second {
			t.Fatalf("calls = %d, err = %v, took %s", calls, err, time.Since(start))
		}
	})
	t.Run("each attempt gets the time left", func(t *testing.T) {
		t.Parallel()
		var lefts []time.Duration
		_ = killUntilFound(300*time.Millisecond, func(left time.Duration) (bool, error) {
			lefts = append(lefts, left)
			return false, nil
		})
		if len(lefts) < 2 || lefts[0] > 300*time.Millisecond || lefts[len(lefts)-1] >= lefts[0] {
			t.Fatalf("time left per attempt = %v", lefts)
		}
	})
	t.Run("error stops at once", func(t *testing.T) {
		t.Parallel()
		boom := errors.New("boom")
		calls := 0
		err := killUntilFound(time.Second, func(time.Duration) (bool, error) {
			calls++
			return false, boom
		})
		if !errors.Is(err, boom) || calls != 1 {
			t.Fatalf("calls = %d, err = %v", calls, err)
		}
	})
}
