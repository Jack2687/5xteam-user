package store

import (
	"testing"
	"time"

	"chatgpt-space-merge/internal/model"
	"chatgpt-space-merge/internal/restock"
)

func TestProRestockProviderClaimRevalidatesAndPins(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { s.Close() }()
	p := model.DefaultProSettings()
	p.CardProvider = "ejh"
	if _, err = s.SaveProSettings(p, "", ""); err != nil {
		t.Fatal(err)
	}
	email := "issuer@example.com"
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
	tasks, err := s.ReserveProRestock(restock.Decision{ID: "issuer-claim", At: now, Version: c.Version, Suggested: 1}, "", 0)
	if err != nil || len(tasks) != 1 {
		t.Fatal(tasks, err)
	}
	claim := tasks[0]
	if claim.CardProvider != "ejh" || claim.StrictCardProvider {
		t.Fatal(claim)
	}
	if _, err = s.ProRestockDispatchTask(claim.ID); err == nil {
		t.Fatal("unclaimed intent got purchase permission")
	}
	// Simulate a time-boundary with no version change, as wall time alone does.
	local := now.In(time.FixedZone("CST", 8*3600))
	c.NoRestockEnabled = true
	c.NoRestockWindows = []restock.DailyWindow{{Start: local.Add(-time.Hour).Format("15:04"), End: local.Add(time.Hour).Format("15:04"), AllowedProvider: "nexovcc"}}
	if _, err = s.db.Exec(`UPDATE pro_restock_config SET payload=? WHERE id=1`, mustJSON(c)); err != nil {
		t.Fatal(err)
	}
	claim.Status, claim.SubmittedAt = "submitting", &now
	if err = s.ClaimProRestockDispatch(claim); err == nil {
		t.Fatal("old issuer crossed boundary")
	}
	claim.CardProvider, claim.StrictCardProvider = "nexovcc", true
	if err = s.ClaimProRestockDispatch(claim); err != nil {
		t.Fatal(err)
	}
	if err = s.ClaimProRestockDispatch(claim); err == nil {
		t.Fatal("duplicate dispatch accepted")
	}
	// Started work keeps its provider even if the next interval blocks all work.
	c.NoRestockWindows[0].AllowedProvider = ""
	if _, err = s.SaveProRestockConfig(c); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	task, err := s.ProRestockDispatchTask(claim.ID)
	if err != nil || task.CardProvider != "nexovcc" || !task.StrictCardProvider {
		t.Fatal("pin lost on restart", task, err)
	}
	p, _, _, err = s.ProSettings()
	if err != nil || p.CardProvider != "ejh" {
		t.Fatal("global provider was changed", p.CardProvider, err)
	}
}
