package clickhouse

import (
	"testing"

	"github.com/galileostudio/baleia/internal/queryengine"
)

func TestClickHousePriority(t *testing.T) {
	t.Parallel()
	cases := map[queryengine.WorkloadClass]string{
		queryengine.ClassInteractive: "1",
		queryengine.ClassHeavy:       "5",
		queryengine.ClassBackground:  "10",
		"":                           "",
	}
	for class, want := range cases {
		if got := clickHousePriority(class); got != want {
			t.Errorf("clickHousePriority(%q) = %q, want %q", class, got, want)
		}
	}
}
