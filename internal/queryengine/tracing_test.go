package queryengine_test

import (
	"context"
	"strings"
	"testing"

	"github.com/galileostudio/baleia/internal/queryengine"
	"github.com/galileostudio/baleia/internal/queryengine/fake"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// TestEngineSpans: a session opens and runs statements inside spans named
// after the operation, with the engine kind, the rows and result sets, and
// on failure the error code but never the statement (#380).
//
//nolint:paralleltest // installs the global tracer provider.
func TestEngineSpans(t *testing.T) {
	exp := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exp))
	prev := otel.GetTracerProvider()
	otel.SetTracerProvider(tp)
	t.Cleanup(func() { otel.SetTracerProvider(prev) })

	e := queryengine.Guard(fake.New(fake.Options{ReadOnly: queryengine.ReadOnlyEnforced}), false)
	if _, err := queryengine.CollectStrings(context.Background(), e, queryengine.SessionSpec{}, "SELECT MULTI", 100); err != nil {
		t.Fatal(err)
	}
	const secret = "SET name 'p4ssw0rd'"
	if _, err := queryengine.CollectStrings(context.Background(), e, queryengine.SessionSpec{}, secret, 100); err == nil {
		t.Fatal("the malformed SET ran")
	}

	byName := map[string][]sdktrace.ReadOnlySpan{}
	for _, s := range exp.GetSpans().Snapshots() {
		byName[s.Name()] = append(byName[s.Name()], s)
	}
	if len(byName["engine.open_session"]) != 2 || len(byName["engine.execute"]) != 2 {
		t.Fatalf("spans = %v, want two sessions and two statements", byName)
	}
	ok, failed := byName["engine.execute"][0], byName["engine.execute"][1]
	if attrs := attrMap(ok.Attributes()); attrs["baleia.engine.kind"] != "fake" || attrs["baleia.result_sets"] != "2" || attrs["baleia.rows"] != "2" {
		t.Errorf("statement attributes = %v, want kind, 2 result sets, 2 rows", attrs)
	}
	if failed.Status().Code != codes.Error || attrMap(failed.Attributes())["baleia.error.code"] != string(queryengine.CodeSyntax) {
		t.Errorf("failed statement status = %v %v, want the error code", failed.Status(), failed.Attributes())
	}
	for _, s := range exp.GetSpans().Snapshots() {
		for _, kv := range s.Attributes() {
			if strings.Contains(kv.Value.String(), "p4ssw0rd") {
				t.Errorf("span %s carries the statement: %v", s.Name(), kv)
			}
		}
		if strings.Contains(s.Status().Description, "p4ssw0rd") {
			t.Errorf("span %s status carries the statement: %v", s.Name(), s.Status())
		}
	}
}

func attrMap(kvs []attribute.KeyValue) map[string]string {
	out := map[string]string{}
	for _, kv := range kvs {
		out[string(kv.Key)] = kv.Value.String()
	}
	return out
}
