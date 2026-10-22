package httpapi

import (
	"encoding/json"
	"net/http"
	"testing"

	"chatgpt-space-merge/internal/model"
)

func TestProAutomationSettingsSaveWithoutPushCredentials(t *testing.T) {
	s, st, _ := gptPayFixture(t, func(w http.ResponseWriter, r *http.Request) { t.Error("saving config must not contact supplier") })
	pro := model.DefaultProSettings()
	pro.QuotaUsedThreshold, pro.QuotaCheckIntervalSeconds = 85, 45
	input := map[string]any{"automation": pro, "gptpay": map[string]any{"plan_code": "pro5"}}
	rec := payCall(t, s.saveProAutomationSettings, "", "", input)
	if rec.Code != 200 {
		t.Fatal(rec.Body.String())
	}
	saved, _, _, _ := st.ProSettings()
	if saved.QuotaUsedThreshold != 85 || saved.QuotaCheckIntervalSeconds != 45 {
		t.Fatal("settings not persisted")
	}
	for _, threshold := range []float64{0, -1, 101} {
		pro.QuotaUsedThreshold = threshold
		input["automation"] = pro
		if rec = payCall(t, s.saveProAutomationSettings, "", "", input); rec.Code != 400 {
			t.Fatalf("accepted invalid threshold %v", threshold)
		}
	}
	pro.QuotaUsedThreshold, pro.AutoMergeEnabled = 85, true
	input["automation"] = pro
	if rec = payCall(t, s.saveProAutomationSettings, "", "", input); rec.Code != 400 {
		t.Fatal("auto merge accepted with no target")
	}
	saved, _, _, _ = st.ProSettings()
	if saved.QuotaUsedThreshold != 85 || saved.AutoMergeEnabled {
		t.Fatal("failed request changed settings")
	}
}

func TestProDeletionIntervalSettings(t *testing.T) {
	s, st, _ := gptPayFixture(t, func(http.ResponseWriter, *http.Request) { t.Error("unexpected upstream request") })
	pro := model.DefaultProSettings()
	input := map[string]any{"automation": pro, "gptpay": map[string]any{"plan_code": "pro5"}}
	for _, interval := range []int{1, 86400, 37, 0, -1, 86401} {
		pro.DeletionCheckIntervalSeconds = interval
		input["automation"] = pro
		want := 200
		if interval < 1 || interval > 86400 {
			want = 400
		}
		if rec := payCall(t, s.saveProAutomationSettings, "", "", input); rec.Code != want {
			t.Fatal(interval, rec.Code, rec.Body.String())
		}
	}
	encoded, _ := json.Marshal(pro)
	var legacy map[string]any
	if err := json.Unmarshal(encoded, &legacy); err != nil {
		t.Fatal(err)
	}
	delete(legacy, "deletion_check_interval_seconds")
	input["automation"] = legacy
	if rec := payCall(t, s.saveProAutomationSettings, "", "", input); rec.Code != 200 {
		t.Fatal(rec.Body.String())
	}
	downstream := map[string]any{"provider": "cpa", "cpa": map[string]any{"url": "http://cpa.invalid"}, "cpa_key": "fixture-key", "deletion_check_interval_seconds": 999}
	if rec := payCall(t, s.saveProSettings, "", "", downstream); rec.Code != 200 {
		t.Fatal(rec.Body.String())
	}
	saved, _, _, _ := st.ProSettings()
	if saved.DeletionCheckIntervalSeconds != 37 {
		t.Fatal("invalid, legacy or downstream save changed deletion interval")
	}
}

func TestProAutomationMergeConcurrencySettings(t *testing.T) {
	s, st, _ := gptPayFixture(t, func(w http.ResponseWriter, r *http.Request) { t.Error("unexpected supplier call") })
	pro := model.DefaultProSettings()
	pro.Concurrency, pro.MergeConcurrency = 6, 3
	input := map[string]any{"automation": pro, "gptpay": map[string]any{"plan_code": "pro5"}}
	if rec := payCall(t, s.saveProAutomationSettings, "", "", input); rec.Code != 200 {
		t.Fatal(rec.Body.String())
	}
	saved, _, _, _ := st.ProSettings()
	if saved.Concurrency != 6 || saved.MergeConcurrency != 3 {
		t.Fatal("independent settings not saved", saved)
	}
	for _, invalid := range []int{0, -1, 21} {
		pro.MergeConcurrency = invalid
		input["automation"] = pro
		if rec := payCall(t, s.saveProAutomationSettings, "", "", input); rec.Code != 400 {
			t.Fatal("invalid merge limit accepted", invalid)
		}
	}
	// A pre-upgrade browser omitting the field must not reset the saved limit.
	encoded, _ := json.Marshal(pro)
	var legacy map[string]any
	if err := json.Unmarshal(encoded, &legacy); err != nil {
		t.Fatal(err)
	}
	delete(legacy, "merge_concurrency")
	legacy["concurrency"] = 8
	input["automation"] = legacy
	if rec := payCall(t, s.saveProAutomationSettings, "", "", input); rec.Code != 200 {
		t.Fatal(rec.Body.String())
	}
	saved, _, _, _ = st.ProSettings()
	if saved.Concurrency != 8 || saved.MergeConcurrency != 3 {
		t.Fatal("legacy save reset merge limit")
	}
	// The separate downstream form must retain the automation-owned limit.
	inputDownstream := map[string]any{"provider": "cpa", "cpa": map[string]any{"url": "http://cpa.invalid"}, "cpa_key": "fixture-key"}
	if rec := payCall(t, s.saveProSettings, "", "", inputDownstream); rec.Code != 200 {
		t.Fatal(rec.Body.String())
	}
	saved, _, _, _ = st.ProSettings()
	if saved.MergeConcurrency != 3 {
		t.Fatal("downstream settings reset merge limit")
	}
}
