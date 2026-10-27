package dbpool

import (
	"context"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/testdb"
)

// TestOpen_SessionGuards: the timeouts reach every session as server
// settings, in milliseconds (#377).
func TestOpen_SessionGuards(t *testing.T) {
	t.Parallel()
	url := testdb.New(t).Config().ConnString()
	ctx := context.Background()
	pool, err := Open(ctx, url, Options{
		StatementTimeout: time.Minute, IdleInTxTimeout: 2 * time.Minute, LockTimeout: 30 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	for name, want := range map[string]string{
		"statement_timeout":                   "60000",
		"idle_in_transaction_session_timeout": "120000",
		"lock_timeout":                        "30000",
	} {
		var got string
		if err := pool.QueryRow(ctx, "SELECT setting FROM pg_settings WHERE name = $1", name).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Errorf("%s = %s, want %s", name, got, want)
		}
	}
}
