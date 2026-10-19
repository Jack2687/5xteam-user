package store

import (
	"testing"

	"chatgpt-space-merge/internal/gptpay"
	"chatgpt-space-merge/internal/model"
)

func TestProMergeConcurrencyLegacyAndRestart(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	pro := model.DefaultProSettings()
	pro.Concurrency = 6
	if _, err := s.SaveProSettings(pro, "", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec("UPDATE pro_settings SET profile=json_remove(profile,'$.merge_concurrency')"); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	got, _, _, err := s.ProSettings()
	if err != nil || got.Concurrency != 6 || got.MergeConcurrency != 2 {
		t.Fatalf("legacy defaults: %+v, %v", got, err)
	}
	got.MergeConcurrency = 1
	if _, _, err := s.SaveProAutomationSettings(got, gptpay.Settings{PlanCode: "pro5"}, ""); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	got, _, _, err = s.ProSettings()
	if err != nil || got.Concurrency != 6 || got.MergeConcurrency != 1 {
		t.Fatalf("restart lost independent settings: %+v, %v", got, err)
	}
}
