package restock

import (
	"strings"
	"testing"
	"time"
)

func TestNoRestockDailyWindow(t *testing.T) {
	for _, tc := range []struct {
		name, start, end, at string
		want                 bool
	}{
		{"before start", "09:00", "18:00", "2026-10-16T00:59:59Z", false},
		{"start inclusive", "09:00", "18:00", "2026-10-16T01:00:00Z", true},
		{"last second", "09:00", "18:00", "2026-10-16T09:59:59Z", true},
		{"end exclusive", "09:00", "18:00", "2026-10-16T10:00:00Z", false},
		{"cross before", "23:00", "07:00", "2026-10-16T22:59:59+08:00", false},
		{"cross start", "23:00", "07:00", "2026-10-16T23:00:00+08:00", true},
		{"midnight", "23:00", "07:00", "2026-10-17T00:00:00+08:00", true},
		{"cross last", "23:00", "07:00", "2026-10-17T06:59:59+08:00", true},
		{"cross end", "23:00", "07:00", "2026-10-17T07:00:00+08:00", false},
		{"next day repeats", "23:00", "07:00", "2026-10-17T23:00:00+08:00", true},
		{"foreign timezone", "23:00", "07:00", "2026-10-16T09:00:00-07:00", true},
		{"ends midnight", "22:00", "00:00", "2026-10-17T00:00:00+08:00", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := DefaultConfig()
			c.NoRestockEnabled, c.NoRestockStart, c.NoRestockEnd = true, tc.start, tc.end
			at, err := time.Parse(time.RFC3339, tc.at)
			if err != nil {
				t.Fatal(err)
			}
			if got := c.NoRestockReason(at); (got != "") != tc.want {
				t.Fatalf("reason=%q want blocked=%v", got, tc.want)
			}
			c.NoRestockEnabled = false
			if c.NoRestockReason(at) != "" {
				t.Fatal("disabled schedule blocked restock")
			}
		})
	}
}

func TestNoRestockConfigValidation(t *testing.T) {
	for _, pair := range [][2]string{{"", "07:00"}, {"9:00", "17:00"}, {"24:00", "07:00"}, {"23:60", "07:00"}, {"23:00:00", "07:00"}, {" 23:00", "07:00"}, {"ab:cd", "07:00"}, {"00:00", "00:00"}, {"23:00", "07:0"}} {
		c := DefaultConfig()
		c.NoRestockEnabled, c.NoRestockStart, c.NoRestockEnd = true, pair[0], pair[1]
		if c.Validate() == nil || c.NoRestockReason(time.Now()) == "" {
			t.Fatalf("invalid schedule accepted: %v", pair)
		}
		c.NoRestockEnabled = false
		if err := c.Validate(); err != nil {
			t.Fatalf("disabled schedule must allow saving: %v", err)
		}
	}
	c := DefaultConfig()
	if c.NoRestockEnabled || c.Validate() != nil {
		t.Fatal("incompatible defaults")
	}
	c.NoRestockEnabled = true
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestNoRestockGatesAllTriggersAndRetainsForecast(t *testing.T) {
	for _, mode := range []string{"auto", "observe"} {
		for _, trigger := range []string{"dual", "reserve", "empty", "replacement", "emergency"} {
			t.Run(mode+"/"+trigger, func(t *testing.T) {
				c, s, now := fixture()
				c.Mode = mode
				switch trigger {
				case "reserve":
					c.TimeEnabled, c.ConcurrencyEnabled, c.Reserve = false, false, 3
				case "empty":
					s.Accounts, c.EmptyGroupCount = nil, 2
				case "replacement":
					c.TimeEnabled, c.ConcurrencyEnabled, c.ForceMergeEnabled = false, false, true
					s.Accounts = []Account{timedAccount(1, 1, now)}
				case "emergency":
					s.Accounts[0].Waiting = 20
				}
				before := Evaluate(c, s, nil, Limits{}, 2, 20, 2, now)
				if before.Suggested < 1 {
					t.Fatal("fixture did not trigger", before)
				}
				c.NoRestockEnabled = true
				c.NoRestockStart = now.In(scheduleLocation).Add(-time.Hour).Format("15:04")
				c.NoRestockEnd = now.In(scheduleLocation).Add(time.Hour).Format("15:04")
				blocked := Evaluate(c, s, nil, Limits{}, 2, 20, 2, now)
				if blocked.Suggested != 0 || !blocked.NoRestockActive || !blocked.DemandKnown || !strings.Contains(blocked.Reason, "每日禁补") {
					t.Fatal(blocked)
				}
				if blocked.Capacity != before.Capacity || blocked.TimeNeed != before.TimeNeed || blocked.ConcurrencyNeed != before.ConcurrencyNeed || blocked.EmptyGroupNeed != before.EmptyGroupNeed || blocked.ForceMergeNeed != before.ForceMergeNeed || blocked.ReserveNeed != before.ReserveNeed {
					t.Fatal("monitoring data was lost", blocked)
				}
				if before.SupplyMinutes != nil && (blocked.SupplyMinutes == nil || *before.SupplyMinutes != *blocked.SupplyMinutes) {
					t.Fatal("forecast stopped")
				}
				s.Valid, s.Error = false, "collection failed"
				failed := Evaluate(c, s, nil, Limits{}, 2, 20, 2, now)
				if failed.Suggested != 0 || !strings.Contains(strings.Join(failed.Warnings, ";"), s.Error) {
					t.Fatal("collection error hidden", failed)
				}
			})
		}
	}
}

func TestNoRestockEndReevaluatesCurrentDemand(t *testing.T) {
	c := DefaultConfig()
	c.NoRestockEnabled, c.NoRestockStart, c.NoRestockEnd = true, "23:00", "07:00"
	c.EmptyGroupCount = 20
	at := time.Date(2026, 10, 17, 6, 59, 59, 0, scheduleLocation)
	s := Snapshot{At: at, Valid: true, LoadValid: true}
	if d := Evaluate(c, s, nil, Limits{}, 2, 20, 2, at); d.Suggested != 0 {
		t.Fatal(d)
	}
	at = at.Add(time.Second)
	s.At = at
	if d := Evaluate(c, s, nil, Limits{}, 2, 20, 2, at); d.NoRestockActive || d.Suggested != c.MaxBatch {
		t.Fatal("end must resume with batch limit", d)
	}
	s.Accounts = []Account{{Ready: true, Capacity: 20}}
	if d := Evaluate(c, s, nil, Limits{}, 2, 20, 2, at); d.Suggested != 0 {
		t.Fatal("old empty demand accumulated", d)
	}
}
