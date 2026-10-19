package vm2api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"chatgpt-space-merge/internal/model"
)

func TestVMGroupsCreateAndCapabilities(t *testing.T) {
	for _, runtime := range []string{"docker", "kvm"} {
		t.Run(runtime, func(t *testing.T) {
			capable := false
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer management" {
					t.Error("missing management auth")
				}
				var data any
				switch r.URL.Path {
				case "/api/panel/space-capabilities":
					data = map[string]any{"version": 1, "docker_per_slot": true, "kvm_per_slot": true, "delete_account": true, "account_groups": capable}
				case "/api/panel/vm-groups":
					data = map[string]any{"groups": []Group{{ID: "pool_aaaaaaaa", Name: "A", Enabled: true, AccountCount: 2}}}
				case "/api/panel/vms/create":
					var b struct {
						GroupIDs []string `json:"group_ids"`
						Runtime  string   `json:"runtime_type"`
						Start    bool     `json:"start"`
					}
					if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
						t.Error(err)
					}
					if !slices.Equal(b.GroupIDs, []string{"pool_aaaaaaaa", "pool_bbbbbbbb"}) || b.Runtime != runtime || !b.Start {
						t.Errorf("wrong create: %+v", b)
					}
					data = map[string]any{"vm": map[string]any{"group_ids": b.GroupIDs}}
				default:
					t.Error("unexpected endpoint", r.URL.Path)
				}
				json.NewEncoder(w).Encode(map[string]any{"ok": true, "data": data})
			}))
			defer server.Close()
			v := model.DefaultVM2APISettings()
			v.URL = server.URL
			v.RuntimeType = runtime
			c := New(v, "management")
			if err := c.Check(t.Context()); err != nil {
				t.Fatal("legacy/no-group push must work", err)
			}
			c.Settings.GroupIDs = []string{"pool_bbbbbbbb", " pool_aaaaaaaa ", "pool_bbbbbbbb"}
			if err := c.Check(t.Context()); err == nil || !strings.Contains(err.Error(), "请先更新") {
				t.Fatal("old server silently ignored groups", err)
			}
			capable = true
			if err := c.Check(t.Context()); err != nil {
				t.Fatal(err)
			}
			groups, err := c.Groups(t.Context())
			if err != nil || len(groups) != 1 || groups[0].AccountCount != 2 {
				t.Fatal(groups, err)
			}
			slot, err := c.Create(t.Context(), "user@example.com", "space-assignment")
			if err != nil || !SlotGroupsMatch(c.Settings, slot) {
				t.Fatal("groups lost", err)
			}
			slot.VM.GroupIDs = []string{"pool_aaaaaaaa"}
			if SlotGroupsMatch(c.Settings, slot) {
				t.Fatal("partial group assignment accepted")
			}
		})
	}
}
