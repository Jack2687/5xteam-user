package restock

import (
	"testing"
	"time"
)

func TestTakeoverWindowsOverlapMidnightAndExceptions(t *testing.T) {
	c := DefaultConfig()
	c.NoRestockEnabled = true
	c.NoRestockWindows = []DailyWindow{{Start: "17:00", End: "20:00"}, {Start: "19:00", End: "21:00"}, {Start: "23:00", End: "01:00"}, {Start: "06:00", End: "08:00", AllowedProvider: "nexovcc"}}
	for _, tt := range []struct {
		at, start, end string
		ok             bool
	}{
		{"2026-10-19T16:49:00+08:00", "", "", false},
		{"2026-10-19T16:50:00+08:00", "2026-10-19T17:00:00+08:00", "2026-10-19T21:00:00+08:00", true},
		{"2026-10-19T20:59:59+08:00", "2026-10-19T17:00:00+08:00", "2026-10-19T21:00:00+08:00", true},
		{"2026-10-19T21:00:00+08:00", "", "", false},
		{"2026-10-20T00:30:00+08:00", "2026-10-19T23:00:00+08:00", "2026-10-20T01:00:00+08:00", true},
		{"2026-10-19T06:30:00+08:00", "", "", false},
	} {
		now, _ := time.Parse(time.RFC3339, tt.at)
		start, end, ok := c.TakeoverWindow(now, 10)
		if ok != tt.ok || ok && (start.Format(time.RFC3339) != tt.start || end.Format(time.RFC3339) != tt.end) {
			t.Fatalf("%s => %v %v %v", tt.at, start, end, ok)
		}
	}
}
func TestTakeoverBorrowedSupplyDoesNotPreventRecoveryPurchases(t *testing.T) {
	c, s, now := fixture()
	c.TimeEnabled = false
	c.CapacityPercent = 60
	c.TriggerPercent = 70
	c.TargetPercent = 60
	s.Accounts = []Account{{ID: 1, Ready: true, Capacity: 80, Used: 20}, {ID: 2, Ready: true, SupplyExcluded: true, Capacity: 800, Used: 40}}
	d := Evaluate(c, s, nil, Limits{}, c.Confirmations, 80, 1, now)
	if d.Capacity != 48 || d.Used != 60 || d.Suggested != 2 {
		t.Fatalf("borrowed supply hid demand: %+v", d)
	}
	s.Accounts = s.Accounts[1:]
	d = Evaluate(c, s, nil, Limits{}, c.Confirmations, 80, 1, now)
	if d.Capacity != 0 || d.Used != 40 || d.Suggested != 2 {
		t.Fatalf("zero own capacity did not recover: %+v", d)
	}
	task := Task{ID: "pending", Status: "running", GroupID: s.GroupID, Binding: s.Binding}
	if Evaluate(c, s, []Task{task}, Limits{}, c.Confirmations, 80, 1, now).Suggested != 0 {
		t.Fatal("batch protection bypassed")
	}
	if Evaluate(c, s, nil, Limits{Failures: c.FailureLimit}, c.Confirmations, 80, 1, now).Suggested != 0 {
		t.Fatal("breaker bypassed")
	}
}
func TestTakeoverMoneyKeepsDemandButExcludesBorrowedBudget(t *testing.T) {
	c, s, now := fixture()
	a := s.Accounts[0]
	a.ID = 2
	a.SupplyExcluded = true
	a.Money = testMoney(now, 50, 50, 3)
	s.Accounts = append(s.Accounts, a)
	f := ForecastMoney(c, s, now)
	if f.RateUSD == nil || *f.RateUSD != 5 || f.RemainingUSD == nil || *f.RemainingUSD != 10 {
		t.Fatalf("double counted budget or lost demand: %+v", f)
	}
}
func TestTakeoverRecoveryNeedsFreshOwnReadySupply(t *testing.T) {
	c, s, now := fixture()
	c.TimeEnabled = false
	s.CollectionStartedAt = now.Add(-time.Second)
	tc := DefaultTakeoverConfig()
	after := now.Add(-time.Minute)
	s.Accounts[0].Used = 2
	ready, _, _, _ := TakeoverRecoveryReady(c, tc, s, 0, after, now)
	if !ready {
		t.Fatal("own supply not recognized")
	}
	for _, kind := range []string{"borrowed", "inflight", "stale", "before", "demand", "exiting"} {
		v := s
		v.Accounts = append([]Account{}, s.Accounts...)
		pending := 0
		switch kind {
		case "borrowed":
			v.Accounts[0].SupplyExcluded = true
		case "inflight":
			pending = 1
		case "stale":
			v.At = now.Add(-time.Hour)
		case "before":
			v.CollectionStartedAt = after.Add(-time.Second)
		case "demand":
			v.Accounts[0].Used = 100
		case "exiting":
			deadline := now.Add(time.Second)
			v.Accounts[0].ForceMergeDeadline = &deadline
		}
		if ready, _, _, _ := TakeoverRecoveryReady(c, tc, v, pending, after, now); ready {
			t.Fatal("unsafe release:", kind)
		}
	}
}
func TestTakeoverExpiredGrantRetainsOwnershipBarrier(t *testing.T) {
	c := DefaultTakeoverConfig()
	c.Role = "provider"
	c.Enabled = true
	now := time.Now()
	s := TakeoverState{Epoch: "epoch", Phase: "active", CoverageReady: true, ContactAt: now, LeaseUntil: now.Add(time.Second)}
	if s.Block(c, now) != "" {
		t.Fatal("fresh grant blocked")
	}
	if s.Block(c, now.Add(2*time.Second)) == "" {
		t.Fatal("expired grant accepted")
	}
	c.Role = "receiver"
	if s.Block(c, now) == "" {
		t.Fatal("both projects allowed to purchase")
	}
	s.Phase = "recovering"
	s.PeerPending = 1
	if s.Block(c, now.Add(2*time.Second)) == "" {
		t.Fatal("ignored peer batch")
	}
	s.PeerPending = 0
	if s.Block(c, now.Add(2*time.Second)) != "" {
		t.Fatal("recovery blocked")
	}
}
