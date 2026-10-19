package restock

import (
	"encoding/json"
	"testing"
	"time"
)

func TestTakeoverSelectedWindowsOnly(t *testing.T) {
	c := DefaultConfig()
	c.NoRestockEnabled = true
	c.NoRestockWindows = []DailyWindow{
		{Start: "02:00", End: "08:00"},
		{Start: "17:00", End: "20:00", TakeoverEnabled: true},
		{Start: "18:00", End: "18:30"},
		{Start: "22:00", End: "23:00", AllowedProvider: "nexovcc"},
		{Start: "23:00", End: "01:00", TakeoverEnabled: true},
	}
	for _, tt := range []struct {
		at   string
		lead int
		want bool
	}{
		{"03:00", 120, false}, {"07:59", 10, false},
		{"16:59", 0, false}, {"17:00", 0, true}, {"17:59", 0, true},
		{"18:00", 10, false}, {"18:29", 10, false}, {"18:30", 0, true},
		{"19:59", 0, true}, {"20:00", 0, false},
		{"22:59", 10, false}, {"23:00", 0, true}, {"00:59", 0, true}, {"01:00", 0, false},
	} {
		now, _ := time.Parse(time.RFC3339, "2026-10-19T"+tt.at+":00+08:00")
		_, _, ok := c.TakeoverWindow(now, tt.lead)
		if ok != tt.want {
			t.Errorf("%s lead %d: got %v", tt.at, tt.lead, ok)
		}
	}
	// Old JSON and legacy single-window configurations never opt in silently.
	for _, raw := range []string{
		`{"no_restock_enabled":true,"no_restock_windows":[{"start":"17:00","end":"20:00"}]}`,
		`{"no_restock_enabled":true,"no_restock_start":"17:00","no_restock_end":"20:00"}`,
	} {
		legacy := DefaultConfig()
		if err := json.Unmarshal([]byte(raw), &legacy); err != nil {
			t.Fatal(err)
		}
		now, _ := time.Parse(time.RFC3339, "2026-10-19T18:00:00+08:00")
		if _, _, ok := legacy.TakeoverWindow(now, 10); ok {
			t.Fatal("legacy window opted in")
		}
	}
	c.NoRestockWindows = []DailyWindow{{Start: "17:00", End: "20:00", AllowedProvider: "nexovcc", TakeoverEnabled: true}}
	if c.validateNoRestockWindows() == nil {
		t.Fatal("ambiguous same-project exception and peer takeover accepted")
	}
}

func TestTakeoverDeadlineStartsRecoveryAndRetainsGroups(t *testing.T) {
	now := time.Now()
	c := DefaultTakeoverConfig()
	c.Enabled, c.Role = true, "provider"
	s := TakeoverState{Epoch: "epoch", Phase: "active", End: now, LeaseUntil: now.Add(time.Hour), ContactAt: now, CoverageReady: true, Pending: 3, PeerPending: 2}
	if s.Block(c, now) == "" {
		t.Fatal("old grant allowed purchases after window end")
	}
	if !s.RecoverAtEnd(now) || s.Phase != "recovering" || !s.RecoveryAt.Equal(now) {
		t.Fatal("did not begin recovery at exact boundary")
	}
	if s.Block(c, now) != "" {
		t.Fatal("provider own-group purchases stayed blocked")
	}
	c.Role = "receiver"
	if s.Block(c, now) != "" {
		t.Fatal("receiver recovery waited for capacity or peer batch")
	}
	s.ContactAt = now.Add(-time.Hour)
	if s.Block(c, now) != "" {
		t.Fatal("offline peer blocked deadline recovery")
	}
	if s.RecoverAtEnd(now.Add(time.Minute)) {
		t.Fatal("replayed deadline transition")
	}
	s.Phase = "released"
	if s.RecoverAtEnd(now.Add(time.Minute)) || s.Phase != "released" {
		t.Fatal("deadline reset completed confirmation")
	}
}

