package metadata

import (
	"context"
	"testing"

	"github.com/galileostudio/baleia/internal/dialect"
	"github.com/galileostudio/baleia/internal/queryengine"
)

type fakeFunctionEngine struct {
	queryengine.Engine
	name string
	rows [][]string
}

func (e *fakeFunctionEngine) Dialect() queryengine.Dialect {
	return dialect.NewRegistry().MustGet(e.name)
}

func (e *fakeFunctionEngine) OpenSession(context.Context, queryengine.SessionSpec) (queryengine.Session, error) {
	return &fakeFunctionSession{rows: e.rows}, nil
}

type fakeFunctionSession struct{ rows [][]string }

func (s *fakeFunctionSession) Close() error { return nil }

func (s *fakeFunctionSession) Execute(_ context.Context, _ string, _ queryengine.Callbacks, sink queryengine.RecordSink) error {
	return emitStrings(sink, s.rows)
}

func TestFunctions(t *testing.T) {
	t.Parallel()
	eng := &fakeFunctionEngine{name: dialect.PostgreSQL, rows: [][]string{
		{"abs", "abs(int) → int", "absolute value", "function"},
		{"sum", "sum(x) → x", "sum", "aggregate"},
		{"", "", "", ""}, // an empty name is dropped
	}}
	refs, err := Functions(context.Background(), eng, "catalog")
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 2 {
		t.Fatalf("got %d functions, want 2", len(refs))
	}
	if refs[0].Name != "abs" || refs[0].Signature != "abs(int) → int" || refs[0].Description != "absolute value" || refs[0].Kind != "function" {
		t.Errorf("abs = %+v", refs[0])
	}
	if refs[1].Kind != "aggregate" {
		t.Errorf("sum = %+v", refs[1])
	}
}

func TestFunctions_NoQuery(t *testing.T) {
	t.Parallel()
	// T-SQL has no function catalog (FunctionQuery is empty).
	refs, err := Functions(context.Background(), &fakeFunctionEngine{name: dialect.TSQL}, "catalog")
	if err != nil {
		t.Fatal(err)
	}
	if refs != nil {
		t.Fatalf("got %v, want nil", refs)
	}
}
