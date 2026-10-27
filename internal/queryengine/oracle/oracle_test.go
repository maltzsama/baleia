package oracle

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/galileostudio/baleia/internal/dialect"
	"github.com/galileostudio/baleia/internal/queryengine"
)

// TestPLSQLBlockKeptWhole: a PL/SQL block is one statement, kept whole with its
// END; and terminated by a line with / (SQL*Plus convention).
func TestPLSQLBlockKeptWhole(t *testing.T) {
	t.Parallel()
	spans, err := dialect.NewRegistry().MustGet(dialect.Oracle).Statements("BEGIN\n  NULL;\nEND;\n/\n")
	if err != nil {
		t.Fatal(err)
	}
	if len(spans) != 1 {
		t.Fatalf("statements = %d, want 1: %+v", len(spans), spans)
	}
	if !strings.Contains(strings.ToUpper(spans[0].Text), "BEGIN") || !strings.Contains(strings.ToUpper(spans[0].Text), "END") {
		t.Errorf("block = %q", spans[0].Text)
	}
}

// TestCancelScopeInProcess: without the cancel_with_alter_system assertion,
// Cancel cannot reach another process and returns ErrCancelUnsupported.
func TestCancelScopeInProcess(t *testing.T) {
	t.Parallel()
	e := &Engine{cancelWithAlterSystem: false}
	if got := e.Capabilities().CancelScope; got != queryengine.CancelInProcess {
		t.Errorf("CancelScope = %q, want in_process", got)
	}
	err := e.Cancel(context.Background(), queryengine.QueryHandle{Extra: map[string]string{"sid": "1", "serial": "1"}})
	if !errors.Is(err, queryengine.ErrCancelUnsupported) {
		t.Fatalf("err = %v, want ErrCancelUnsupported", err)
	}
}

// TestReadOnlyRequiresAssertion: Oracle cannot enforce a read-only session
// (ReadOnly is none), so without the credentials assertion OpenSession fails.
func TestReadOnlyRequiresAssertion(t *testing.T) {
	t.Parallel()
	e := &Engine{}
	if got := e.Capabilities().ReadOnly; got != queryengine.ReadOnlyNone {
		t.Fatalf("ReadOnly = %q, want none", got)
	}
	_, err := queryengine.Guard(e, false).OpenSession(context.Background(), queryengine.SessionSpec{ReadOnly: true})
	if queryengine.CodeOf(err) != queryengine.CodeReadOnly {
		t.Fatalf("OpenSession(read-only) = %v, want read_only_unsupported", err)
	}
}

func TestOraCode(t *testing.T) {
	t.Parallel()
	cases := map[int]queryengine.ErrorCode{
		900:   queryengine.CodeSyntax,
		933:   queryengine.CodeSyntax,
		936:   queryengine.CodeSyntax,
		942:   queryengine.CodeNotFound,
		904:   queryengine.CodeNotFound,
		4043:  queryengine.CodeNotFound,
		1017:  queryengine.CodePermission,
		1031:  queryengine.CodePermission,
		1013:  queryengine.CodeCancelled,
		60:    queryengine.CodeConflict,
		1555:  queryengine.CodeResource,
		12541: queryengine.CodeUnavailable,
		9999:  queryengine.CodeInternal,
	}
	for n, want := range cases {
		if got := oraCode(n); got != want {
			t.Errorf("ORA-%05d: %s, want %s", n, got, want)
		}
	}
}

func TestSessionIDs(t *testing.T) {
	t.Parallel()
	if _, _, err := sessionIDs(queryengine.QueryHandle{}); err == nil {
		t.Error("a handle without sid did not fail")
	}
	sid, serial, err := sessionIDs(queryengine.QueryHandle{Extra: map[string]string{"sid": "7", "serial": "9"}})
	if err != nil || sid != 7 || serial != 9 {
		t.Errorf("sid=%d serial=%d err=%v", sid, serial, err)
	}
}

func TestTruncateUTF8(t *testing.T) {
	t.Parallel()
	if got := truncateUTF8("ação", 100); got != "ação" {
		t.Errorf("no-op truncation = %q", got)
	}
	// "ação" is a(1) ç(2) ã(2) o(1) = 6 bytes; cutting at 2 must not split ç.
	if got := truncateUTF8("ação", 2); got != "a" {
		t.Errorf("truncateUTF8(ação, 2) = %q, want %q", got, "a")
	}
}
