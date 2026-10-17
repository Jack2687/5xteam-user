package restock

import (
	"testing"
	"time"
)

func fixture() (Config, Snapshot, time.Time) {
	n := time.Now()
	c := DefaultConfig()
	c.CapacityPercent = 100 // Legacy scenarios exercise the unscaled policy explicitly.
	c.Enabled = true
	c.Mode = "auto"
	s := Snapshot{ID: "s", At: n, Valid: true, LoadValid: true, GroupID: 1, Binding: "pool", NewPlan: "pro20", Accounts: []Account{{ID: 1, Plan: "pro20", Ready: true, Capacity: 20, Used: 19, Windows: []Window{{Seconds: 18000, ResetAt: n.Add(time.Hour).Unix(), Used: 90, Rate: 2, Samples: 3, At: n}}}}}
	s.Accounts[0].Money = testMoney(n, 90, 90, 2)
	return c, s, n
}
func TestDecisionRules(t *testing.T) {
	c, s, n := fixture()
	d := Evaluate(c, s, nil, Limits{}, 2, 20, 2, n)
	if d.TimeNeed == 0 || d.ConcurrencyNeed == 0 || d.Suggested != max(d.TimeNeed, d.ConcurrencyNeed) {
		t.Fatalf("dual rules should max: %+v", d)
	}
	c.TimeEnabled = false
	d = Evaluate(c, s, nil, Limits{}, 1, 20, 2, n)
	if d.Suggested != 0 {
		t.Fatal("transient pressure")
	}
	d = Evaluate(c, s, nil, Limits{}, 2, 20, 2, n)
	if d.Suggested != 1 {
		t.Fatal(d)
	}
	c.AllowSmall = false
	d = Evaluate(c, s, nil, Limits{}, 2, 20, 2, n)
	if d.Suggested != 3 {
		t.Fatal("strict minimum")
	}
	c.HourLimit = 2
	d = Evaluate(c, s, nil, Limits{}, 2, 20, 2, n)
	if d.Suggested != 0 {
		t.Fatal("hard cap cannot round up")
	}
}

