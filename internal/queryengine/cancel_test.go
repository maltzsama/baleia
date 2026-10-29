package queryengine

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestTagStatement(t *testing.T) {
	t.Parallel()
	tag := NewStatementTag()
	sql := TagStatement("SELECT 1", tag)
	if !strings.HasPrefix(sql, "/* baleia:"+tag+" */ ") || !strings.HasSuffix(sql, "SELECT 1") {
		t.Fatalf("tagged = %q", sql)
	}
	pattern := TagPattern(tag)
	if !strings.HasSuffix(pattern, "%") || !strings.HasPrefix(sql, strings.TrimSuffix(pattern, "%")) {
		t.Fatalf("pattern %q does not match %q", pattern, sql)
	}
	if NewStatementTag() == tag {
		t.Fatal("two statements got the same tag")
	}
	if got := TagPattern("a%b_c"); strings.ContainsAny(strings.TrimSuffix(got, "%"), "%_") {
		t.Fatalf("wildcards kept in %q", got)
	}
}

// TestCancelUntilFound: a cancel that finds nothing is retried, since the
// statement may not have reached the server yet; one that keeps finding
// nothing gives up without an error after the window.
func TestCancelUntilFound(t *testing.T) {
	t.Parallel()
	t.Run("found on a later try", func(t *testing.T) {
		t.Parallel()
		calls := 0
		err := CancelUntilFound(time.Second, func(time.Duration) (bool, error) {
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
		err := CancelUntilFound(200*time.Millisecond, func(time.Duration) (bool, error) {
			calls++
			return false, nil
		})
		if err != nil || calls < 2 || time.Since(start) > time.Second {
			t.Fatalf("calls = %d, err = %v, took %s", calls, err, time.Since(start))
		}
	})
	t.Run("each attempt gets at least the floor", func(t *testing.T) {
		t.Parallel()
		var lefts []time.Duration
		_ = CancelUntilFound(300*time.Millisecond, func(left time.Duration) (bool, error) {
			lefts = append(lefts, left)
			return false, nil
		})
		if len(lefts) < 2 {
			t.Fatalf("attempts = %d, want retries", len(lefts))
		}
		for _, left := range lefts {
			if left < CancelMinAttempt {
				t.Fatalf("time per attempt = %v, want at least %s each", lefts, CancelMinAttempt)
			}
		}
	})
	t.Run("error stops at once", func(t *testing.T) {
		t.Parallel()
		boom := errors.New("boom")
		calls := 0
		err := CancelUntilFound(time.Second, func(time.Duration) (bool, error) {
			calls++
			return false, boom
		})
		if !errors.Is(err, boom) || calls != 1 {
			t.Fatalf("calls = %d, err = %v", calls, err)
		}
	})
}
