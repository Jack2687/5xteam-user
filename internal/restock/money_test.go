package restock

import (
	"math"
	"testing"
	"time"
)

func testMoney(now time.Time, used, cost, rate float64) *AccountMoney {
	return &AccountMoney{At: now, Day: now.UTC().Format("2006-01-02"), RateUSD: moneyPtr(rate), Windows: []MoneyWindow{{Seconds: 604800, ResetAt: now.Add(7 * 24 * time.Hour), UsedPercent: used, UsedUSD: cost}}}
}

func TestMoneySamplingThreeMinutesAndReset(t *testing.T) {
	now := time.Date(2026, 10, 16, 8, 0, 0, 0, time.UTC)
	var p *AccountMoney
	for i := 0; i <= 6; i++ {
		v := ObserveMoney(AccountMoney{At: now.Add(time.Duration(i) * 30 * time.Second), Day: "2026-10-16"}, p, 100+float64(i)*5)
		if i < 6 && v.RateUSD != nil {
			t.Fatal("partial window reported as a three-minute sample")
		}
		p = &v
	}
	if p.RateUSD == nil || *p.RateUSD != 10 {
		t.Fatal("dollar rate", p)
	}
	for _, tt := range []struct {
		name, day string
		at        time.Time
		cost      float64
	}{
		{"midnight", "2026-10-17", p.At.Add(time.Minute), 1},
		{"correction", "2026-10-16", p.At.Add(time.Minute), 90},
		{"gap", "2026-10-16", p.At.Add(3 * time.Minute), 160},
		{"duplicate", "2026-10-16", p.At, 130},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got := ObserveMoney(AccountMoney{At: tt.at, Day: tt.day}, p, tt.cost)
			if got.RateUSD != nil || len(got.Samples) != 1 {
				t.Fatal(got)
			}
		})
	}
	// A long idle period must decay to a real zero, not retain a percentage EMA.
	for i := 1; i <= 6; i++ {
		v := ObserveMoney(AccountMoney{At: p.At.Add(30 * time.Second), Day: p.Day}, p, 130)
		p = &v
	}
	if p.RateUSD == nil || *p.RateUSD != 0 {
		t.Fatal("idle must be zero", p)
	}
}

func TestMoneySamplingInterpolatesBoundary(t *testing.T) {
	now := time.Now()
	p := &AccountMoney{Day: "same", Samples: []MoneySample{{now.Add(-200 * time.Second), 10}, {now.Add(-140 * time.Second), 20}, {now.Add(-80 * time.Second), 30}, {now.Add(-20 * time.Second), 40}}}
	v := ObserveMoney(AccountMoney{At: now, Day: "same"}, p, 43.333333333333)
	if v.RateUSD == nil || math.Abs(*v.RateUSD-10) > 1e-8 {
		t.Fatal(v)
	}
}

func TestMoneyForecastDollarBasisAndIndependentRules(t *testing.T) {
	c, s, n := fixture()
	s.Accounts[0].Money = testMoney(n, 50, 266.41, 10)
	s.Accounts[0].Windows[0].Rate = 999
	d := Evaluate(c, s, nil, Limits{}, 2, 20, 2, n)
	if d.SupplyMinutes == nil || math.Abs(*d.SupplyMinutes-26.641) > 1e-8 || *d.Money.TotalUSD != 532.82 {
		t.Fatalf("wrong dollar formula: %+v", d)
	}
	s.Accounts[0].Money.Windows = append(s.Accounts[0].Money.Windows, MoneyWindow{Seconds: 18000, ResetAt: n.Add(time.Hour), UsedPercent: 80, UsedUSD: 80})
	d = Evaluate(c, s, nil, Limits{}, 2, 20, 2, n)
	if *d.SupplyMinutes != 2 || *d.Money.RemainingUSD != 20 {
		t.Fatal("windows added instead of taking limiting budget", d)
	}
	s.Accounts[0].Money.RateUSD = moneyPtr(0)
	d = Evaluate(c, s, nil, Limits{}, 2, 20, 2, n)
	if d.SupplyMinutes != nil || d.Money.State != "idle" || d.TimeNeed != 0 || d.ConcurrencyNeed == 0 {
		t.Fatal("zero rate must not suppress concurrency", d)
	}
}

