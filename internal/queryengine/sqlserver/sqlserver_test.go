package sqlserver

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/galileostudio/baleia/internal/dialect"
	"github.com/galileostudio/baleia/internal/queryengine"
)

// TestGOBatches: the dialect splits a script on GO lines, and GO never reaches
// the server (each batch is one Execute).
func TestGOBatches(t *testing.T) {
	spans, err := dialect.MustGet(dialect.TSQL).Statements("SELECT 1\nGO\nSELECT 2\nGO\n")
	if err != nil {
		t.Fatal(err)
	}
	if len(spans) != 2 {
		t.Fatalf("statements = %d, want 2", len(spans))
	}
	for _, s := range spans {
		if s.Text == "GO" {
			t.Errorf("GO was kept as a statement: %q", s.Text)
		}
	}
}

// TestCancelScopeInProcess: without the cancel_with_kill assertion, Cancel
// cannot reach another process and returns ErrCancelUnsupported.
func TestCancelScopeInProcess(t *testing.T) {
	e := &Engine{cancelWithKill: false}
	if got := e.Capabilities().CancelScope; got != queryengine.CancelInProcess {
		t.Errorf("CancelScope = %q, want in_process", got)
	}
	err := e.Cancel(context.Background(), queryengine.QueryHandle{Extra: map[string]string{"spid": "1"}})
	if !errors.Is(err, queryengine.ErrCancelUnsupported) {
		t.Fatalf("err = %v, want ErrCancelUnsupported", err)
	}
}

// TestReadOnlyRequiresAssertion: SQL Server cannot enforce a read-only session
// (ReadOnly is none), so without the credentials assertion OpenSession fails.
func TestReadOnlyRequiresAssertion(t *testing.T) {
	e := &Engine{}
	if got := e.Capabilities().ReadOnly; got != queryengine.ReadOnlyNone {
		t.Fatalf("ReadOnly = %q, want none", got)
	}
	_, err := queryengine.Guard(e, false).OpenSession(context.Background(), queryengine.SessionSpec{ReadOnly: true})
	if queryengine.CodeOf(err) != queryengine.CodeReadOnly {
		t.Fatalf("OpenSession(read-only) = %v, want read_only_unsupported", err)
	}
}

func TestTSQLCode(t *testing.T) {
	cases := map[int32]queryengine.ErrorCode{
		102:  queryengine.CodeSyntax,
		156:  queryengine.CodeSyntax,
		208:  queryengine.CodeNotFound,
		2812: queryengine.CodeNotFound,
		229:  queryengine.CodePermission,
		1205: queryengine.CodeConflict,
		1222: queryengine.CodeTimeout,
		9999: queryengine.CodeInternal,
	}
	for n, want := range cases {
		if got := tsqlCode(n); got != want {
			t.Errorf("%d: %s, want %s", n, got, want)
		}
	}
}

func TestFormatUUID(t *testing.T) {
	b := []byte{0x11, 0x11, 0x11, 0x11, 0x22, 0x22, 0x33, 0x33, 0x44, 0x44, 0x55, 0x55, 0x55, 0x55, 0x55, 0x55}
	if got := formatUUID(b); got != "11111111-2222-3333-4444-555555555555" {
		t.Errorf("formatUUID = %q", got)
	}
}

func TestSessionID(t *testing.T) {
	if _, err := sessionID(queryengine.QueryHandle{}); err == nil {
		t.Error("a handle without spid did not fail")
	}
	id, err := sessionID(queryengine.QueryHandle{Extra: map[string]string{"spid": "55"}})
	if err != nil || id != 55 {
		t.Errorf("id=%d err=%v", id, err)
	}
}

func TestNanosSinceMidnight(t *testing.T) {
	v := time.Date(1, 1, 1, 13, 45, 30, 123456700, time.UTC)
	want := int64(13)*3_600_000_000_000 + 45*60_000_000_000 + 30*1_000_000_000 + 123456700
	if got := nanosSinceMidnight(v); got != want {
		t.Errorf("nanosSinceMidnight = %d, want %d", got, want)
	}
}
