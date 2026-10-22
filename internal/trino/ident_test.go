package trino

import "testing"

func TestValidateRetention(t *testing.T) {
	for _, ok := range []string{"7d", "12h", "30m", "99999d", "1d"} {
		if err := ValidateRetention(ok); err != nil {
			t.Errorf("ValidateRetention(%q): %v", ok, err)
		}
	}
	for _, bad := range []string{"", "0d", "07d", "7", "d", "7w", "100000d", "7d') --", "7d ", "-7d", "7D"} {
		if err := ValidateRetention(bad); err == nil {
			t.Errorf("ValidateRetention(%q) accepted", bad)
		}
	}
}
