package store

import (
	"chatgpt-space-merge/internal/model"
	"chatgpt-space-merge/internal/restock"
	"testing"
	"time"
)

func TestProRestockEmptyStartupBelowMinimumPersists(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { s.Close() }()
	c := restock.DefaultConfig()
	c.Enabled = true
	c.Mode = "auto"
	c.GroupID = 1
	c.AllowSmall = false
	c.EmptyGroupCount = 1
	c, err = s.SaveProRestockConfig(c)
	if err != nil {
		t.Fatal(err)
	}
	email := "empty-start@example.com"
	if _, err = s.SaveMailAccount(model.MailAccountProfile{Email: email, ManagementScope: "pro"}, model.MailAccountCredentials{Email: email}); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	snap := restock.Snapshot{At: now, Valid: true, LoadValid: true, GroupID: 1, Binding: "pool"}
	d := restock.Evaluate(c, snap, nil, restock.Limits{}, 0, 20, 5, now)
	d.ID = "empty-start"
	tasks, err := s.ReserveProRestock(d, "target", 0)
	if err != nil || len(tasks) != 1 || !tasks[0].EmptyGroupStartup {
		t.Fatalf("reserve: %+v %v", tasks, err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.ProRestockConfig()
	if err != nil || got.EmptyGroupCount != 1 {
		t.Fatal(got, err)
	}
	tasks, err = s.ProRestockTasks()
	if err != nil || len(tasks) != 1 || !tasks[0].EmptyGroupStartup {
		t.Fatal(tasks, err)
	}
	// Beyond cooldown, existing reservation still suppresses a new startup.
	snap.At = now.Add(2 * time.Minute)
	d = restock.Evaluate(got, snap, tasks, restock.Limits{}, 0, 20, 5, snap.At)
	if d.Suggested != 0 {
		t.Fatal(d)
	}
}
