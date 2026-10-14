package fake_test

import (
	"testing"

	"github.com/apache/arrow-go/v18/arrow"
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
