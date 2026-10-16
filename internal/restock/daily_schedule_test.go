package restock

import (
	"testing"
	"time"
)

func TestDailyScheduleBoundaries(t *testing.T) {
	c := DefaultConfig()
	c.NoRestockEnabled = true
	c.NoRestockFallback = true
	c.NoRestockWindows = []DailyWindow{{Start: "17:00", End: "20:50"}}
	c.DailyEJHMergeEnabled = true
	c.DailyEJHMergeTime = "17:30"
	f := DefaultFallbackConfig()
	f.Enabled = true
	f.URL = "https://example.test"
	f.Email = "admin@test"
	f.GroupID = 1
	f.AccountIDs = []int64{1}
	for _, tc := range []struct {
		at             string
		scheduled, due bool
	}{{"16:59:59", false, false}, {"17:00:00", true, false}, {"17:29:59", true, false}, {"17:30:00", true, true}, {"20:49:59", true, true}, {"20:50:00", false, true}} {
		now, _ := time.Parse(time.RFC3339, "2026-10-16T"+tc.at+"+08:00")
		if c.ScheduledFallbackActive(now.UTC()) != tc.scheduled {
			t.Fatal("schedule boundary", tc.at)
		}
		deadline, ok := c.DailyEJHMergeDeadline(now.UTC())
		if !ok || !now.Before(deadline) != tc.due {
			t.Fatal("daily deadline", tc.at, deadline)
		}
		p := f.PlanWithSchedule(now, FallbackObservation{Error: "source down"}, c)
		if p.Active != tc.scheduled || p.Hold == tc.scheduled {
			t.Fatal("scheduled override", tc.at, p)
		}
	}
	c.NoRestockWindows = []DailyWindow{{Start: "17:00", End: "17:00"}}
	if c.ScheduledFallbackActive(time.Now()) {
		t.Fatal("invalid window enabled fallback")
	}
	c.NoRestockWindows = []DailyWindow{{Start: "17:00", End: "20:50"}}
	for _, invalid := range []string{"", "17:60", "24:00", "7:30"} {
		c.DailyEJHMergeTime = invalid
		if c.Validate() == nil {
			t.Fatal("invalid daily time accepted", invalid)
		}
	}
}
