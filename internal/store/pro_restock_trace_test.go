package store

import (
	"chatgpt-space-merge/internal/restock"
	"testing"
	"time"
)

func TestProRestockReservationPersistsDecisionEvidence(t *testing.T) {
	s := transferStore(t)
	transferSource(t, s, "trace-one@example.com")
	transferSource(t, s, "trace-two@example.com")
	c := restock.DefaultConfig()
	c.Enabled, c.Mode, c.GroupID, c.EmptyGroupCount = true, "auto", 1, 2
	c, err := s.SaveProRestockConfig(c)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	snap := restock.Snapshot{ID: "saved-snapshot", At: now, Valid: true, LoadValid: true, GroupID: 1, Binding: "pool"}
	d := restock.Evaluate(c, snap, nil, restock.Limits{}, 0, 10, 2, now)
	d.ID = "saved-decision"
	tasks, err := s.ReserveProRestock(d, "target", 0)
	if err != nil || len(tasks) != 2 {
		t.Fatalf("reserve: %d, %v", len(tasks), err)
	}
	// Later config/task/caller changes must not rewrite why the batch started.
	d.Trace.NetNeed = 99
	c.EmptyGroupCount = 20
	if _, err := s.SaveProRestockConfig(c); err != nil {
		t.Fatal(err)
	}
	tasks[0].Status = "ready"
	if err := s.SaveProRestockTask(tasks[0]); err != nil {
		t.Fatal(err)
	}
	records, err := s.ProRestockDecisions(10, 0)
	if err != nil || len(records) != 1 {
		t.Fatalf("records: %+v %v", records, err)
	}
	got := records[0]
	if got.Trace == nil || got.Trace.NetNeed != 2 || got.Trace.NewAccountCapacity != 10 || got.Config.EmptyGroupCount != 2 || got.Started != 2 || got.Snapshot.ID != snap.ID {
		t.Fatalf("historical evidence changed: %+v", got)
	}
	linked, err := s.ProRestockDecisionTasks(got.ID)
	if err != nil || len(linked) != 2 || linked[0].DecisionID != got.ID {
		t.Fatalf("missing execution links: %+v %v", linked, err)
	}
	if err := s.SaveProRestockDecision(restock.Decision{ID: "legacy", At: now.Add(-time.Minute), Reason: "旧记录"}); err != nil {
		t.Fatal(err)
	}
	records, err = s.ProRestockDecisions(10, 0)
	if err != nil || len(records) != 2 || records[1].Trace != nil {
		t.Fatalf("legacy record must not fabricate trace: %+v %v", records, err)
	}
}
