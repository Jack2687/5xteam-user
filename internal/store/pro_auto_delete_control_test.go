package store

import (
	"testing"
	"time"

	"chatgpt-space-merge/internal/gptpay"
	"chatgpt-space-merge/internal/model"
)

func TestProAutoDeletePausePersistsAndIsIndependent(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { s.Close() }()
	cfg := model.DefaultProSettings()
	cfg.AutoDeleteEnabled, cfg.QuotaEnabled = true, true
	next := time.Now().Add(time.Minute).UTC()
	cfg.NextQuotaSweepAt = &next
	if _, err = s.SaveProSettings(cfg, "sub-secret", "cpa-secret"); err != nil {
		t.Fatal(err)
	}
	if err = s.SetProAutoDeletePaused(true); err != nil {
		t.Fatal(err)
	}
	got, secret, _, err := s.ProSettings()
	if err != nil || !got.AutoDeletePaused || !got.QuotaEnabled || !got.AutoDeleteEnabled || !got.NextQuotaSweepAt.Equal(next) || secret != "sub-secret" {
		t.Fatal("switch changed unrelated state", err)
	}
	if _, err = s.SaveProSettings(cfg, "", ""); err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.SaveProAutomationSettings(cfg, gptpay.Settings{PlanCode: "pro20"}, ""); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	got, _, _, err = s.ProSettings()
	if err != nil || !got.AutoDeletePaused {
		t.Fatal("stale save or restart removed pause", err)
	}
	if err = s.SetProAutoDeletePaused(false); err != nil {
		t.Fatal(err)
	}
	got, _, _, err = s.ProSettings()
	if err != nil || got.AutoDeletePaused || !got.AutoDeleteEnabled || !got.QuotaEnabled {
		t.Fatal("resume changed rules", err)
	}
}
