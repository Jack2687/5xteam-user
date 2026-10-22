package restock

import (
	"fmt"
	"testing"
	"time"
)

func overlapFixture() (Config, Snapshot, time.Time) {
	c, s, now := fixture()
	c.TimeEnabled, c.ConcurrencyEnabled, c.ForceMergeEnabled, c.ExitEnabled = true, true, true, true
	c.MinBatch, c.MaxBatch, c.MaxInflight = 1, 10, 10
	c.ForceMergeMinutes, c.ExitMinutes = 5, 5
	enabled := true
	deadline := now.Add(4 * time.Minute)
	s.ExitConfirmations = c.Confirmations
	s.Accounts = []Account{{ID: 1, Plan: s.NewPlan, Ready: true, Capacity: 50, Used: 30,
		Money: testMoney(now, 96, 96, 1), EJH: true, PushTimeoutEnabled: &enabled,
		ForceMergeDeadline: &deadline, ExitDeadline: &deadline, ForceMergeKey: "original"}}
	return c, s, now
}

func TestRestockOverlapAllRuleCombinations(t *testing.T) {
	// Independent triggers must reach the same admission path, never add their
	// orders. Include current concurrency as well as the three time-based cards.
	for mask := 0; mask < 16; mask++ {
		t.Run(fmt.Sprintf("rules_%04b", mask), func(t *testing.T) {
			c, s, now := overlapFixture()
			c.TimeEnabled, c.ForceMergeEnabled, c.ExitEnabled, c.ConcurrencyEnabled = mask&1 != 0, mask&2 != 0, mask&4 != 0, mask&8 != 0
			s.Accounts[0].Used = 45
			d := Evaluate(c, s, nil, Limits{}, c.Confirmations, 50, 2, now)
			want := 0
			if mask != 0 {
				want = 1
			}
			if mask&4 != 0 {
				want = 2 // After the old account exits, 45 / 75% needs two 50-slot accounts.
			}
			if d.Suggested != want || d.Trace.NetNeed != want {
				t.Fatalf("overlapping rules ordered separately: time=%d force=%d exit=%d concurrency=%d suggested=%d", d.TimeNeed, d.ForceMergeNeed, d.ExitNeed, d.ConcurrencyNeed, d.Suggested)
			}
		})
	}
}

func TestRestockOverlapDifferentNeedsAndHardLimits(t *testing.T) {
	c, s, now := overlapFixture()
	c.ConcurrencyEnabled = false
	s.Accounts[0].Used = 80
	s.Accounts[0].Money = testMoney(now, 96, 96, 14)
	d := Evaluate(c, s, nil, Limits{}, c.Confirmations, 50, 2, now)
	if d.TimeNeed != 3 || d.ForceMergeNeed != 1 || d.ExitNeed != 3 || d.Suggested != 3 {
		t.Fatalf("3/1/3 should order 3, not 7: %+v", d)
	}
	for _, tc := range []struct {
		name      string
		configure func(*Config, *Snapshot, *Limits)
		want      int
	}{
		{"batch", func(c *Config, _ *Snapshot, _ *Limits) { c.MaxBatch = 2 }, 2},
		{"inflight", func(c *Config, _ *Snapshot, _ *Limits) { c.MaxInflight = 2 }, 2},
		{"pool", func(c *Config, _ *Snapshot, _ *Limits) { c.PoolLimit = 2 }, 1},
		{"hour", func(c *Config, _ *Snapshot, l *Limits) { l.HourCount = c.HourLimit - 1 }, 1},
		{"day", func(c *Config, _ *Snapshot, l *Limits) { l.DayCount = c.DayLimit }, 0},
		{"budget", func(c *Config, _ *Snapshot, _ *Limits) { c.UnitCost, c.DayBudget = 10, 20 }, 2},
		{"cooldown", func(_ *Config, _ *Snapshot, l *Limits) { l.LastBatch = &now }, 0},
		{"breaker", func(c *Config, _ *Snapshot, l *Limits) { l.Failures = c.FailureLimit }, 0},
		{"missing_load", func(_ *Config, s *Snapshot, _ *Limits) { s.LoadValid = false }, 0},
		{"stale", func(_ *Config, s *Snapshot, _ *Limits) { s.At = now.Add(-time.Hour) }, 0},
		{"pause", func(c *Config, _ *Snapshot, _ *Limits) {
			c.NoRestockEnabled = true
			c.NoRestockStart, c.NoRestockEnd = now.In(scheduleLocation).Add(-time.Minute).Format("15:04"), now.In(scheduleLocation).Add(time.Minute).Format("15:04")
		}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, snap, limits := c, s, Limits{}
			tc.configure(&cfg, &snap, &limits)
			got := Evaluate(cfg, snap, nil, limits, cfg.Confirmations, 50, 2, now)
			if got.Suggested != tc.want {
				t.Fatalf("want %d, got %+v", tc.want, got)
			}
		})
	}
}

func TestRestockOverlapOneReplacementCoversAllThreeRules(t *testing.T) {
	c, s, now := overlapFixture()
	eta := now.Add(time.Minute)
	task := Task{ID: "incoming", Status: "running", Plan: s.NewPlan, Binding: s.Binding, GroupID: s.GroupID,
		ETA: &eta, Capacity: 50, ForceMergeSource: "original"}
	for _, status := range []string{"reserved", "submitting", "running", "verifying", "ready"} {
		t.Run(status, func(t *testing.T) {
			snap, incoming := s, task
			incoming.Status = status
			if status == "ready" {
				incoming.AccountID, incoming.ETA = 2, nil
				snap.Accounts = append(append([]Account{}, s.Accounts...), Account{ID: 2, Ready: true, Plan: s.NewPlan, Capacity: 50, Money: testMoney(now, 1, 1, 0)})
			}
			// Use empty limits: this must be deduplication, not a cooldown hiding it.
			d := Evaluate(c, snap, []Task{incoming}, Limits{}, c.Confirmations, 50, 2, now)
			if d.Suggested != 0 || d.TimeNeed != 0 || d.ForceMergeNeed != 0 || d.ExitNeed != 0 {
				t.Fatalf("same replacement counted again in %s: %+v", status, d)
			}
		})
	}
	task.Status, task.ETA = "unknown", nil
	c.MaxInflight = 1
	d := Evaluate(c, s, []Task{task}, Limits{}, c.Confirmations, 50, 2, now)
	if d.Suggested != 0 || d.Inflight != 1 || d.ForceMergeNeed != 0 || d.ExitNeed != 1 {
		t.Fatal("unknown payment lost occupancy or promised capacity", d)
	}
	// The configured strict minimum deliberately rounds up the net need once.
	c.MaxInflight, c.MinBatch, c.AllowSmall = 10, 3, false
	if d = Evaluate(c, s, nil, Limits{}, c.Confirmations, 50, 2, now); d.Suggested != 3 {
		t.Fatal("minimum must apply once to the combined net need", d)
	}
}