func TestMoneyMissingAndStaleNeverFallsBackToPercent(t *testing.T) {
	for _, name := range []string{"missing", "partial", "stale", "stale-budget", "future", "failed", "zero-utilization", "negative", "infinite", "reset"} {
		t.Run(name, func(t *testing.T) {
			c, s, n := fixture()
			s.Accounts[0].Money = testMoney(n, 50, 100, 10)
			switch name {
			case "missing":
				s.Accounts[0].Money = nil
			case "partial":
				s.Accounts = append(s.Accounts, Account{ID: 2, Ready: true, Capacity: 20})
			case "stale":
				s.Accounts[0].Money.At = n.Add(-3 * time.Minute)
			case "stale-budget":
				s.Accounts[0].Money.BudgetAt = n.Add(-3 * time.Minute)
			case "future":
				s.Accounts[0].Money.At = n.Add(time.Minute)
			case "failed":
				s.Accounts[0].Money.Error = "unavailable"
			case "zero-utilization":
				s.Accounts[0].Money.Windows[0].UsedPercent = 0
			case "negative":
				s.Accounts[0].Money.Windows[0].UsedUSD = -1
			case "infinite":
				s.Accounts[0].Money.RateUSD = moneyPtr(math.Inf(1))
			case "reset":
				s.Accounts[0].Money.Windows[0].ResetAt = n
			}
			d := Evaluate(c, s, nil, Limits{}, 2, 20, 2, n)
			if d.SupplyMinutes != nil || d.TimeNeed != 0 {
				t.Fatal("untrusted money produced a forecast", d)
			}
		})
	}
}

func TestMoneyThresholdInflightAndGuards(t *testing.T) {
	c, s, n := fixture()
	c.ConcurrencyEnabled = false
	c.Early = false
	c.TriggerMinutes = 5
	s.Accounts[0].Money = testMoney(n, 90, 90, 2) // $10 remaining / $2 per minute = exactly 5
	d := Evaluate(c, s, nil, Limits{}, 2, 20, 2, n)
	if d.TimeNeed != 1 || d.Suggested != 1 {
		t.Fatal("threshold not inclusive", d)
	}
	eta := n.Add(8 * time.Minute)
	task := Task{ID: "new", Binding: s.Binding, GroupID: s.GroupID, Plan: s.NewPlan, Status: "running", ETA: &eta}
	d = Evaluate(c, s, []Task{task}, Limits{}, 2, 20, 2, n)
	if d.TimeNeed != 0 || *d.SupplyMinutes != 5 {
		t.Fatal("inflight must only reduce demand, never inflate displayed supply", d)
	}
	for _, status := range []string{"unknown", "attention", "failed", "cancelled"} {
		task.Status = status
		d = Evaluate(c, s, []Task{task}, Limits{}, 2, 20, 2, n)
		if d.TimeNeed != 1 {
			t.Fatal(status, d)
		}
	}
	task.Status = "verifying"
	task.AccountID = s.Accounts[0].ID
	if d = Evaluate(c, s, []Task{task}, Limits{}, 2, 20, 2, n); d.TimeNeed != 1 {
		t.Fatal("already-ready account credited twice", d)
	}
	c.DayBudget = 1
	c.UnitCost = 10
	if d = Evaluate(c, s, nil, Limits{}, 2, 20, 2, n); d.Suggested != 0 {
		t.Fatal("budget bypassed", d)
	}
	c.DayBudget = 0
	s.NewPlan = "different"
	if d = Evaluate(c, s, nil, Limits{}, 2, 20, 2, n); d.SupplyMinutes == nil || d.TimeNeed != 0 {
		t.Fatal("new plan changes existing dollars or assumes new budget", d)
	}
}

func TestConcurrencyUsesSameAvailableCohort(t *testing.T) {
	c, s, n := fixture()
	c.TimeEnabled = false
	s.Accounts[0].Used = 10
	expired := n.Add(-time.Second)
	s.Accounts = append(s.Accounts, Account{ID: 2, Ready: false, Used: 100, Capacity: 100}, Account{ID: 3, Ready: true, ExpiresAt: &expired, Used: 100, Capacity: 100})
	d := Evaluate(c, s, nil, Limits{}, 2, 20, 2, n)
	if d.Used != 10 || d.Capacity != 20 || d.ConcurrencyNeed != 0 || Pressure(c, s) {
		t.Fatalf("unavailable usage distorted percentage: %+v", d)
	}
	s.Accounts[0].Waiting = 20
	d = Evaluate(c, s, nil, Limits{}, 2, 20, 2, n)
	if d.Used != 10 || d.Waiting != 20 || d.ConcurrencyNeed == 0 {
		t.Fatal("queue must stay separate and still trigger demand", d)
	}
}
