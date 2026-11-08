package model

import (
	"reflect"
	"testing"
)

func TestStringArrayRoundTrip(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   StringArray
		lit  string
	}{
		{name: "one", in: StringArray{"*"}, lit: `{"*"}`},
		{name: "many", in: StringArray{"a", "b"}, lit: `{"a","b"}`},
		{name: "escaping", in: StringArray{`a"b`, `c\d`}, lit: `{"a\"b","c\\d"}`},
		{name: "empty", in: StringArray{}, lit: `{}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := tt.in.Value()
			if err != nil {
				t.Fatalf("Value: %v", err)
			}
			if got != tt.lit {
				t.Fatalf("Value = %q, want %q", got, tt.lit)
			}
			var back StringArray
			if err := back.Scan(got); err != nil {
				t.Fatalf("Scan: %v", err)
			}
			if !reflect.DeepEqual(back, tt.in) {
				t.Fatalf("round trip = %v, want %v", back, tt.in)
			}
		})
	}
}

func TestStringArrayScanJSON(t *testing.T) {
	t.Parallel()
	var got StringArray
	if err := got.Scan(`["a","b"]`); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, StringArray{"a", "b"}) {
		t.Fatalf("Scan = %v, want [a b]", got)
	}
}

func TestScalarTypesScanAndValue(t *testing.T) {
	t.Parallel()
	var uuid UUID
	if err := uuid.Scan("7b0e6d4a-0000-0000-0000-000000000000"); err != nil {
		t.Fatal(err)
	}
	if uuid != "7b0e6d4a-0000-0000-0000-000000000000" {
		t.Fatalf("UUID = %q", uuid)
	}
	if v, _ := uuid.Value(); v != string(uuid) {
		t.Fatalf("UUID.Value = %v", v)
	}

	var text Text
	if err := text.Scan([]byte("hello")); err != nil {
		t.Fatal(err)
	}
	if text != "hello" {
		t.Fatalf("Text = %q", text)
	}

	var inet Inet
	if err := inet.Scan("127.0.0.1"); err != nil {
		t.Fatal(err)
	}
	if inet != "127.0.0.1" {
		t.Fatalf("Inet = %q", inet)
	}

	var js JSON
	if err := js.Scan([]byte(`{"a":1}`)); err != nil {
		t.Fatal(err)
	}
	if string(js) != `{"a":1}` {
		t.Fatalf("JSON = %s", js)
	}
}
