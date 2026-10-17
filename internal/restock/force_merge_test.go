package restock

import (
	"fmt"
	"testing"
	"time"
)

func timedAccount(id int64, minutes float64, now time.Time) Account {
	enabled := true
	at := now.Add(time.Duration(minutes * float64(time.Minute)))
	return Account{ID: id, Ready: true, Capacity: 20, EJH: true, PushTimeoutEnabled: &enabled, ForceMergeKey: fmt.Sprint(id), ForceMergeDeadline: &at}
}

func TestForceMergeIndependentAverageAndExclusions(t *testing.T) {
	c, s, now := fixture()
	c.TimeEnabled = false
	c.ConcurrencyEnabled = false
	c.ForceMergeEnabled = true
	off := false
	s.Accounts = []Account{timedAccount(1, 11, now), {ID: 2, Ready: true, EJH: true, PushTimeoutEnabled: &off}, {ID: 3, Ready: true, EJH: true}, {ID: 4, Ready: true}, {ID: 5, EJH: true}}
	d := Evaluate(c, s, nil, Limits{}, 0, 20, 5, now)
	if d.Suggested != 1 || d.ForceMergeNeed != 1 || d.ForceMerge.Participants != 1 || d.ForceMerge.Untimed != 1 || d.ForceMerge.Unknown != 1 || *d.ForceMerge.AverageMinutes != 11 {
		t.Fatalf("wrong independent decision: %+v", d)
	}
	s.Accounts = append(s.Accounts, timedAccount(6, 19, now))
	d = Evaluate(c, s, nil, Limits{}, 0, 20, 5, now)
	if d.ForceMerge.Triggered || d.Suggested != 0 || *d.ForceMerge.AverageMinutes != 15 {
		t.Fatal("equal threshold must not trigger", d)
	}
	s.Accounts[0] = timedAccount(1, -20, now)
	d = Evaluate(c, s, nil, Limits{}, 0, 20, 5, now)
	if *d.ForceMerge.AverageMinutes != 9.5 || d.ForceMergeNeed != 1 {
		t.Fatal("clamp expired then divide by eligible count", d)
	}
	c.Reserve = 5
	d = Evaluate(c, s, nil, Limits{}, 0, 20, 5, now)
	if d.Suggested != max(d.ReserveNeed, d.ForceMergeNeed) {
		t.Fatal("rules added instead of max", d)
	}
	c.ForceMergeEnabled = false
	if Evaluate(c, s, nil, Limits{}, 0, 20, 5, now).ForceMergeNeed != 0 {
		t.Fatal("disabled rule triggered")
	}
}

func TestForceMergeCoverageAndHardLimits(t *testing.T) {
	c, s, now := fixture()
	c.TimeEnabled = false
	c.ConcurrencyEnabled = false
	c.ForceMergeEnabled = true
	s.Accounts = []Account{timedAccount(1, 1, now), timedAccount(2, 2, now)}
	for _, status := range []string{"reserved", "running", "unknown", "attention", "verifying"} {
		tasks := []Task{{ID: "one", Status: status, ForceMergeSource: "1", Binding: s.Binding, GroupID: s.GroupID}}
		d := Evaluate(c, s, tasks, Limits{}, 0, 20, 5, now)
		if d.ForceMergeNeed != 1 || d.ForceMerge.Pending != 1 || d.Suggested != 0 || d.BatchWait == nil {
			t.Fatal(status, d)
		}
	}
	tasks := []Task{{ID: "one", Status: "ready", AccountID: 3, ForceMergeSource: "1", Binding: s.Binding, GroupID: s.GroupID}}
	if ForceMergeSummary(c, s, tasks, now).Ready != 0 {
		t.Fatal("unverified replacement counted ready")
	}
	s.Accounts = append(s.Accounts, Account{ID: 3, Ready: true, Capacity: 20})
	if d := Evaluate(c, s, tasks, Limits{}, 0, 20, 5, now); d.ForceMergeNeed != 1 || d.ForceMerge.Ready != 1 {
		t.Fatal(d)
	}
	for _, mutate := range []func(*Config, *Snapshot, *Limits){
		func(c *Config, s *Snapshot, l *Limits) { s.Valid = false },
		func(c *Config, s *Snapshot, l *Limits) { s.LoadValid = false },
		func(c *Config, s *Snapshot, l *Limits) { s.At = now.Add(-time.Hour) },
		func(c *Config, s *Snapshot, l *Limits) { l.Failures = c.FailureLimit },
		func(c *Config, s *Snapshot, l *Limits) { l.LastBatch = &now },
		func(c *Config, s *Snapshot, l *Limits) { c.DayBudget = 1; c.UnitCost = 2 },
		func(c *Config, s *Snapshot, l *Limits) { c.MaxInflight = 1 },
	} {
		cc, ss, l := c, s, Limits{}
		mutate(&cc, &ss, &l)
		active := []Task{{Status: "unknown", GroupID: s.GroupID, Binding: s.Binding, ForceMergeSource: "1"}}
		if d := Evaluate(cc, ss, active, l, 0, 20, 5, now); d.Suggested != 0 {
			t.Fatal("guard bypass", d)
		}
	}
}

func TestForceMergeScansEntireSnapshotAndForecastIndependence(t *testing.T) {
	c, s, now := fixture()
	c.ForceMergeEnabled = true
	base := Evaluate(c, s, nil, Limits{}, 2, 20, 2, now)
	s.LifetimeMinutes = 1
	c.MinNewUsePercent = 100
	s.NewPlan = "different-vendor"
	s.Accounts[0].ForceMergeDeadline = &now
	s.Accounts[0].EJH = true
	enabled := true
	s.Accounts[0].PushTimeoutEnabled = &enabled
	s.Accounts[0].ForceMergeKey = "old"
	eta := now.Add(time.Minute)
	d := Evaluate(c, s, []Task{{Plan: s.NewPlan, Status: "running", GroupID: s.GroupID, Binding: s.Binding, ETA: &eta}}, Limits{}, 2, 20, 2, now)
	if d.SupplyMinutes == nil || *d.SupplyMinutes != *base.SupplyMinutes {
		t.Fatal("local policy/provider/work changed upstream forecast", d)
	}
	c.TimeEnabled = false
	c.ConcurrencyEnabled = false
	s.Accounts = nil
	for i := int64(1); i <= 405; i++ {
		s.Accounts = append(s.Accounts, timedAccount(i, 10, now))
	}
	if stats := ForceMergeSummary(c, s, nil, now); stats.Participants != 405 || len(stats.Sources) != 405 {
		t.Fatal("statistics paginated", stats)
	}
}
