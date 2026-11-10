package partitioning

import "testing"

func TestCapabilitiesFor(t *testing.T) {
	cases := map[string]Capabilities{
		"postgres":  {NativeRange: true, ForeignKeysOnPartitioned: true, Implemented: true},
		"gaussdb":   {NativeRange: true, ForeignKeysOnPartitioned: true, Implemented: true},
		"mysql":     {NativeRange: true, ForeignKeysOnPartitioned: false, Implemented: true},
		"sqlserver": {NativeRange: true, ForeignKeysOnPartitioned: true, Implemented: true},
		"oracle":    {NativeRange: true, ForeignKeysOnPartitioned: true, Implemented: true},
		"sqlite":    {NativeRange: false, ForeignKeysOnPartitioned: true, Emulated: true, Implemented: true},
		"tidb":      {NativeRange: true, ForeignKeysOnPartitioned: false},
	}
	for name, want := range cases {
		if got := capabilitiesFor(name); got != want {
			t.Errorf("capabilitiesFor(%q) = %+v, want %+v", name, got, want)
		}
	}
	if got := capabilitiesFor("unknown"); got != (Capabilities{}) {
		t.Errorf("capabilitiesFor(unknown) = %+v, want the zero value", got)
	}
}
