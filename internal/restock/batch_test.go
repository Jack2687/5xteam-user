package restock

import (
	"testing"
	"time"
)

func TestRestockBatchBlocksEveryTriggerAndEmergency(t *testing.T) {
	c, s, now := overlapFixture()
	c.Reserve = 10
	s.Accounts[0].Waiting = c.EmergencyQueue + 1
	for _, status := range []string{"reserved", "submitting", "running", "verifying", "external", "unknown", "attention"} {
		t.Run(status, func(t *testing.T) {
			c, s := c, s
			tasks := []Task{{Status: status, Binding: s.Binding, GroupID: s.GroupID}}
			d := Evaluate(c, s, tasks, Limits{}, c.Confirmations, 50, 2, now)
			if d.Trace.NetNeed == 0 || d.Suggested != 0 || d.BatchWait == nil || d.BatchWait.Remaining != 1 {
				t.Fatalf("ongoing batch admitted more purchases: %+v", d)
			}
			s.Accounts = nil
			c.EmptyGroupCount = 3
			d = Evaluate(c, s, tasks, Limits{}, c.Confirmations, 50, 2, now)
			if d.EmptyGroupNeed != 2 || d.Suggested != 0 || d.BatchWait == nil {
				t.Fatal("empty-group trigger bypassed the batch", d)
			}
		})
	}
}

func TestRestockBatchWaitsForAllMembersThenFreshCollection(t *testing.T) {
	c, s, now := fixture()
	c.TimeEnabled = false
	tasks := []Task{
		{Status: "ready", ReadyAt: &now, Binding: s.Binding, GroupID: s.GroupID},
		{Status: "running", Binding: s.Binding, GroupID: s.GroupID},
		{Status: "reserved", Binding: s.Binding, GroupID: s.GroupID},
	}
	d := Evaluate(c, s, tasks, Limits{}, c.Confirmations, 20, 2, now)
	if d.BatchWait == nil || d.BatchWait.Remaining != 2 || d.BatchWait.Queued != 1 || d.Suggested != 0 {
		t.Fatal("first arrival released the batch", d)
	}
	tasks[1].Status, tasks[1].UpdatedAt = "failed", now
	tasks[2].Status, tasks[2].UpdatedAt = "cancelled", now
	s.At = now.Add(time.Second)
	s.CollectionStartedAt = now.Add(-time.Second)
	d = Evaluate(c, s, tasks, Limits{}, c.Confirmations, 20, 2, s.At)
	if d.BatchWait == nil || !d.BatchWait.Refresh || d.Suggested != 0 {
		t.Fatal("collection already in flight counted as a post-batch refresh", d)
	}
	s.CollectionStartedAt = s.At
	s.LoadValid = false
	if WaitForBatch(s, tasks) == nil {
		t.Fatal("failed collection released batch")
	}
	s.LoadValid = true
	d = Evaluate(c, s, tasks, Limits{}, c.Confirmations, 20, 2, s.At)
	if d.BatchWait != nil || d.Suggested != 1 {
		t.Fatal("new valid monitoring round did not release completed batch", d)
	}
}
