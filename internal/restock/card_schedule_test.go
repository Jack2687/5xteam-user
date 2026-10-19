package restock

import (
	"testing"
	"time"
)

func TestCardScheduleWholeDayAndIndependentMerge(t *testing.T) {
	c := DefaultConfig()
	c.NoRestockEnabled, c.NoRestockFallback, c.DailyEJHMergeEnabled = true, true, true
	c.DailyEJHMergeTime = "17:30"
	c.NoRestockWindows = []DailyWindow{
		{Start: "02:00", End: "09:00"},
		{Start: "17:00", End: "20:50", AllowedProvider: "nexovcc"},
	}
	day := time.Date(2026, 10, 18, 0, 0, 0, 0, scheduleLocation)
	for minute := 0; minute < 1440; minute++ {
		now := day.Add(time.Duration(minute) * time.Minute).UTC()
		policy := c.CardSchedule(now)
		blocked := minute >= 120 && minute < 540
		allowed := minute >= 1020 && minute < 1250
		if policy.Active != (blocked || allowed) || policy.Blocked != blocked || (c.NoRestockReason(now) != "") != blocked || c.ScheduledFallbackActive(now) != blocked {
			t.Fatalf("minute %d: %+v", minute, policy)
		}
		provider := "ejh"
		if allowed {
			provider = "nexovcc"
		}
		if c.EffectiveCardProvider(now, "ejh") != provider {
			t.Fatalf("wrong issuer at %d", minute)
		}
		deadline, ok := c.DailyEJHMergeDeadline(now)
		if !ok || deadline.Sub(day) != 17*time.Hour+30*time.Minute {
			t.Fatal("daily merge changed", deadline)
		}
	}
}

func TestCardScheduleOverlapAndOvernight(t *testing.T) {
	c := DefaultConfig()
	c.NoRestockEnabled = true
	c.NoRestockWindows = []DailyWindow{{Start: "23:00", End: "02:00", AllowedProvider: "nexovcc"}, {Start: "01:00", End: "03:00", AllowedProvider: "nexovcc"}, {Start: "00:30", End: "01:00"}}
	for _, tc := range []struct {
		at, provider, end string
		blocked, active   bool
	}{
		{"23:00", "nexovcc", "02:00", false, true},
		{"00:29", "nexovcc", "02:00", false, true},
		{"00:30", "", "01:00", true, true},
		{"01:00", "nexovcc", "03:00", false, true},
		{"02:00", "nexovcc", "03:00", false, true},
		{"03:00", "", "", false, false},
	} {
		at, _ := time.Parse(time.RFC3339, "2026-10-18T"+tc.at+":00+08:00")
		p := c.CardSchedule(at)
		if p.Provider != tc.provider || p.End != tc.end || p.Blocked != tc.blocked || p.Active != tc.active {
			t.Fatal(tc.at, p)
		}
	}
	// Prohibition has the same precedence regardless of row order.
	c.NoRestockWindows[0], c.NoRestockWindows[2] = c.NoRestockWindows[2], c.NoRestockWindows[0]
	at, _ := time.Parse(time.RFC3339, "2026-10-18T00:30:00+08:00")
	if !c.CardSchedule(at).Blocked {
		t.Fatal("row order bypassed prohibition")
	}
	c.NoRestockWindows[1].AllowedProvider = "ejh"
	if c.Validate() == nil || c.NoRestockReason(at) == "" {
		t.Fatal("conflicting issuers accepted")
	}
	c.NoRestockWindows = []DailyWindow{{Start: "23:00", End: "01:00", AllowedProvider: "ejh"}, {Start: "01:00", End: "02:00", AllowedProvider: "nexovcc"}}
	if err := c.Validate(); err != nil {
		t.Fatal("adjacent windows conflict", err)
	}
	c.NoRestockWindows[0].AllowedProvider = "invalid"
	if c.Validate() == nil {
		t.Fatal("unknown issuer accepted")
	}
}

