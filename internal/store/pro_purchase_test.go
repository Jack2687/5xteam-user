package store

import (
	"chatgpt-space-merge/internal/gptpay"
	"chatgpt-space-merge/internal/model"
	"chatgpt-space-merge/internal/restock"
	"testing"
	"time"
)

func TestProPurchaseIndependentRulesAndIssuerException(t *testing.T) {
	p := model.DefaultProSettings()
	p.PurchaseScheduleEnabled = true
	p.PurchaseWindows = []model.ProPurchaseWindow{{Start: "17:00", End: "20:50", CardProvider: "ejh", PlanCode: "pro20"}}
	p.EJHPlanTemplates = map[string]string{"pro20": "ejh20"}
	p.NexovccPlanTemplates = map[string]string{"pro20": "global20"}
	pay := gptpay.Settings{Provider: "tokenseek", PlanCode: "pro5", Country: "PH"}
	c := restock.DefaultConfig()
	now := time.Date(2026, 10, 19, 18, 0, 0, 0, time.FixedZone("CST", 8*3600))
	selected, chosen, err := ResolveProPurchase(p, pay, c, now)
	if err != nil || chosen.PlanCode != "pro20" || selected.EJHTemplateID != "ejh20" {
		t.Fatal(selected, chosen, err)
	}
	c.NoRestockEnabled = true
	c.NoRestockWindows = []restock.DailyWindow{{Start: "17:00", End: "20:50", AllowedProvider: "nexovcc"}}
	selected, chosen, err = ResolveProPurchase(p, pay, c, now)
	if err != nil || selected.CardProvider != "nexovcc" || selected.NexovccTemplateID != "global20" || chosen.PlanCode != "pro20" {
		t.Fatal(selected, chosen, err)
	}
	delete(p.NexovccPlanTemplates, "pro20")
	if _, _, err = ResolveProPurchase(p, pay, c, now); err == nil {
		t.Fatal("missing exception template accepted")
	}
	c.NoRestockWindows[0].AllowedProvider = ""
	if c.NoRestockReason(now) == "" {
		t.Fatal("full block lost")
	}
	if pay.PlanCode != "pro5" || p.EJHTemplateID != "" {
		t.Fatal("mutated defaults")
	}
	selected, chosen, err = ResolveProPurchase(p, pay, c, now.Add(4*time.Hour))
	if err != nil || selected.PurchaseRoute != nil || chosen.PlanCode != "pro5" {
		t.Fatal("outside window", err)
	}
}

func TestProPurchaseClaimPinsPlanAndTemplate(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { s.Close() }()
	p := model.DefaultProSettings()
	p.PurchaseScheduleEnabled = true
	local := time.Now().In(time.FixedZone("CST", 8*3600))
	p.PurchaseWindows = []model.ProPurchaseWindow{{Start: local.Add(-time.Hour).Format("15:04"), End: local.Add(time.Hour).Format("15:04"), CardProvider: "nexovcc", PlanCode: "pro20"}}
	p.NexovccPlanTemplates = map[string]string{"pro20": "global20"}
	if _, err = s.SaveProSettings(p, "", ""); err != nil {
		t.Fatal(err)
	}
	pay := gptpay.Settings{PlanCode: "pro5", Provider: "tokenseek", Country: "PH"}
	if _, err = s.SaveGPTPaySettings(pay, "fixture-key"); err != nil {
		t.Fatal(err)
	}
	email := "routed@example.com"
	if _, err = s.SaveMailAccount(model.MailAccountProfile{Email: email, ManagementScope: "pro"}, model.MailAccountCredentials{Email: email}); err != nil {
		t.Fatal(err)
	}
	c := restock.DefaultConfig()
	c.Enabled, c.Mode, c.GroupID = true, "auto", 1
	c, err = s.SaveProRestockConfig(c)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	d := restock.Decision{ID: "route-test", At: now, Version: c.Version, Suggested: 1, Snapshot: restock.Snapshot{NewPlan: "tokenseek:pro5:PH:0"}}
	if _, err = s.ReserveProRestock(d, "", 0); err == nil {
		t.Fatal("stale plan accepted")
	}
	d.Snapshot.NewPlan = "tokenseek:pro20:PH:0"
	tasks, err := s.ReserveProRestock(d, "", 0)
	if err != nil || len(tasks) != 1 {
		t.Fatal(tasks, err)
	}
	task := tasks[0]
	task.Status, task.SubmittedAt = "submitting", &now
	p.NexovccPlanTemplates["pro20"] = "changed"
	s.SaveProSettings(p, "", "")
	if s.ClaimProRestockDispatch(task) == nil {
		t.Fatal("changed template accepted")
	}
	p.NexovccPlanTemplates["pro20"] = "global20"
	s.SaveProSettings(p, "", "")
	if err = s.ClaimProRestockDispatch(task); err != nil {
		t.Fatal(err)
	}
	p.PurchaseScheduleEnabled = false
	s.SaveProSettings(p, "", "")
	s.Close()
	s, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	pinned, err := s.ProRestockDispatchTask(task.ID)
	if err != nil || pinned.PurchaseRoute == nil || pinned.PurchaseRoute.PlanCode != "pro20" || pinned.PurchaseRoute.TemplateID != "global20" {
		t.Fatal(pinned, err)
	}
}
