package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"chatgpt-space-merge/internal/model"
)

func TestVMGroupsUseIndependentSavedKeysAndUnsavedConnection(t *testing.T) {
	f := newManualRemoveFixture(t)
	team := model.DefaultVM2APISettings()
	team.URL = "https://saved-team.example"
	pro := model.DefaultProSettings()
	pro.VM2API = team
	pro.VM2API.URL = "https://saved-pro.example"
	if _, err := f.s.store.SaveVM2APISettings(team, "team-key"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.store.SaveProSettings(pro, "", "", "pro-key"); err != nil {
		t.Fatal(err)
	}
	for _, scope := range []string{"team", "pro"} {
		for _, edited := range []bool{false, true} {
			expected := scope + "-key"
			key := ""
			if edited {
				expected = "edited-key"
				key = expected
			}
			remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer "+expected || r.URL.Path != "/api/panel/vm-groups" {
					t.Error("wrong group connection", scope)
				}
				json.NewEncoder(w).Encode(map[string]any{"ok": true, "data": map[string]any{"groups": []any{map[string]any{"id": "pool_aaaaaaaa", "name": "A", "enabled": true, "account_count": 2}}}})
			}))
			body := map[string]any{"vm2api": map[string]any{"url": remote.URL, "key": key}}
			handler := f.s.getVMGroups
			if scope == "pro" {
				handler = f.s.getProVMGroups
				body = map[string]any{"vm2api": map[string]any{"url": remote.URL}, "vm2api_key": key}
			}
			raw, _ := json.Marshal(body)
			w := httptest.NewRecorder()
			handler(w, httptest.NewRequest("POST", "/", strings.NewReader(string(raw))))
			remote.Close()
			if w.Code != 200 || !strings.Contains(w.Body.String(), "pool_aaaaaaaa") || strings.Contains(w.Body.String(), expected) {
				t.Fatal(w.Code, w.Body.String())
			}
		}
	}
	a, key, _ := f.s.store.VM2APISettings()
	b, proKey, _ := f.s.store.ProVM2APISettings()
	if a.URL != team.URL || b.URL != pro.VM2API.URL || key != "team-key" || proKey != "pro-key" {
		t.Fatal("reading groups mutated settings")
	}
}
