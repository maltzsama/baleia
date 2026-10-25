package schedule

import (
	"testing"
	"time"
)

// TestParseCron_IsStandardFiveField pins #139: the expression is
// "minute hour day-of-month month day-of-week", not "second minute hour ...".
func TestParse_IsStandardFiveField(t *testing.T) {
	s, _, err := Parse("0 3 * * *")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	from := time.Date(2026, 10, 13, 19, 10, 0, 0, time.UTC)
	if got, want := s.Next(from), time.Date(2026, 10, 14, 3, 0, 0, 0, time.UTC); !got.Equal(want) {
		t.Fatalf("next run of '0 3 * * *' after %s = %s, want %s (daily at 03:00, not hourly at :03)", from, got, want)
	}
	s, _, err = Parse("*/15 9-17 * * 1-5")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	// Friday 17:50 -> next is Monday 09:00.
	fri := time.Date(2026, 10, 16, 17, 50, 0, 0, time.UTC)
	if got, want := s.Next(fri), time.Date(2026, 10, 19, 9, 0, 0, 0, time.UTC); !got.Equal(want) {
		t.Fatalf("weekday window next = %s, want %s", got, want)
	}
}

func TestParse_Rejects(t *testing.T) {
	for name, expr := range map[string]string{
		"every minute":      "* * * * *",
		"every 4 minutes":   "*/4 * * * *",
		"six fields":        "0 0 3 * * *",
		"garbage":           "nope",
		"empty":             "",
		"out of range hour": "0 25 * * *",
	} {
		if _, _, err := Parse(expr); err == nil {
			t.Errorf("%s: %q was accepted", name, expr)
		}
	}
	for _, ok := range []string{"*/5 * * * *", "0 */6 * * *", "30 2 * * 0"} {
		if _, _, err := Parse(ok); err != nil {
			t.Errorf("%q rejected: %v", ok, err)
		}
	}
}

func TestLocation(t *testing.T) {
	loc, expr := Location("CRON_TZ=America/Sao_Paulo 0 3 * * *")
	if loc.String() != "America/Sao_Paulo" || expr != "0 3 * * *" {
		t.Errorf("CronLocation = %s, %q", loc, expr)
	}
	if loc, expr := Location("0 3 * * *"); loc != time.UTC || expr != "0 3 * * *" {
		t.Errorf("no prefix = %s, %q; want UTC", loc, expr)
	}
}

func TestNext5(t *testing.T) {
	if p := Next5("*/5 * * * *"); !p.Valid || len(p.Next) != 5 || !p.MinIntervalOK {
		t.Errorf("every 5 minutes = %+v", p)
	}
	if p := Next5("* * * * *"); !p.Valid || p.MinIntervalOK {
		t.Errorf("every minute = %+v; valid but below the minimum interval", p)
	}
	if p := Next5("nope"); p.Valid || p.Error == "" || p.Next == nil {
		t.Errorf("garbage = %+v", p)
	}
}
