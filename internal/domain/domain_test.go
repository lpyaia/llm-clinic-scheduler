package domain

import (
	"testing"
	"time"
)

func TestWeekBounds(t *testing.T) {
	loc, _ := time.LoadLocation("America/Sao_Paulo")

	for _, test := range []struct{ at, start string }{
		{"2026-10-19T00:00:00-03:00", "2026-10-19T00:00:00-03:00"},
		{"2026-10-25T23:59:59-03:00", "2026-10-19T00:00:00-03:00"},
		{"2026-10-26T02:59:59Z", "2026-10-19T00:00:00-03:00"},
		{"2026-10-26T03:00:00Z", "2026-10-26T00:00:00-03:00"},
	} {
		at, _ := time.Parse(time.RFC3339, test.at)
		start, end := WeekBounds(at, loc)
		if start.Format(time.RFC3339) != test.start || !end.Equal(start.AddDate(0, 0, 7)) {
			t.Fatalf("%s: %s - %s", test.at, start, end)
		}
	}
}
func TestValidateChange(t *testing.T) {
	now := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		status string
		offset time.Duration
		valid  bool
	}{
		{Scheduled, time.Second, true}, {Scheduled, 0, false}, {Scheduled, -time.Second, false}, {Cancelled, time.Hour, false}, {Completed, time.Hour, false},
	} {
		err := ValidateChange(Appointment{Status: test.status, StartsAt: now.Add(test.offset)}, now)
		if (err == nil) != test.valid {
			t.Fatalf("status=%s offset=%s: %v", test.status, test.offset, err)
		}
	}
}
func TestPeriods(t *testing.T) {
	for _, test := range []struct {
		period     string
		start, end int
	}{{"", 0, 24}, {"morning", 6, 12}, {"afternoon", 12, 18}, {"evening", 18, 24}} {
		start, end, err := PeriodBounds(test.period)
		if err != nil || start != test.start || end != test.end {
			t.Fatalf("%s: %d %d %v", test.period, start, end, err)
		}
	}

	if _, _, err := PeriodBounds("invalid"); err == nil {
		t.Fatal("expected invalid period")
	}
}
