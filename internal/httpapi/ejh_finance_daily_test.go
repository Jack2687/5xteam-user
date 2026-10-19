package httpapi

import (
	"chatgpt-space-merge/internal/ejh"
	"chatgpt-space-merge/internal/store"
	"testing"
	"time"
)

func TestEJHFinancialConsumptionAgreesWithAccountAndCardTotals(t *testing.T) {
	old, fresh := 100.0, 200.0
	cases := []ejh.Case{{Email: "same@example.com", ConsumptionTotal: &old}, {Email: "deleted@example.com"}, {Email: "fresh@example.com", ConsumptionTotal: &fresh}, {Email: "unknown@example.com"}}
	history := []store.EJHConsumption{{ID: "first", Email: "SAME@example.com", Total: 110}, {ID: "first", Email: "same@example.com", Total: 110}, {ID: "second", Email: "same@example.com", Total: 2}, {ID: "deleted", Email: "deleted@example.com", Total: 30}, {ID: "stale", Email: "fresh@example.com", Total: 150}}
	applyEJHFinancialConsumption(cases, history)
	for i, want := range []float64{112, 30, 200} {
		if cases[i].ConsumptionTotal == nil || *cases[i].ConsumptionTotal != want {
			t.Fatalf("account %d: got %v, want %v", i, cases[i].ConsumptionTotal, want)
		}
	}
	if cases[3].ConsumptionTotal != nil || old != 100 || fresh != 200 {
		t.Fatal("unknown or operational counters were changed")
	}
}

func TestDailyConsumptionRefreshesFinalRequestsAndBacksOff(t *testing.T) {
	zone := time.FixedZone("Asia/Shanghai", 8*3600)
	at := time.Date(2026, 10, 16, 23, 0, 0, 0, zone)
	r := store.EJHConsumption{DownstreamID: 42, StartDay: "2026-10-16", EndDay: "2026-10-16", SourceAt: &at}
	if !ejhDailyDue(r, at) {
		t.Fatal("new account not backfilled")
	}
	r.DailyCheckedAt = &at
	r.DailySourceAt = &at
	r.DailyAttemptAt = &at
	if ejhDailyDue(r, at.Add(30*time.Second)) {
		t.Fatal("immediate repeat")
	}
	if !ejhDailyDue(r, at.Add(2*time.Hour)) {
		t.Fatal("final day's late requests not refreshed")
	}
	final := at.Add(2 * time.Hour)
	r.DailyCheckedAt = &final
	r.DailyAttemptAt = &final
	if ejhDailyDue(r, final.Add(24*time.Hour)) {
		t.Fatal("finalized historic account queried forever")
	}
	r.DailyError = "offline"
	if ejhDailyDue(r, final.Add(2*time.Minute)) {
		t.Fatal("failed query not backed off")
	}
}
