package store

import (
	"chatgpt-space-merge/internal/model"
	"chatgpt-space-merge/internal/restock"
	"testing"
	"time"
)

func TestStopProRestockTaskDurableAndIndependent(t *testing.T) {
	for _, status := range []string{"reserved", "running", "attention", "unknown", "waiting_card", "verifying"} {
		t.Run(status, func(t *testing.T) {
			s := transferStore(t)
			for _, email := range []string{"first@example.com", "second@example.com", "third@example.com"} {
				transferSource(t, s, email)
			}
			c := restock.DefaultConfig()
			c.Enabled, c.Mode, c.GroupID, c.AllowSmall = true, "auto", 1, true
			c, err := s.SaveProRestockConfig(c)
			if err != nil {
				t.Fatal(err)
			}
			tasks, err := s.ReserveProRestock(restock.Decision{ID: "stop-batch", At: time.Now(), Version: c.Version, Suggested: 2}, "target", 0)
			if err != nil || len(tasks) != 2 {
				t.Fatalf("reserve: %v %v", tasks, err)
			}
			original := tasks[0]
			original.Status = status
			if status != "reserved" {
				now := time.Now()
				original.SubmittedAt = &now
				original.OrderID = "original-order"
				original.CardID = "original-card"
			}
			if err = s.SaveProRestockTask(original); err != nil {
				t.Fatal(err)
			}
			before, _ := s.ProRestockLimits(time.Now())
			stopped, changed, err := s.StopProRestockTask(original.ID)
			if err != nil || !changed || stopped.Active() || stopped.StoppedAt == nil || stopped.OrderID != original.OrderID || stopped.CardID != original.CardID {
				t.Fatalf("stop: %+v %v", stopped, err)
			}
			p, _, err := s.MailAccountCredential(original.Email)
			if err != nil || !p.ProAuto.StopRequested || p.ProAuto.Status != "stopped" || p.ProAuto.NextCheckAt != nil || p.ProRestockTaskID != original.ID {
				t.Fatal("durable stop/ownership missing", err)
			}
			// An already-running reconciliation must not revive the stopped intent.
			original.Status = "running"
			if err = s.SaveProRestockTask(original); err != nil {
				t.Fatal(err)
			}
			got, _ := s.ProRestockTasks()
			active := 0
			for _, task := range got {
				if task.Active() {
					active++
				}
				if task.ID == stopped.ID && task.StoppedAt == nil {
					t.Fatal("stale writer revived task")
				}
			}
			if active != 1 {
				t.Fatalf("other task affected: %d", active)
			}
			_, changed, err = s.StopProRestockTask(original.ID)
			if err != nil || changed {
				t.Fatal("stop not idempotent", err)
			}
			after, _ := s.ProRestockLimits(time.Now())
			if status == "reserved" {
				if after.HourCount != before.HourCount-1 {
					t.Fatal("unsubmitted budget not released")
				}
			} else if after.HourCount != before.HourCount || after.HourCost != before.HourCost {
				t.Fatal("submitted budget lost")
			}
			if after.Failures != before.Failures {
				t.Fatal("manual stop changed circuit breaker")
			}
			cfg, _ := s.ProRestockConfig()
			if mustJSON(cfg) != mustJSON(c) {
				t.Fatal("config changed")
			}
			candidates, _ := s.ProScheduleCandidates(10)
			for _, email := range candidates {
				if email == original.Email {
					t.Fatal("stopped account auto-reclaimed")
				}
			}
			if wait := restock.WaitForBatch(restock.Snapshot{Binding: stopped.Binding, GroupID: stopped.GroupID}, []restock.Task{stopped}); wait == nil {
				t.Fatal("must wait for fresh monitor")
			}
		})
	}
}

func TestStoppedRestockCanBeExplicitlyRetried(t *testing.T) {
	s := transferStore(t)
	email := "retry-stop@example.com"
	transferSource(t, s, email)
	c := restock.DefaultConfig()
	c.Enabled, c.Mode, c.GroupID = true, "auto", 1
	c, _ = s.SaveProRestockConfig(c)
	tasks, err := s.ReserveProRestock(restock.Decision{ID: "retry-stop", At: time.Now(), Version: c.Version, Suggested: 1}, "target", 0)
	if err != nil || len(tasks) != 1 {
		t.Fatal(err)
	}
	task := tasks[0]
	task.CardProvider = "nexovcc"
	task.Status = "attention"
	s.SaveProRestockTask(task)
	if _, _, err = s.StopProRestockTask(task.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.DetachFailedProRestock(email, "", ""); err != nil {
		t.Fatal(err)
	}
	p, _, _ := s.MailAccountCredential(email)
	if p.ProRestockTaskID != "" {
		t.Fatal("manual retry still owned")
	}
	s.UpdateProAccount(email, func(p *model.MailAccountProfile) {
		p.ProAuto.ID = "new-manual-flow"
		p.ProAuto.StopRequested = false
		p.ProAuto.Status = "running"
	})
	if _, changed, err := s.StopProRestockTask(task.ID); err != nil || changed {
		t.Fatal("old stop should be idempotent", err)
	}
	p, _, _ = s.MailAccountCredential(email)
	if p.ProAuto.StopRequested || p.ProAuto.ID != "new-manual-flow" {
		t.Fatal("old stop changed new flow")
	}
}
