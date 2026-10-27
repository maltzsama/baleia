package oracle

import (
	"testing"

	"github.com/galileostudio/baleia/internal/dialect"
)

func TestOptions_LOBMaxBytes(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		input    int
		expected int
	}{
		{name: "unset keeps the default", input: 0, expected: defaultLobMaxBytes},
		{name: "negative keeps the default", input: -1, expected: defaultLobMaxBytes},
		{name: "configured", input: 4096, expected: 4096},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := (Options{LOBMaxBytes: tt.input}).lobMaxBytes(); got != tt.expected {
				t.Fatalf("lobMaxBytes = %d, want %d", got, tt.expected)
			}
		})
	}
}

func TestSpec(t *testing.T) {
	t.Parallel()
	if s := Spec(Options{}, dialect.NewRegistry().MustGet(dialect.Oracle)); s.Name != Kind || s.Factory == nil {
		t.Fatalf("spec = %+v, want kind %q with a factory", s, Kind)
	}
}
