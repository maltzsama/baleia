package trino

import (
	"errors"
	"regexp"
)

var retentionRe = regexp.MustCompile(`^[1-9][0-9]{0,4}[dhm]$`)

// ValidateRetention accepts the retention_threshold values maintenance sends to
// Trino: 1 to 99999 followed by d, h or m.
func ValidateRetention(s string) error {
	if !retentionRe.MatchString(s) {
		return errors.New("retention must look like 7d, 12h or 30m")
	}
	return nil
}
