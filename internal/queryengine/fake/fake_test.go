package fake_test

import (
	"context"
	"testing"
	"time"

	"github.com/apache/arrow-go/v18/arrow"
	"github.com/galileostudio/baleia/internal/dialect"
	"github.com/galileostudio/baleia/internal/queryengine"
	"github.com/galileostudio/baleia/internal/queryengine/conformance"
	"github.com/galileostudio/baleia/internal/queryengine/fake"
)

func fixtures() conformance.Fixtures {
	return conformance.Fixtures{
		TypesSQL: "SELECT TYPES",
		Types: []conformance.TypeCase{
			{Column: "bool", Type: arrow.FixedWidthTypes.Boolean, Value: fake.TypesValues["bool"]},
			{Column: "int64", Type: arrow.PrimitiveTypes.Int64, Value: fake.TypesValues["int64"]},
			{Column: "decimal", Type: &arrow.Decimal128Type{Precision: 38, Scale: 10}, Value: fake.TypesValues["decimal"]},
			{Column: "date", Type: arrow.FixedWidthTypes.Date32, Value: fake.TypesValues["date"]},
			{Column: "timestamp", Type: &arrow.TimestampType{Unit: arrow.Microsecond}, Value: fake.TypesValues["timestamp"]},
			{Column: "timestamptz", Type: &arrow.TimestampType{Unit: arrow.Microsecond, TimeZone: "UTC"}, Value: fake.TypesValues["timestamptz"]},
			{Column: "varchar", Type: arrow.BinaryTypes.String, Value: fake.TypesValues["varchar"]},
			{Column: "null", Type: arrow.Null, Value: fake.TypesValues["null"]},
			{Column: "binary", Type: arrow.BinaryTypes.Binary, Value: fake.TypesValues["binary"]},
		},
		ZeroRowsSQL:       "SELECT ZERO",
		MultiResultSQL:    "SELECT MULTI",
		SetStateSQL:       "SET search_path = 'sales'",
		ReadStateSQL:      "SHOW search_path",
		StateValue:        "sales",
		BeginSQL:          "BEGIN",
		InsertSQL:         "INSERT",
		CountSQL:          "SELECT COUNT",
		ReadSQL:           "SELECT ZERO",
		WriteSQL:          "WRITE",
		SleepSQL:          "SLEEP",
		SyntaxErrorSQL:    "SELEKT",
		MissingTableSQL:   "SELECT * FROM missing",
		ManyRowsSQL:       "ROWS 1000",
		CollectCap:        250,
		IntrospectCatalog: "fake",
		IntrospectSchema:  []string{"main"},
		IntrospectTable:   "events",
		IntrospectColumns: []string{"id", "payload"},
	}
}

func TestConformance(t *testing.T) {
	conformance.Run(t, func(*testing.T) queryengine.Engine { return fake.New(fake.Options{ID: "e1"}) }, fixtures())
}

// The suite also covers an engine that cannot enforce read-only sessions.
func TestConformance_ReadOnlyNone(t *testing.T) {
	conformance.Run(t, func(*testing.T) queryengine.Engine {
		return fake.New(fake.Options{ID: "e2", ReadOnly: queryengine.ReadOnlyNone})
	}, fixtures())
}

// TestCapabilities_ReportTheInjectedDialect: Capabilities and Dialect agree.
func TestCapabilities_ReportTheInjectedDialect(t *testing.T) {
	pg := dialect.NewRegistry().MustGet(dialect.PostgreSQL)
	e := fake.New(fake.Options{Dialect: pg})
	if got := e.Capabilities().Dialect; got != pg.Name() {
		t.Fatalf("capabilities dialect = %q, want %q", got, pg.Name())
	}
	if got := fake.New(fake.Options{}).Capabilities().Dialect; got != dialect.FlightSQL {
		t.Fatalf("default capabilities dialect = %q, want %q", got, dialect.FlightSQL)
	}
}

// TestCancel_AsSoonAsTheHandleIsReported: a Cancel issued from OnStart, the
// earliest a caller can hold the handle, stops the statement. Before the fix
// the handle was registered only after OnStart, and the statement slept on.
func TestCancel_AsSoonAsTheHandleIsReported(t *testing.T) {
	ctx := context.Background()
	e := fake.New(fake.Options{ID: "a"})
	other := fake.New(fake.Options{ID: "b"})
	s, err := e.OpenSession(ctx, queryengine.SessionSpec{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	done := make(chan error, 1)
	go func() {
		done <- s.Execute(ctx, "SLEEP", queryengine.Callbacks{OnStart: func(h queryengine.QueryHandle) {
			if err := other.Cancel(ctx, h); err != nil {
				t.Errorf("cancel: %v", err)
			}
		}}, nil)
	}()
	select {
	case err := <-done:
		if queryengine.CodeOf(err) != queryengine.CodeCancelled {
			t.Fatalf("err = %v, want cancelled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a cancel at OnStart was lost; the statement is still running")
	}
}
