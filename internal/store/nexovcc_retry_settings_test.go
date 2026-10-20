package store

import (
	"testing"

	"chatgpt-space-merge/internal/nexovcc"
)

func TestNexovccRetrySettingsLegacyAndExplicitOff(t *testing.T) {
	s := transferStore(t)
	if _, err := s.SaveNexovccSettings(nexovcc.Defaults(), nexovcc.Credentials{}, false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE nexovcc_settings SET profile=json_remove(profile,'$.page_auto_refresh','$.http_522_retry_seconds','$.http_522_retries')`); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := s.NexovccSettings()
	if err != nil || !cfg.PageAutoRefresh || cfg.HTTP522RetrySeconds != 5 || cfg.HTTP522Retries != 2 {
		t.Fatal(cfg, err)
	}
	cfg.PageAutoRefresh, cfg.HTTP522Retries, cfg.HTTP522RetrySeconds = false, 0, 11
	if _, err = s.SaveNexovccSettings(cfg, nexovcc.Credentials{}, false); err != nil {
		t.Fatal(err)
	}
	cfg, _, err = s.NexovccSettings()
	if err != nil || cfg.PageAutoRefresh || cfg.HTTP522Retries != 0 || cfg.HTTP522RetrySeconds != 11 {
		t.Fatal(cfg, err)
	}
	for _, count := range []int{-1, 11} {
		cfg.HTTP522Retries = count
		if _, err = s.SaveNexovccSettings(cfg, nexovcc.Credentials{}, false); err == nil {
			t.Fatal("invalid retry count saved")
		}
	}
}