func TestCardScheduleExceptionPreservesDemandAndProtections(t *testing.T) {
	c, s, now := fixture()
	baseline := Evaluate(c, s, nil, Limits{}, 2, 20, 2, now)
	c.NoRestockEnabled = true
	c.NoRestockWindows = []DailyWindow{{Start: now.In(scheduleLocation).Add(-time.Hour).Format("15:04"), End: now.In(scheduleLocation).Add(time.Hour).Format("15:04"), AllowedProvider: "nexovcc"}}
	d := Evaluate(c, s, nil, Limits{}, 2, 20, 2, now)
	if d.NoRestockActive || d.Suggested != baseline.Suggested || d.Suggested < 1 {
		t.Fatal("exception changed demand", d)
	}
	if d := Evaluate(c, s, nil, Limits{Failures: c.FailureLimit}, 2, 20, 2, now); d.Suggested != 0 {
		t.Fatal("breaker bypassed")
	}
	if d := Evaluate(c, s, nil, Limits{DayCount: c.DayLimit}, 2, 20, 2, now); d.Suggested != 0 {
		t.Fatal("daily limit bypassed")
	}
	if d := Evaluate(c, s, nil, Limits{LastBatch: &now}, 2, 20, 2, now); d.Suggested != 0 {
		t.Fatal("cooldown bypassed")
	}
	c.TimeEnabled, c.ConcurrencyEnabled, c.ForceMergeEnabled, c.ExitEnabled = false, false, false, false
	c.Reserve, c.EmptyGroupCount = 0, 0
	if d := Evaluate(c, s, nil, Limits{}, 0, 20, 2, now); d.Suggested != 0 {
		t.Fatal("exception forced restock without demand")
	}
}

func TestCardScheduleRequestedBoundariesAndGates(t *testing.T) {
	for _, end := range []string{"20:00", "20:50"} {
		t.Run(end, func(t *testing.T) {
			c, s, _ := fixture()
			c.NoRestockEnabled, c.DailyEJHMergeEnabled = true, true
			c.DailyEJHMergeTime = "17:30"
			c.NoRestockWindows = []DailyWindow{{Start: "02:00", End: "09:00"}, {Start: "17:00", End: end, AllowedProvider: "nexovcc"}}
			for _, at := range []string{"01:59:59", "02:00:00", "08:59:59", "09:00:00", "16:59:59", "17:00:00", "17:29:59", "17:30:00", "19:59:59", "20:00:00", "20:49:59", "20:50:00", "23:59:59"} {
				now, _ := time.Parse(time.RFC3339, "2026-10-18T"+at+"+08:00")
				blocked := at >= "02:00:00" && at < "09:00:00"
				allowed := at >= "17:00:00" && at < end+":00"
				policy := c.CardSchedule(now.UTC())
				if policy.Blocked != blocked || (policy.Provider == "nexovcc") != allowed {
					t.Fatalf("%s: %+v", at, policy)
				}
				s.At = now
				// Explicit empty-group demand avoids dependence on historical quota data.
				s.Accounts = nil
				c.EmptyGroupCount = 1
				d := Evaluate(c, s, nil, Limits{}, 2, 20, 2, now)
				if (d.Suggested == 0) != blocked {
					t.Fatalf("%s: demand gate=%d", at, d.Suggested)
				}
				for _, fallback := range []bool{false, true} {
					c.NoRestockFallback = fallback
					if c.ScheduledFallbackActive(now) != (fallback && blocked) {
						t.Fatal("fallback exception bypass", at)
					}
				}
				deadline, enabled := c.DailyEJHMergeDeadline(now)
				if !enabled || (!now.Before(deadline)) != (at >= "17:30:00") {
					t.Fatal("daily merge boundary", at)
				}
			}
			c.NoRestockEnabled = false
			now, _ := time.Parse(time.RFC3339, "2026-10-18T18:00:00+08:00")
			if c.CardSchedule(now).Active || c.EffectiveCardProvider(now, "ejh") != "ejh" {
				t.Fatal("disabled windows still override issuer")
			}
		})
	}
}