func TestStaleQuotaDoesNotInflateConcurrencyNeed(t *testing.T) {
	c, s, now := fixture()
	for _, age := range []time.Duration{time.Hour, -time.Minute} {
		s.Accounts[0].Windows[0].At = now.Add(-age)
		s.Accounts[0].Windows[0].Used = 100
		d := Evaluate(c, s, nil, Limits{}, 2, 20, 2, now)
		if d.ConcurrencyNeed != 1 || d.SupplyMinutes == nil || *d.SupplyMinutes != 5 {
			t.Fatalf("quota age %v used for prediction: %+v", age, d)
		}
	}
}
func TestInflightAndCooldown(t *testing.T) {
	c, s, n := fixture()
	c.TimeEnabled = false
	eta := n.Add(time.Minute)
	task := Task{Status: "running", Binding: s.Binding, GroupID: 1, ETA: &eta}
	d := Evaluate(c, s, []Task{task}, Limits{}, 2, 20, 2, n)
	if d.Suggested != 0 || d.Capacity != 20 {
		t.Fatal("inflight counted as usable or double counted")
	}
	task.ETA = nil
	d = Evaluate(c, s, []Task{task}, Limits{}, 2, 20, 2, n)
	if d.Suggested != 0 || d.ConcurrencyNeed != 1 || d.BatchWait == nil {
		t.Fatal("unknown arrival must retain demand but block the next batch")
	}
	last := n.Add(-10 * time.Second)
	d = Evaluate(c, s, nil, Limits{LastBatch: &last}, 2, 20, 2, n)
	if d.Suggested != 0 {
		t.Fatal("cooldown")
	}
	last = n.Add(-61 * time.Second)
	d = Evaluate(c, s, nil, Limits{LastBatch: &last}, 2, 20, 2, n)
	if d.Suggested != 1 {
		t.Fatal("must continue without recovering above threshold")
	}
}
func TestDataAndEmptyPool(t *testing.T) {
	c, s, n := fixture()
	s.LoadValid = false
	if Evaluate(c, s, nil, Limits{}, 2, 20, 2, n).Suggested != 0 {
		t.Fatal("unknown load")
	}
	s.LoadValid = true
	s.Accounts[0].Windows = nil
	s.Accounts[0].Money = nil
	d := Evaluate(c, s, nil, Limits{}, 2, 20, 2, n)
	if d.SupplyMinutes != nil || d.Suggested != 1 {
		t.Fatal("concurrency independent of quota")
	}
	s.Accounts = nil
	if Evaluate(c, s, nil, Limits{}, 2, 20, 2, n).Suggested != 0 {
		t.Fatal("empty no demand")
	}
	c.Reserve = 1
	if Evaluate(c, s, nil, Limits{}, 2, 20, 2, n).Suggested != 1 {
		t.Fatal("explicit reserve")
	}
	s.At = n.Add(-time.Hour)
	if Evaluate(c, s, nil, Limits{}, 2, 20, 2, n).Suggested != 0 {
		t.Fatal("stale")
	}
}
func TestWindowResetAndPlan(t *testing.T) {
	n := time.Now()
	p := []Window{{Seconds: 18000, ResetAt: 42, Used: 80, Rate: 2, Samples: 5, At: n.Add(-time.Minute)}}
	v := ObserveWindows([]Window{{Seconds: 18000, ResetAt: 43, Used: 5, At: n}}, p, true, time.Hour)
	if v[0].Rate != 0 || v[0].Samples != 0 {
		t.Fatal("reset as negative burn")
	}
	v = ObserveWindows([]Window{{Seconds: 18000, ResetAt: 42, Used: 85, At: n}}, p, false, time.Hour)
	if v[0].Samples != 0 {
		t.Fatal("plan change")
	}
	v = ObserveWindows([]Window{{Seconds: 18000, ResetAt: 42, Used: 85, At: n}}, p, true, time.Hour)
	if v[0].Rate != 5 || v[0].Samples != 6 {
		t.Fatal(v)
	}
}
func TestArrivalExpiryAndLimits(t *testing.T) {
	c, s, n := fixture()
	c.ConcurrencyEnabled = false
	d := Evaluate(c, s, nil, Limits{}, 2, 20, 8, n)
	if len(d.Warnings) == 0 || d.SupplyMinutes == nil || *d.SupplyMinutes >= 8 {
		t.Fatal("prearrival gap missing")
	}
	c.ForecastMinutes = 16
	d = Evaluate(c, s, nil, Limits{}, 2, 20, 16, n)
	if d.Suggested != 0 {
		t.Fatal("forecast cap")
	}
	c.ForecastMinutes = 60
	c.DayBudget = 10
	c.UnitCost = 20
	if Evaluate(c, s, nil, Limits{}, 2, 20, 2, n).Suggested != 0 {
		t.Fatal("budget")
	}
	c.DayBudget = 0
	d = Evaluate(c, s, nil, Limits{Failures: c.FailureLimit}, 2, 20, 2, n)
	if d.Suggested != 0 {
		t.Fatal("breaker")
	}
	s.NewPlan = "other"
	if Evaluate(c, s, nil, Limits{}, 2, 20, 2, n).SupplyMinutes == nil {
		t.Fatal("purchase configuration must not invalidate the upstream forecast")
	}
}
func TestConfigValidation(t *testing.T) {
	c := DefaultConfig()
	c.CapacityPercent = 100 // Legacy scenarios exercise the unscaled policy explicitly.
	if c.Validate() != nil {
		t.Fatal(c.Validate())
	}
	c.MinBatch = 6
	if c.Validate() == nil {
		t.Fatal("invalid batch")
	}
	c = DefaultConfig()
	c.DayBudget = 100
	if c.Validate() == nil {
		t.Fatal("budget needs cost")
	}
}
func TestExistingArrivalCoversFutureDespiteEarlyGap(t *testing.T) {
	c, s, n := fixture()
	c.ConcurrencyEnabled = false
	eta := n.Add(8 * time.Minute)
	task := Task{Status: "running", Binding: s.Binding, GroupID: s.GroupID, Plan: s.NewPlan, ETA: &eta}
	d := Evaluate(c, s, []Task{task}, Limits{}, 2, 20, 8, n)
	if d.TimeNeed != 0 || d.Suggested != 0 || len(d.Warnings) == 0 {
		t.Fatalf("early gap must not cause duplicate purchase: %+v", d)
	}
	task.Status = "unknown"
	d = Evaluate(c, s, []Task{task}, Limits{}, 2, 20, 8, n)
	if d.TimeNeed == 0 {
		t.Fatal("unknown order must not promise capacity even with an old ETA")
	}
}

func TestUpstreamFaultAndLifetimeGuard(t *testing.T) {
	c, s, n := fixture()
	s.Accounts[0].Fault = true
	d := Evaluate(c, s, nil, Limits{}, 2, 20, 2, n)
	if d.Suggested != 0 || d.DemandKnown {
		t.Fatal("fault interpreted as demand")
	}
	s.Accounts[0].Fault = false
	c.ConcurrencyEnabled = false
	s.Accounts[0].Used = 1
	baseline := Evaluate(c, s, nil, Limits{}, 2, 20, 2, n)
	c.MinNewUsePercent = 100
	s.LifetimeMinutes = 5
	d = Evaluate(c, s, nil, Limits{}, 2, 20, 2, n)
	if d.TimeNeed != baseline.TimeNeed || d.Suggested != baseline.Suggested || d.SupplyMinutes == nil || *d.SupplyMinutes != *baseline.SupplyMinutes {
		t.Fatal("Pro lifetime changed upstream supply forecast")
	}
	c.ConcurrencyEnabled = true
	s.Accounts[0].Used = 19
	if Evaluate(c, s, nil, Limits{}, 2, 20, 2, n).ConcurrencyNeed == 0 {
		t.Fatal("lifetime guard suppressed reliable pressure")
	}
}
