package oracle

import (
	"testing"

	"github.com/galileostudio/baleia/internal/queryengine"
)

func TestOracleModule(t *testing.T) {
	t.Parallel()
	if got := oracleModule(queryengine.Tags{}); got != "baleia" {
		t.Errorf("no class: %q", got)
	}
	if got := oracleModule(queryengine.Tags{Class: queryengine.ClassBackground}); got != "baleia-background" {
		t.Errorf("background: %q", got)
	}
}
