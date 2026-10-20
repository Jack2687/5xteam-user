package store

import (
	"chatgpt-space-merge/internal/model"
	"chatgpt-space-merge/internal/restock"
	"testing"
	"time"
)

func TestDemandGatePersistenceAndInvalidation(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { s.Close() }()
	now := time.Now()
	local := now.In(time.FixedZone("Asia/Shanghai", 8*3600))
	c := restock.DefaultConfig()
	c.Enabled, c.Mode, c.GroupID = true, "auto", 1
	c.NoRestockEnabled = true
	c.NoRestockWindows = []restock.DailyWindow{{Start: local.Add(-time.Hour).Format("15:04"), End: local.Add(time.Hour).Format("15:04"), HighDemandEnabled: true, HighDemandThreshold: 20, HighDemandConfirmations: 1}}
	c, err = s.SaveProRestockConfig(c)
	if err != nil {
		t.Fatal(err)
	}
	scope, err := s.ProRestockDemandScope()
	if err != nil {
		t.Fatal(err)
	}
	g := c.AdvanceDemand(restock.DemandGate{Scope: scope, Valid: true, MainAt: now, TotalUsed: 20, ValidUntil: now.Add(time.Minute)}, now)
	if err = s.SaveProRestockDemand(g); err != nil {
		t.Fatal(err)
	}
	c, err = s.ProRestockConfig()
	if err != nil || c.NoRestockReason(now) != "" {
		t.Fatal("stored permission not applied", c, err)
	}
	email := "demand-admission@example.test"
	if _, err = s.SaveMailAccount(model.MailAccountProfile{Email: email, ManagementScope: "pro"}, model.MailAccountCredentials{Email: email}); err != nil {
		t.Fatal(err)
	}
	tasks, err := s.ReserveProRestock(restock.Decision{ID: "high-demand", At: now, Version: c.Version, Suggested: 1}, "", 0)
	if err != nil || len(tasks) != 1 {
		t.Fatal("valid high demand did not pass reservation", err)
	}
	low := c.AdvanceDemand(restock.DemandGate{Scope: scope, Valid: true, MainAt: now.Add(time.Second), ValidUntil: now.Add(time.Minute), TotalUsed: 1}, now.Add(time.Second))
	if err = s.SaveProRestockDemand(low); err != nil {
		t.Fatal(err)
	}
	claim := tasks[0]
	claim.Status = "submitting"
	claim.SubmittedAt = &now
	if err = s.ClaimProRestockDispatch(claim); err == nil {
		t.Fatal("dispatch bypassed lowered demand")
	}
	if err = s.SaveProRestockDemand(g); err != nil {
		t.Fatal(err)
	}
	if err = s.ClaimProRestockDispatch(claim); err != nil {
		t.Fatal("confirmed high demand did not pass dispatch", err)
	}
	f, target, err := s.ProFallbackConfig()
	if err != nil {
		t.Fatal(err)
	}
	f.CheckSeconds++
	if _, err = s.SaveProFallbackConfig(f, target); err != nil {
		t.Fatal(err)
	}
	c, err = s.ProRestockConfig()
	if err != nil || c.NoRestockReason(now) == "" {
		t.Fatal("changed fallback reused permission", err)
	}
	if err = s.SaveProRestockDemand(g); err == nil {
		t.Fatal("stale sampler accepted")
	}
	scope, _ = s.ProRestockDemandScope()
	g.Scope = scope
	if err = s.SaveProRestockDemand(g); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	c, err = s.ProRestockConfig()
	if err != nil || c.NoRestockReason(now) == "" {
		t.Fatal("restart reused confirmation", err)
	}
}
