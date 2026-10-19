package store

import (
	"chatgpt-space-merge/internal/model"
	"chatgpt-space-merge/internal/restock"
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestProRestockTransferDropsForeignOwnership(t *testing.T) {
	src, dst := transferStore(t), transferStore(t)
	email := "restock-transfer@example.com"
	transferSource(t, src, email)
	target := model.DownstreamTarget{Provider: "sub2", Scope: "pro-restock", Secret: "test-secret", Sub2: model.Sub2Settings{URL: "https://original.example.invalid", Email: "admin@example.com"}}
	id, err := src.SaveDownstreamTarget(target)
	if err != nil {
		t.Fatal(err)
	}
	src.UpdateProAccount(email, func(p *model.MailAccountProfile) { p.ProRestockTaskID = "source-intent"; p.ProRestockTargetID = id })
	file, err := src.ExportProAccounts([]string{email}, model.ProDownstreamRef{Provider: "sub2", URL: "https://new.example.invalid"})
	if err != nil {
		t.Fatal(err)
	}
	if file.Accounts[0].Downstream.URL != target.Sub2.URL {
		t.Fatal("original target lost")
	}
	_, err = dst.ImportProAccount(file.Accounts[0], file.ExportedAt, false, file.Accounts[0].Downstream)
	if err != nil {
		t.Fatal(err)
	}
	profile, _, err := dst.MailAccountCredential(email)
	if err != nil || profile.ProRestockTaskID != "" || profile.ProRestockTargetID != "" {
		t.Fatal("foreign ownership imported", err)
	}
}

func TestProRestockCredentialImportPreservesOwnership(t *testing.T) {
	s := transferStore(t)
	email := "restock-credentials@example.com"
	transferSource(t, s, email)
	started := time.Now().Add(-30 * time.Minute)
	next := time.Now().Add(time.Minute)
	p, err := s.UpdateProAccount(email, func(p *model.MailAccountProfile) {
		p.ProRestockTaskID, p.ProRestockTargetID = "local-task", "local-target"
		p.ProPushStartedAt, p.ProForceMergeNextAt, p.ProForceMergeAttempts = &started, &next, 2
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"", p.ID} {
		got, err := s.SaveMailAccountToPro(model.MailAccountProfile{ID: id, Email: email}, model.MailAccountCredentials{Email: email, MailPassword: "updated-password"})
		if err != nil {
			t.Fatal(err)
		}
		if got.ProRestockTaskID != "local-task" || got.ProRestockTargetID != "local-target" || got.ID != p.ID {
			t.Fatal("credential update lost local ownership")
		}
		if got.ProPushStartedAt == nil || !got.ProPushStartedAt.Equal(started) || got.ProForceMergeNextAt == nil || !got.ProForceMergeNextAt.Equal(next) || got.ProForceMergeAttempts != 2 {
			t.Fatal("credential update reset original push deadline")
		}
	}
}

func TestProRestockTransferCannotOverwriteActiveIntent(t *testing.T) {
	src, dst := transferStore(t), transferStore(t)
	email := "restock-import-guard@example.com"
	transferSource(t, src, email)
	transferSource(t, dst, email)
	file, err := src.ExportProAccounts([]string{email}, model.ProDownstreamRef{})
	if err != nil {
		t.Fatal(err)
	}
	c := restock.DefaultConfig()
	c.Enabled, c.Mode, c.GroupID = true, "auto", 1
	c, err = dst.SaveProRestockConfig(c)
	if err != nil {
		t.Fatal(err)
	}
	tasks, err := dst.ReserveProRestock(restock.Decision{ID: "import-guard", At: time.Now(), Version: c.Version, Suggested: 1}, "local-target", 0)
	if err != nil || len(tasks) != 1 {
		t.Fatalf("reserve: %v, %v", tasks, err)
	}
	task := tasks[0]
	for _, status := range []string{"reserved", "submitting", "running", "verifying", "unknown", "attention"} {
		task.Status = status
		if err = dst.SaveProRestockTask(task); err != nil {
			t.Fatal(err)
		}
		if _, err = dst.ImportProAccount(file.Accounts[0], file.ExportedAt, true, model.ProDownstreamRef{}); err == nil {
			t.Fatalf("overwrote %s intent", status)
		}
	}
	task.Status = "ready"
	if err = dst.SaveProRestockTask(task); err != nil {
		t.Fatal(err)
	}
	if _, err = dst.ImportProAccount(file.Accounts[0], file.ExportedAt, true, model.ProDownstreamRef{}); err != nil {
		t.Fatal(err)
	}
	p, _, err := dst.MailAccountCredential(email)
	if err != nil || p.ProRestockTaskID != task.ID || p.ProRestockTargetID != "local-target" {
		t.Fatal("terminal intent lost local ownership", err)
	}
}

func TestProRestockAtomicClaimBudgetAndRestart(t *testing.T) {
	dir := t.TempDir()
	s, e := Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	c := restock.DefaultConfig()
	c.Enabled = true
	c.Mode = "auto"
	c.GroupID = 1
	c.UnitCost = 25
	c.DayBudget = 100
	c.MaxInflight = 4
	c.MaxBatch = 4
	c, e = s.SaveProRestockConfig(c)
	if e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 12; i++ {
		email := fmt.Sprintf("fresh-%02d@example.com", i)
		s.SaveMailAccount(model.MailAccountProfile{Email: email, ManagementScope: "pro"}, model.MailAccountCredentials{Email: email})
	}
	n := time.Now()
	d := restock.Decision{At: n, Version: c.Version, Suggested: 4, Snapshot: restock.Snapshot{ID: "s", At: n, Binding: "pool", GroupID: 1}}
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			copy := d
			copy.ID = fmt.Sprintf("d%d", i)
			s.ReserveProRestock(copy, "target", 0)
		}(i)
	}
	wg.Wait()
	tasks, e := s.ProRestockTasks()
	if e != nil || len(tasks) != 4 {
		t.Fatalf("claims=%d err=%v", len(tasks), e)
	}
	l, _ := s.ProRestockLimits(n)
	if l.DayCost != 100 || l.DayCount != 4 {
		t.Fatal(l)
	}
	candidates, _ := s.ProScheduleCandidates(20)
	if len(candidates) != 8 {
		t.Fatal("legacy can reclaim", candidates)
	}
	if e = s.ReserveProCard(tasks[0].Email, "missing", false); e == nil {
		t.Fatal("manual card admission must reject claimed account")
	}
	if e = s.CancelProRestock(tasks[0], "demand dropped"); e != nil {
		t.Fatal(e)
	}
	tasks[1].Status = "unknown"
	tasks[1].OrderID = "original"
	s.SaveProRestockTask(tasks[1])
	if s.CancelProRestock(tasks[1], "drop") == nil {
		t.Fatal("cancelled committed task")
	}
	l, _ = s.ProRestockLimits(n)
	if l.DayCost != 75 {
		t.Fatal(l)
	}
	s.Close()
	s, e = Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	tasks, _ = s.ProRestockTasks()
	if len(tasks) != 4 {
		t.Fatal("lost history")
	}
	l, _ = s.ProRestockLimits(n.Add(48 * time.Hour))
	if l.DayCost != 75 {
		t.Fatal("unknown/reserved budget must survive day rollover", l)
	}
	old := c
	c, e = s.SaveProRestockConfig(c)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.SaveProRestockConfig(old); e == nil {
		t.Fatal("stale config accepted")
	}
}
