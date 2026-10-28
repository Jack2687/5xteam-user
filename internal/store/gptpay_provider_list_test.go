package store

import (
	"encoding/json"
	"strings"
	"testing"

	"chatgpt-space-merge/internal/gptpay"
	"chatgpt-space-merge/internal/model"
)

func providerList(t *testing.T, s *Store) []gptpay.SettingsInput {
	t.Helper()
	var rows []gptpay.SettingsInput
	for _, id := range []string{gptpay.Tokenseek, gptpay.CMSNav, gptpay.XUPay} {
		cfg, _, err := s.GPTPayProviderSettings(id)
		if err != nil {
			t.Fatal(err)
		}
		rows = append(rows, gptpay.SettingsInput{Settings: cfg})
	}
	return rows
}

func TestGPTPayProviderListPreservesLegacyAndSwitchesAtomically(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { s.Close() }()
	sealed, _ := s.encrypt("legacy-secret")
	_, err = s.db.Exec(`INSERT INTO gptpay_settings VALUES(1,?,?)`, `{"url":"https://original.example/api/v1","plan_code":"pro20","country":"JP","card_account_limit":9,"session_timeout_minutes":45}`, sealed)
	if err != nil {
		t.Fatal(err)
	}
	pay, _, _ := s.GPTPaySettings()
	pay.BackupProvider = gptpay.XUPay
	rows := providerList(t, s)
	if rows[0].PlanCode != "pro20" || rows[0].Country != "JP" || !rows[0].KeyPresent || rows[1].KeyPresent || rows[2].KeyPresent {
		t.Fatal("existing selections or isolated key status lost")
	}
	rows[1].Country, rows[1].PlanCode, rows[1].APIKey = "US", "pro50", "cms-secret"
	rows[2].Country, rows[2].PlanCode, rows[2].APIKey, rows[2].TermsAccepted = "PH", "pro20", "xupay-secret", true
	pro := model.DefaultProSettings()
	if _, _, err = s.SaveProAutomationProviderSettings(pro, pay, "", rows, ""); err != nil {
		t.Fatal(err)
	}
	active, key, _ := s.GPTPaySettings()
	if active.Provider != gptpay.Tokenseek || active.BackupProvider != gptpay.XUPay || active.PlanCode != "pro20" || key != "legacy-secret" {
		t.Fatal("editing inactive supplier switched active supplier or lost legacy key")
	}
	rows = providerList(t, s)
	// Edit the previously active supplier while activating another. The archive
	// step must not overwrite the edited inactive profile.
	rows[0].Country, rows[0].PlanCode = "EG", "pro5"
	rows[1].APIKey = "cms-replacement"
	pay.Provider = gptpay.XUPay
	pay.BackupProvider = gptpay.CMSNav
	if _, _, err = s.SaveProAutomationProviderSettings(pro, pay, "", rows, ""); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	active, key, _ = s.GPTPaySettings()
	if active.Provider != gptpay.XUPay || active.BackupProvider != gptpay.CMSNav || active.PlanCode != "pro20" || active.Country != "PH" || !active.TermsAccepted || key != "xupay-secret" || active.CardAccountLimit != 9 || active.SessionTimeoutMinutes != 45 {
		t.Fatal("wrong supplier or shared limits after restart")
	}
	previous, key, _ := s.GPTPayProviderSettings(gptpay.Tokenseek)
	if previous.PlanCode != "pro5" || previous.Country != "EG" || previous.BackupProvider != "" || key != "legacy-secret" {
		t.Fatal("archiving active configuration overwrote edited row")
	}
	other, key, _ := s.GPTPayProviderSettings(gptpay.CMSNav)
	if other.PlanCode != "pro50" || key != "cms-replacement" {
		t.Fatal("inactive replacement key or plan lost")
	}
	for _, table := range []string{"gptpay_settings", "gptpay_provider_settings"} {
		var raw string
		if err = s.db.QueryRow("SELECT group_concat(profile || encrypted_key) FROM " + table).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		for _, secret := range []string{"legacy-secret", "cms-replacement", "xupay-secret"} {
			if strings.Contains(raw, secret) {
				t.Fatal("plaintext key persisted")
			}
		}
	}
}

func TestGPTPayProviderListRollsBackAllRowsAndAutomation(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	pro := model.DefaultProSettings()
	pay, _, _ := s.GPTPaySettings()
	rows := providerList(t, s)
	for i := range rows {
		rows[i].APIKey = rows[i].Provider + "-secret"
	}
	if _, _, err = s.SaveProAutomationProviderSettings(pro, pay, "", rows, ""); err != nil {
		t.Fatal(err)
	}
	before, _ := json.Marshal(providerList(t, s))
	rows = providerList(t, s)
	for i := range rows {
		rows[i].PlanCode, rows[i].APIKey = "pro20", "replacement-secret"
	}
	pro.Concurrency = 7
	pay.Provider = gptpay.CMSNav
	_, err = s.db.Exec(`CREATE TRIGGER reject_last_supplier BEFORE UPDATE ON gptpay_provider_settings WHEN NEW.provider='xupay' BEGIN SELECT RAISE(ABORT,'fixture rollback'); END`)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.SaveProAutomationProviderSettings(pro, pay, "", rows, ""); err == nil {
		t.Fatal("expected failure")
	}
	after, _ := json.Marshal(providerList(t, s))
	savedPro, _, _, _ := s.ProSettings()
	active, key, _ := s.GPTPaySettings()
	if string(before) != string(after) || savedPro.Concurrency == 7 || active.Provider != gptpay.Tokenseek || key != "tokenseek-secret" {
		t.Fatal("failed list save partially committed")
	}
}
