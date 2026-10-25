// Package schedule parses retriever cron expressions: five standard fields,
// an optional CRON_TZ=<IANA> prefix, and a minimum interval between runs
// (#118, #139). It has no dependencies, so the API and the worker share it.
package schedule

import (
	"errors"
	"strings"
	"time"

	"github.com/robfig/cron"
)

// MinInterval is the shortest time allowed between two refreshes.
const MinInterval = 5 * time.Minute

// Parse parses a standard five-field cron expression (minute hour
// day-of-month month day-of-week), optionally prefixed with CRON_TZ=<IANA>,
// and enforces MinInterval.
func Parse(expr string) (cron.Schedule, *time.Location, error) {
	loc, clean := Location(expr)
	schedule, err := cron.ParseStandard(clean)
	if err != nil {
		return nil, loc, errors.New("invalid cron expression: " + err.Error())
	}
	// Sampled across three consecutive runs.
	ref := time.Now().In(loc)
	t1 := schedule.Next(ref)
	t2 := schedule.Next(t1)
	t3 := schedule.Next(t2)
	if t2.Sub(t1) < MinInterval || t3.Sub(t2) < MinInterval {
		return nil, loc, errors.New("cron minimum interval is 5 minutes")
	}
	return schedule, loc, nil
}

// Location reads the optional CRON_TZ=<IANA> prefix and returns the
// expression the parser understands; UTC without a valid prefix.
func Location(expr string) (*time.Location, string) {
	expr = strings.TrimSpace(expr)
	const prefix = "CRON_TZ="
	if strings.HasPrefix(strings.ToUpper(expr), prefix) {
		rest := strings.TrimSpace(expr[len(prefix):])
		parts := strings.SplitN(rest, " ", 2)
		if len(parts) == 2 {
			if loc, err := time.LoadLocation(strings.TrimSpace(parts[0])); err == nil {
				return loc, strings.TrimSpace(parts[1])
			}
		}
	}
	return time.UTC, expr
}

// Preview is the next runs of an expression, for the editor (#118); the
// browser never evaluates cron itself.
type Preview struct {
	Valid bool
	// Error is why an invalid expression was rejected.
	Error string
	Next  []time.Time
	// MinIntervalOK is false when two runs are closer than MinInterval.
	MinIntervalOK bool
}

// Next5 returns the next five runs of expr, in UTC.
func Next5(expr string) Preview {
	loc, clean := Location(expr)
	schedule, err := cron.ParseStandard(clean)
	if err != nil {
		return Preview{Error: "invalid cron expression: " + err.Error(), Next: []time.Time{}}
	}
	next := make([]time.Time, 0, 5)
	ref := time.Now().In(loc)
	for range 5 {
		ref = schedule.Next(ref)
		next = append(next, ref.UTC())
	}
	ok := true
	for i := 1; i < len(next); i++ {
		if next[i].Sub(next[i-1]) < MinInterval {
			ok = false
			break
		}
	}
	return Preview{Valid: true, Next: next, MinIntervalOK: ok}
}
