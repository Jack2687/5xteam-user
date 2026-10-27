package restock

import (
	"strings"
	"testing"
	"time"
)

func TestPaymentRiskReplacementDecisionIsOneAndHonorsGates(t *testing.T) {
	now := time.Now()
	c := DefaultConfig()
	c.Enabled, c.Mode, c.GroupID, c.TimeEnabled, c.ConcurrencyEnabled, c.AllowSmall = true, "auto", 1, false, false, false
	s := Snapshot{At: now, Binding: "pool", GroupID: 1, Valid: true, LoadValid: true}
	tasks := []Task{{ID: "risk", Status: "failed", Binding: s.Binding, GroupID: 1, PaymentRiskPending: true, UpdatedAt: now.Add(-time.Minute)}}
	d := Evaluate(c, s, tasks, Limits{}, 0, 60, 5, now)
	if d.Suggested != 1 || !strings.Contains(d.Reason, "仅安排一个") {
		t.Fatal(d)
	}
	for _, l := range []Limits{{DayCount: c.DayLimit}, {Failures: c.FailureLimit}, {LastBatch: &now}} {
		if d := Evaluate(c, s, tasks, l, 0, 60, 5, now); d.Suggested != 0 {
			t.Fatal("ignored budget/breaker/cooldown", d)
		}
	}
	s.At = now.Add(-time.Hour)
	if d := Evaluate(c, s, tasks, Limits{}, 0, 60, 5, now); d.Suggested != 0 {
		t.Fatal("stale snapshot admitted", d)
	}
	s.At = now
	s.Binding = "other-pool"
	if d := Evaluate(c, s, tasks, Limits{}, 0, 60, 5, now); d.Suggested != 0 {
		t.Fatal("cross-pool replacement", d)
	}
}
