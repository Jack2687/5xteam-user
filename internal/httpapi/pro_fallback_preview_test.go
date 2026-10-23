package httpapi

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"chatgpt-space-merge/internal/restock"
	"chatgpt-space-merge/internal/sub2"
)

func TestFallbackPreviewDeletedGroupsAndPartialFailure(t *testing.T) {
	s, st, m, _ := fallbackFixture(t)
	f, _, _ := st.ProFallbackConfig()
	f.Enabled = false
	f.GroupIDs = []int64{99, 7, 8}
	m.group[2] = 8
	for _, failed := range []bool{false, true} {
		if failed {
			m.failGroup = 8
		}
		raw, _ := json.Marshal(fallbackInput{FallbackConfig: f})
		w := httptest.NewRecorder()
		s.fallbackAccounts(w, httptest.NewRequest("POST", "/", strings.NewReader(string(raw))))
		var result struct {
			Data struct {
				Accounts []sub2.FallbackAccount `json:"accounts"`
				Missing  []int64                `json:"missing_group_ids"`
				Failed   []int64                `json:"failed_group_ids"`
				Complete bool                   `json:"complete"`
			} `json:"data"`
		}
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &result) != nil {
			t.Fatal(w.Code, w.Body.String())
		}
		want := 3
		if failed {
			want = 2
		}
		if len(result.Data.Accounts) != want || len(result.Data.Missing) != 1 || result.Data.Missing[0] != 99 || result.Data.Complete == failed || (len(result.Data.Failed) > 0) != failed {
			t.Fatal(w.Body.String())
		}
		if len(m.writes) != 0 || strings.Contains(w.Body.String(), "DO-NOT") {
			t.Fatal("preview leaked secrets or changed scheduling")
		}
	}
	current, _, _ := st.ProFallbackConfig()
	if current.Version != f.Version {
		t.Fatal("preview changed configuration")
	}
}

func TestFallbackNamesAndDeletedSelectionRecovery(t *testing.T) {
	s, st, m, _ := fallbackFixture(t)
	if err := s.tickProFallback(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	delete(m.on, 1)
	delete(m.group, 1)
	m.group[1] = 99
	get := httptest.NewRecorder()
	s.getProFallback(get, httptest.NewRequest("GET", "/", nil))
	var display struct {
		Data struct {
			Config restock.FallbackConfig `json:"config"`
		} `json:"data"`
	}
	if json.Unmarshal(get.Body.Bytes(), &display) != nil || display.Data.Config.AccountNames[1] != "fallback-1" {
		t.Fatal("deleted name lost", get.Body.String())
	}
	f, _, _ := st.ProFallbackConfig()
	f.AccountIDs = []int64{2}
	f.AccountNames = map[int64]string{2: "untrusted name", 999: "unselected"}
	raw, _ := json.Marshal(fallbackInput{FallbackConfig: f})
	save := httptest.NewRecorder()
	s.saveProFallback(save, httptest.NewRequest("PUT", "/", strings.NewReader(string(raw))))
	if save.Code != 200 {
		t.Fatal(save.Code, save.Body.String())
	}
	saved, _, _ := st.ProFallbackConfig()
	if len(saved.AccountNames) != 1 || saved.AccountNames[2] != "fallback-2" {
		t.Fatal(saved.AccountNames)
	}
	if err := s.tickProFallback(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	bindings, _ := st.ProFallbackBindings()
	for _, b := range bindings {
		if b.AccountID == 1 {
			t.Fatal("deleted deselected account was not drained")
		}
	}
	// Disabling retains verified names while removing unused snapshots.
	saved.Enabled = false
	raw, _ = json.Marshal(fallbackInput{FallbackConfig: saved})
	save = httptest.NewRecorder()
	s.saveProFallback(save, httptest.NewRequest("PUT", "/", strings.NewReader(string(raw))))
	if save.Code != 200 {
		t.Fatal(save.Code, save.Body.String())
	}
	saved, _, _ = st.ProFallbackConfig()
	if saved.AccountNames[2] != "fallback-2" {
		t.Fatal("disabled config lost name")
	}
}
