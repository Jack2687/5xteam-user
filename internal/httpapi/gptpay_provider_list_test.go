package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"chatgpt-space-merge/internal/gptpay"
	"chatgpt-space-merge/internal/model"
)

func TestGPTPayProviderListAPI(t *testing.T) {
	s, st, _ := gptPayFixture(t, func(http.ResponseWriter, *http.Request) { t.Error("configuration save called payment provider") })
	rows := []gptpay.SettingsInput{}
	for _, provider := range []string{gptpay.Tokenseek, gptpay.CMSNav, gptpay.XUPay} {
		cfg, _, _ := st.GPTPayProviderSettings(provider)
		cfg.PlanCode = "pro20"
		rows = append(rows, gptpay.SettingsInput{Settings: cfg, APIKey: provider + "-private-key"})
	}
	input := map[string]any{"automation": model.DefaultProSettings(), "gptpay": gptpay.Settings{Provider: gptpay.CMSNav, BackupProvider: gptpay.XUPay, CardAccountLimit: 8}, "providers": rows}
	r := payCall(t, s.saveProAutomationSettings, "", "", input)
	if r.Code != 200 || strings.Contains(r.Body.String(), "private-key") || strings.Contains(r.Body.String(), "api_key") {
		t.Fatal("save failed or leaked supplied key", r.Code)
	}
	var result struct {
		Data struct {
			Providers []gptpay.Settings `json:"providers"`
			GPTPay    gptpay.Settings   `json:"gptpay"`
		} `json:"data"`
	}
	if err := json.Unmarshal(r.Body.Bytes(), &result); err != nil || len(result.Data.Providers) != 3 || result.Data.GPTPay.Provider != gptpay.CMSNav || result.Data.GPTPay.BackupProvider != gptpay.XUPay || result.Data.GPTPay.PlanCode != "pro20" {
		t.Fatal("response lost provider list or selected row", err)
	}
	for _, row := range result.Data.Providers {
		if !row.KeyPresent || row.CardAccountLimit != 8 {
			t.Fatal("response key status or common limit incorrect")
		}
	}
	for _, bad := range [][]gptpay.SettingsInput{{}, rows[:2], {rows[0], rows[0], rows[2]}, {rows[0], rows[1], {Settings: gptpay.Settings{Provider: "unknown"}}}, {rows[0], rows[1], {Settings: gptpay.Settings{Provider: gptpay.XUPay, PlanCode: "pro50"}}}} {
		input["providers"] = bad
		if rec := payCall(t, s.saveProAutomationSettings, "", "", input); rec.Code != 400 {
			t.Fatal("invalid list accepted", rec.Code)
		}
	}
	active, key, _ := st.GPTPaySettings()
	if active.Provider != gptpay.CMSNav || key != "cmsnav-private-key" {
		t.Fatal("invalid list changed active supplier")
	}
	input["providers"] = rows
	for _, backup := range []string{gptpay.CMSNav, "unknown"} {
		input["gptpay"] = gptpay.Settings{Provider: gptpay.CMSNav, BackupProvider: backup}
		if rec := payCall(t, s.saveProAutomationSettings, "", "", input); rec.Code != 400 {
			t.Fatal("invalid primary/backup accepted", rec.Code)
		}
	}
	input["gptpay"] = gptpay.Settings{Provider: gptpay.CMSNav}
	if rec := payCall(t, s.saveProAutomationSettings, "", "", input); rec.Code != 200 {
		t.Fatal("clearing backup failed", rec.Code)
	}
	active, _, _ = st.GPTPaySettings()
	if active.BackupProvider != "" {
		t.Fatal("backup was not cleared")
	}
}
