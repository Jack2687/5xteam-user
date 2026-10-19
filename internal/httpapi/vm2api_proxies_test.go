package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"chatgpt-space-merge/internal/model"
)

func TestVMProxyListUsesSavedKeyOrUnsavedConnectionWithoutSaving(t *testing.T) {
	f := newManualRemoveFixture(t)
	for _, key := range []string{"saved-key", "edited-key"} {
		t.Run(key, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer "+key || r.URL.Path != "/api/panel/proxies" {
					t.Error("wrong connection")
				}
				fmt.Fprint(w, `{"ok":true,"data":{"proxies":[{"id":"px-local","enabled":true,"bound_count":2,"bind_limit":32}]}}`)
			}))
			defer upstream.Close()
			cfg := model.DefaultVM2APISettings()
			cfg.URL = "https://saved.example"
			if _, err := f.s.store.SaveVM2APISettings(cfg, "saved-key"); err != nil {
				t.Fatal(err)
			}
			value := ""
			if key == "edited-key" {
				value = key
			}
			payload, _ := json.Marshal(map[string]any{"vm2api": map[string]string{"url": upstream.URL, "key": value}})
			w := httptest.NewRecorder()
			f.s.getVMProxies(w, httptest.NewRequest("POST", "/api/push-settings/vm2api/proxies", strings.NewReader(string(payload))))
			if w.Code != 200 || !strings.Contains(w.Body.String(), "px-local") {
				t.Fatal(w.Body.String())
			}
			after, saved, _ := f.s.store.VM2APISettings()
			if after.URL != cfg.URL || saved != "saved-key" {
				t.Fatal("reading proxies changed saved connection")
			}
		})
	}
}

func TestVMProxySettingsRoundTripAndPinnedTarget(t *testing.T) {
	f := newManualRemoveFixture(t)
	payload := `{"provider":"vm2api","vm2api":{"url":"https://vm.example","key":"management","kernel":"ubuntu-24.04","memory":"1g","locale":"en_US.UTF-8","status_check_interval_seconds":120,"quota_check_interval_seconds":120,"group_ids":["pool_bbbbbbbb"," pool_aaaaaaaa ","pool_bbbbbbbb"],"bind_proxy":true,"proxy_ids":["px-a"," px-b ","px-a"]}}`
	w := httptest.NewRecorder()
	f.s.savePushSettings(w, httptest.NewRequest("PUT", "/api/push-settings", strings.NewReader(payload)))
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	cfg, key, err := f.s.store.VM2APISettings()
	if err != nil || !cfg.BindProxy || !reflect.DeepEqual(cfg.ProxyIDs, []string{"px-a", "px-b"}) {
		t.Fatalf("settings lost: %+v %v", cfg, err)
	}
	if !reflect.DeepEqual(cfg.GroupIDs, []string{"pool_aaaaaaaa", "pool_bbbbbbbb"}) {
		t.Fatal("group settings lost", cfg.GroupIDs)
	}
	id, err := f.s.store.SaveDownstreamTarget(model.DownstreamTarget{Provider: "vm2api", VM2API: cfg, Secret: key})
	if err != nil {
		t.Fatal(err)
	}
	cfg.ProxyIDs = []string{}
	cfg.GroupIDs = []string{"pool_cccccccc"}
	if _, err := f.s.store.SaveVM2APISettings(cfg, ""); err != nil {
		t.Fatal(err)
	}
	target, err := f.s.store.DownstreamTarget(id)
	if err != nil || !reflect.DeepEqual(target.VM2API.ProxyIDs, []string{"px-a", "px-b"}) {
		t.Fatal("settings edit changed pinned cycle", err)
	}
	if !reflect.DeepEqual(target.VM2API.GroupIDs, []string{"pool_aaaaaaaa", "pool_bbbbbbbb"}) {
		t.Fatal("settings changed pinned groups", target.VM2API.GroupIDs)
	}
	// An enabled but empty choice is valid and falls back to local, like disabling it.
	payload = strings.Replace(payload, `["px-a"," px-b ","px-a"]`, `[]`, 1)
	w = httptest.NewRecorder()
	f.s.savePushSettings(w, httptest.NewRequest("PUT", "/api/push-settings", strings.NewReader(payload)))
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
}
