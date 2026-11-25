package config

import (
	"os"
	"regexp"
	"testing"

	"github.com/galileostudio/baleia/internal/connections"
)

// TestOwnEnvCoversConfig: every variable Load reads is Baleia's own
// configuration, which a connection's env: secret reference must not read
// (ownEnv in internal/connections). A new setting outside BALEIA_* fails here until it is
// listed there.
func TestOwnEnvCoversConfig(t *testing.T) {
	t.Parallel()
	src, err := os.ReadFile("config.go")
	if err != nil {
		t.Fatal(err)
	}
	var r connections.SecretResolver
	names := regexp.MustCompile(`(?:getEnv[A-Za-z0-9]*|os\.(?:Getenv|LookupEnv))\("([A-Z0-9_]+)"`).FindAllSubmatch(src, -1)
	if len(names) == 0 {
		t.Fatal("no variables found in config.go")
	}
	for _, m := range names {
		if ref := "@baleia-secret[env:" + string(m[1]) + "]"; r.Writable(ref) {
			t.Errorf("%s is read by config.Load but a connection may name it; add it to ownEnv in internal/connections/secretref.go", m[1])
		}
	}
}
