package store

import (
	"chatgpt-space-merge/internal/ejh"
	"testing"
)

func TestEJHReversalFeeSettingsPersistence(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	cfg, _, err := s.EJHSettings()
	if err != nil || cfg.ReversalFeePercent != 6 {
		t.Fatalf("default: %+v %v", cfg, err)
	}
	cfg.ReversalFeePercent = 6.25
	if _, err = s.SaveEJHSettings(cfg, ""); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	cfg, _, err = s.EJHSettings()
	if err != nil || cfg.ReversalFeePercent != 6.25 {
		t.Fatalf("restart: %+v %v", cfg, err)
	}
	cfg.ReversalFeePercent = 0
	if _, err = s.SaveEJHSettings(cfg, ""); err != nil {
		t.Fatal(err)
	}
	got, _, err := s.EJHSettings()
	if err != nil || got.ReversalFeePercent != 0 {
		t.Fatalf("zero: %+v %v", got, err)
	}
	// Older saved profiles have no percentage field and receive the default.
	if _, err = s.db.Exec(`UPDATE ejh_settings SET profile=json_remove(profile,'$.reversal_fee_percent')`); err != nil {
		t.Fatal(err)
	}
	got, _, err = s.EJHSettings()
	if err != nil || got.ReversalFeePercent != ejh.DefaultSettings().ReversalFeePercent {
		t.Fatalf("legacy: %+v %v", got, err)
	}
}
