package store

import (
	"chatgpt-space-merge/internal/model"
	"chatgpt-space-merge/internal/restock"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestProRestockSchedulePersistsAndGuardsAdmission(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { s.Close() }()
	// Existing JSON records predate the schedule fields.
	if _, err = s.db.Exec(`INSERT INTO pro_restock_config VALUES(1,'{"version":1,"mode":"observe"}')`); err != nil {
		t.Fatal(err)
	}
	c, err := s.ProRestockConfig()
	if err != nil || c.NoRestockEnabled || c.NoRestockStart != "00:00" || c.NoRestockEnd != "07:00" {
		t.Fatal(c, err)
	}
	c.Enabled, c.Mode, c.GroupID, c.UnitCost = true, "auto", 1, 3
	c, err = s.SaveProRestockConfig(c)
	if err != nil {
		t.Fatal(err)
	}
	email := "schedule@example.com"
	if _, err = s.SaveMailAccount(model.MailAccountProfile{Email: email, ManagementScope: "pro"}, model.MailAccountCredentials{Email: email}); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	d := restock.Decision{ID: "before-window", At: now, Version: c.Version, Suggested: 1}
	tasks, err := s.ReserveProRestock(d, "target", 0)
	if err != nil || len(tasks) != 1 {
		t.Fatal(tasks, err)
	}
	local := now.In(time.FixedZone("Asia/Shanghai", 8*60*60))
	c.NoRestockEnabled = true
	c.NoRestockStart, c.NoRestockEnd = local.Add(-time.Hour).Format("15:04"), local.Add(time.Hour).Format("15:04")
	c.NoRestockWindows = []restock.DailyWindow{
		{Start: local.Add(3 * time.Hour).Format("15:04"), End: local.Add(4 * time.Hour).Format("15:04")},
		{Start: c.NoRestockStart, End: c.NoRestockEnd},
	}
	c, err = s.SaveProRestockConfig(c)
	if err != nil {
		t.Fatal(err)
	}
	// Even a forged/stale positive decision outside the window cannot reserve now.
	d.ID, d.Version, d.At = "stale-positive", c.Version, now.Add(-2*time.Hour)
	if _, err = s.ReserveProRestock(d, "target", 0); err == nil || !strings.Contains(err.Error(), "每日禁补") {
		t.Fatal("reservation bypassed current time", err)
	}
	claim := tasks[0]
	claim.Status, claim.Version, claim.SubmittedAt = "submitting", c.Version, &now
	if err = s.ClaimProRestockDispatch(claim); err == nil || !strings.Contains(err.Error(), "每日禁补") {
		t.Fatal("dispatch bypassed schedule", err)
	}
	tasks, err = s.ProRestockTasks()
	if err != nil || len(tasks) != 1 || tasks[0].Status != "reserved" || tasks[0].SubmittedAt != nil {
		t.Fatal(tasks, err)
	}
	limits, err := s.ProRestockLimits(now)
	if err != nil || limits.DayCost != 3 || limits.DayCount != 1 {
		t.Fatal(limits, err)
	}
	if err = s.CancelProRestock(tasks[0], "每日禁补"); err != nil {
		t.Fatal(err)
	}
	limits, err = s.ProRestockLimits(now)
	if err != nil || limits.DayCost != 0 || limits.DayCount != 0 {
		t.Fatal("cancel did not release budget", limits, err)
	}
	p, _, err := s.MailAccountCredential(email)
	if err != nil || p.ProRestockTaskID != "" {
		t.Fatal("cancel did not release account", p, err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.ProRestockConfig()
	if err != nil || !reflect.DeepEqual(got, c) || got.NoRestockReason(time.Now()) == "" {
		t.Fatal("schedule lost on restart", got, err)
	}
}
