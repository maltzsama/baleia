package typemap

import "testing"

func TestIcebergTypeMapping(t *testing.T) {
	cases := map[string]string{
		"boolean":                   "boolean",
		"int":                       "integer",
		"long":                      "bigint",
		"float":                     "real",
		"double":                    "double",
		"decimal(10,2)":             "decimal(10, 2)",
		"decimal(38, 0)":            "decimal(38, 0)",
		"date":                      "date",
		"time":                      "time(6)",
		"timestamp":                 "timestamp(6)",
		"timestamptz":               "timestamp(6) with time zone",
		"timestamptz_ns":            "timestamp(9) with time zone",
		"string":                    "varchar",
		"uuid":                      "uuid",
		"fixed[16]":                 "varbinary",
		"binary":                    "varbinary",
		"list<long>":                "array(bigint)",
		"map<string, decimal(9,3)>": "map(varchar, decimal(9, 3))",
		"struct<a: int, b: string>": "row(a integer, b varchar)",
		"map<string, struct<n: long, at: timestamptz>>":      "map(varchar, row(n bigint, at timestamp(6) with time zone))",
		"list<struct<Order Id: long, tags: list<string>>>":   `array(row("Order Id" bigint, tags array(varchar)))`,
		"struct<inner: struct<x: double>, m: map<int, int>>": "row(inner row(x double), m map(integer, integer))",
	}
	for in, want := range cases {
		got, ok := IcebergToTrino(in)
		if !ok || got != want {
			t.Errorf("%q -> %q, %v; want %q", in, got, ok, want)
		}
	}
	for _, bad := range []string{"", "variant", "list<long", "map<string>", "struct<a int>", "list<long>>"} {
		if got, ok := IcebergToTrino(bad); ok {
			t.Errorf("%q must not map, got %q", bad, got)
		}
	}
}

func TestDisplay(t *testing.T) {
	if got, ok := Display("trino", "trino", "varchar(10)"); !ok || got != "varchar(10)" {
		t.Errorf("same type system is the identity: %q %v", got, ok)
	}
	if got, ok := Display("iceberg", "trino", "long"); !ok || got != "bigint" {
		t.Errorf("iceberg on trino: %q %v", got, ok)
	}
	if _, ok := Display("postgresql", "trino", "integer"); ok {
		t.Error("no mapping must not pretend")
	}
}
