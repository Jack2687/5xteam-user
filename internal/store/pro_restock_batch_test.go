package store

import (
	"fmt"
	"testing"
	"time"

	"chatgpt-space-merge/internal/model"
	"chatgpt-space-merge/internal/restock"
)

func TestProRestockBatchReservationGuardSurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { s.Close() }()
	c := restock.DefaultConfig()
	c.Enabled, c.Mode, c.GroupID = true, "auto", 1
	c, err = s.SaveProRestockConfig(c)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 4; i++ {
		email := fmt.Sprintf("batch-guard-%d@example.com", i)
		if _, err = s.SaveMailAccount(model.MailAccountProfile{Email: email, ManagementScope: "pro"}, model.MailAccountCredentials{Email: email}); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now()
	d := restock.Decision{ID: "old-batch", Version: c.Version, Suggested: 2, At: now.Add(-72 * time.Hour), Snapshot: restock.Snapshot{Binding: "pool", GroupID: 1}}
	tasks, err := s.ReserveProRestock(d, "", 0)
	if err != nil || len(tasks) != 2 {
		t.Fatal(tasks, err)
	}
	d.ID, d.At, d.Suggested = "next-batch", now, 1
	d.Snapshot.At, d.Snapshot.Valid, d.Snapshot.LoadValid = now, true, true
	if added, err := s.ReserveProRestock(d, "", 0); err == nil || len(added) != 0 {
		t.Fatal("stale inflight count bypassed transaction guard")
	}
	for i := range tasks {
		tasks[i].Status, tasks[i].UpdatedAt = "failed", now
		if i == 0 {
			tasks[i].Status, tasks[i].ReadyAt = "ready", &now
		}
		if err = s.SaveProRestockTask(tasks[i]); err != nil {
			t.Fatal(err)
		}
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := s.ProRestockTasks()
	if err != nil || len(restored) != 2 {
		t.Fatal("recently completed old batch disappeared on restart", restored, err)
	}
	if added, err := s.ReserveProRestock(d, "", 0); err == nil || len(added) != 0 {
		t.Fatal("restart accepted the snapshot preceding completion")
	}
	d.Snapshot.At = now.Add(time.Second)
	d.Snapshot.CollectionStartedAt = now.Add(-time.Second)
	if _, err := s.ReserveProRestock(d, "", 0); err == nil {
		t.Fatal("collection started before batch completion accepted")
	}
	d.Snapshot.CollectionStartedAt = d.Snapshot.At
	if added, err := s.ReserveProRestock(d, "", 0); err != nil || len(added) != 1 {
		t.Fatal("completed/failed batch did not release after fresh collection", added, err)
	}
}
