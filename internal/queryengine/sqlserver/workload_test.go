package sqlserver

import (
	"testing"

	"github.com/galileostudio/baleia/internal/queryengine"
)

func TestSQLServerAppName(t *testing.T) {
	t.Parallel()
	if got := sqlServerAppName(""); got != "baleia" {
		t.Errorf("no class: %q", got)
	}
	if got := sqlServerAppName(queryengine.ClassHeavy); got != "baleia-heavy" {
		t.Errorf("heavy: %q", got)
	}
}