func TestTakeoverWindowsOverlapMidnightAndExceptions(t *testing.T) {
	c := DefaultConfig()
	c.NoRestockEnabled = true
	c.NoRestockWindows = []DailyWindow{{Start: "17:00", End: "20:00", TakeoverEnabled: true}, {Start: "19:00", End: "21:00", TakeoverEnabled: true}, {Start: "23:00", End: "01:00", TakeoverEnabled: true}, {Start: "06:00", End: "08:00", AllowedProvider: "nexovcc"}}
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

func TestTakeoverRecoveryStartsOwnSupplyEvenWhenBorrowedAccountsAreIdle(t *testing.T) {
	c, s, now := fixture()
	c.ConcurrencyEnabled = false
	c.AllowSmall = true
	c.MaxBatch = 1
	c.Reserve = DefaultTakeoverConfig().ReturnMinAccounts
	s.Accounts = []Account{{ID: 2, Ready: true, SupplyExcluded: true, Capacity: 80}}
	d := Evaluate(c, s, nil, Limits{}, 0, 80, 1, now)
	if d.Suggested != 1 || d.ReserveNeed != 1 {
		t.Fatalf("idle borrowed supply prevented the EJH replacement: %+v", d)
	}
	pending := Task{ID: "return", Status: "running", GroupID: s.GroupID, Binding: s.Binding}
	if Evaluate(c, s, []Task{pending}, Limits{}, 0, 80, 1, now).Suggested != 0 {
		t.Fatal("replacement batch was duplicated")
	}
	if Evaluate(c, s, nil, Limits{Failures: c.FailureLimit}, 0, 80, 1, now).Suggested != 0 {
		t.Fatal("recovery bypassed manual-reset failure breaker")
	}
}

func TestTakeoverRecoveryNeedsFreshOwnReadySupply(t *testing.T) {
	c, s, now := fixture()
	c.TimeEnabled = false
	s.CollectionStartedAt = now.Add(-time.Second)
	tc := DefaultTakeoverConfig()
	after := now.Add(-time.Minute)
	s.Accounts[0].Used = 2
	s.Accounts[0].EJH = true
	ready, _, _, _ := TakeoverRecoveryReady(c, tc, s, 0, after, now)
	if !ready {
		t.Fatal("own supply not recognized")
	}
	for _, kind := range []string{"borrowed", "inflight", "stale", "before", "not-ready", "not-ejh", "partial-groups", "fault", "leaving", "exiting", "invalid-load", "expired"} {
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
		case "not-ready":
			v.Accounts[0].Ready = false
		case "not-ejh":
			v.Accounts[0].EJH, v.Accounts[0].CardProvider = false, "nexovcc"
		case "partial-groups":
			v.Accounts[0].TakeoverPending = true
		case "fault":
			v.Accounts[0].Fault = true
		case "leaving":
			v.Accounts[0].Leaving = true
		case "invalid-load":
			v.LoadValid = false
		case "expired":
			expired := now.Add(-time.Second)
			v.Accounts[0].ExpiresAt = &expired
		case "exiting":
			deadline := now.Add(time.Second)
			v.Accounts[0].ForceMergeDeadline = &deadline
		}
		if ready, _, _, _ := TakeoverRecoveryReady(c, tc, v, pending, after, now); ready {
			t.Fatal("unsafe release:", kind)
		}
	}
	// One verified EJH is the default return condition, even while it is busy
	// or its first cost sample is still pending. More replacements remain configurable.
	c.TimeEnabled = true
	s.Accounts[0].Money = nil
	s.Accounts[0].Used = 100
	if ready, _, _, _ := TakeoverRecoveryReady(c, tc, s, 0, after, now); !ready {
		t.Fatal("money or demand prevented configured ready-count handoff")
	}
	tc.ReturnMinAccounts = 2
	if ready, _, _, _ := TakeoverRecoveryReady(c, tc, s, 0, after, now); ready {
		t.Fatal("ignored configured replacement count")
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
