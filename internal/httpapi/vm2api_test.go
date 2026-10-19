package httpapi

import (
	"chatgpt-space-merge/internal/model"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func TestVM2LifecycleRetryBindingReloginAndDeletion(t *testing.T) {
	for _, runtime := range []string{"docker", "kvm"} {
		t.Run(runtime, func(t *testing.T) { testVM2Lifecycle(t, runtime) })
	}
}

func testVM2Lifecycle(t *testing.T, runtime string) {
	f := newManualRemoveFixture(t)
	p := f.account(t, "vm-child", "mother_kick", false)
	var mu sync.Mutex
	assignment, slotID := "", ""
	creates, imports, deletes := 0, 0, 0
	on := false
	quotaUsed := 20
	managementDenied := false
	firstCreateFailure := true
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if r.Header.Get("Authorization") != "Bearer vm-management" {
			t.Error("target credential changed")
		}
		ok := func(data any) { _ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "data": data}) }
		detail := func() any {
			return map[string]any{"vm": map[string]any{"id": slotID, "assignment_id": assignment, "execution_mode": runtime + "_per_slot", "container_name": "kin-account-unique", "schedulable": on, "pool_id": "", "group_ids": []string{"pool_aaaaaaaa", "pool_bbbbbbbb"}}, "container": map[string]any{"id": "docker-id", "running": true}}
		}
		switch {
		case r.URL.Path == "/api/panel/space-capabilities":
			ok(map[string]any{"version": 1, "docker_per_slot": true, "kvm_per_slot": true, "delete_account": true, "account_groups": true})
		case r.URL.Path == "/api/panel/vms/create":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["execution_mode"] != runtime+"_per_slot" || body["runtime_type"] != runtime || body["start"] != true || body["proxy_id"] != "px-local" || body["pool_id"] != "" {
				t.Error("wrong create contract", body)
			}
			if got, _ := json.Marshal(body["group_ids"]); string(got) != `["pool_aaaaaaaa","pool_bbbbbbbb"]` {
				t.Error("pinned groups changed", string(got))
			}
			machine := body["machine"].(map[string]any)
			if runtime == "kvm" && (machine["vcpus"] != float64(4) || machine["disk_gb"] != float64(40)) {
				t.Error("pinned KVM spec changed", machine)
			}
			incoming, _ := body["assignment_id"].(string)
			if assignment == "" {
				assignment = incoming
				h := sha256.Sum256([]byte(assignment))
				slotID = fmt.Sprintf("space-%x", h[:12])
				creates++
			} else if incoming != assignment {
				t.Error("retry created a new assignment")
			}
			if firstCreateFailure {
				firstCreateFailure = false
				w.WriteHeader(502)
				fmt.Fprint(w, `{"ok":false,"error":{"code":"lost_response"}}`)
				return
			}
			ok(detail())
		case r.URL.Path == "/api/panel/vms/import":
			var b map[string]any
			_ = json.NewDecoder(r.Body).Decode(&b)
			if b["vm_id"] != slotID {
				t.Error("relogin changed slot")
			}
			imports++
			ok(map[string]any{})
		case strings.HasSuffix(r.URL.Path, "/openai-quota/refresh"):
			if managementDenied {
				w.WriteHeader(401)
				fmt.Fprint(w, `{"ok":false,"error":{"code":"unauthorized"}}`)
				return
			}
			ok(map[string]any{"windows": []any{map[string]any{"id": "7d", "used_percent": quotaUsed, "window_minutes": 10080}}})
		case strings.HasSuffix(r.URL.Path, "/schedulable"):
			var b struct {
				On bool `json:"schedulable"`
			}
			_ = json.NewDecoder(r.Body).Decode(&b)
			if !b.On {
				t.Error("removal must not send a scheduling pause request")
			}
			on = b.On
			ok(map[string]any{"in_flight": 0})
		case r.Method == "DELETE":
			if !on || f.kicks.Load() != 1 {
				t.Error("must leave Team then delete directly, without a scheduling pause")
			}
			deletes++
			if deletes == 1 {
				w.WriteHeader(502)
				fmt.Fprint(w, `{"ok":false,"error":{"code":"docker_failed"}}`)
				return
			}
			ok(map[string]any{"deleted": slotID})
		case r.Method == "GET" && r.URL.Path == "/api/panel/vms/"+slotID:
			ok(detail())
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	cfg := model.DefaultVM2APISettings()
	cfg.GroupIDs = []string{"pool_aaaaaaaa", "pool_bbbbbbbb"}
	cfg.RuntimeType, cfg.VCPUs, cfg.DiskGB = runtime, 4, 40
	cfg.URL = srv.URL
	if _, e := f.s.store.SaveVM2APISettings(cfg, "vm-management"); e != nil {
		t.Fatal(e)
	}
	sub := model.DefaultSub2Settings()
	sub.Provider = "vm2api"
	sub.URL = srv.URL
	sub.Email = "legacy-admin@example.com"
	if _, e := f.s.store.SaveSub2Settings(sub, ""); e != nil {
		t.Fatal(e)
	}
	if _, e := f.s.store.SaveFreeAccountOAuth(p.ID, "at-1", "rt-1", "team-vm"); e != nil {
		t.Fatal(e)
	}
	push := func() int {
		r := httptest.NewRequest("POST", "/push", nil)
		r.SetPathValue("id", p.ID)
		w := httptest.NewRecorder()
		f.s.pushFreeAccount(w, r)
		return w.Code
	}
	if push() != 400 {
		t.Fatal("ambiguous create must remain incomplete")
	}
	p, _, _ = f.s.store.FreeAccountCredential(p.ID)
	if p.VM2AssignmentID == "" || p.DownstreamConnectionID == "" || p.PushStatus == "completed" {
		t.Fatal("missing durable retry state")
	}
	// Change both the default provider and server. The retry still owns its original target.
	sub.Provider = "cpa"
	_, _ = f.s.store.SaveSub2Settings(sub, "")
	cfg.URL = "http://127.0.0.1:1"
	cfg.GroupIDs = []string{"pool_cccccccc"}
	if runtime == "kvm" {
		cfg.RuntimeType = "docker"
	} else {
		cfg.RuntimeType = "kvm"
	}
	cfg.Memory, cfg.VCPUs, cfg.DiskGB = "8g", 8, 80
	_, _ = f.s.store.SaveVM2APISettings(cfg, "new-key")
	if status := push(); status != 200 {
		t.Fatalf("retry failed: %d", status)
	}
	p, creds, _ := f.s.store.FreeAccountCredential(p.ID)
	if p.PushStatus != "completed" || !p.VM2ContainerRunning || !p.VM2Schedulable || p.PushProvider != "vm2api" || p.VM2RuntimeType != runtime {
		t.Fatalf("not ready: %+v", p)
	}
	mu.Lock()
	on = false
	mu.Unlock()
	creds.OAuthAccessToken = "at-2"
	creds.OAuthRefreshToken = "rt-2"
	if e := f.s.reloginVMAccount(t.Context(), p, creds); e != nil {
		t.Fatal(e)
	}
	p, _, _ = f.s.store.FreeAccountCredential(p.ID)
	if p.VM2Schedulable || p.ReloginCount != 1 {
		t.Fatal("relogin must preserve manual pause")
	}
	mu.Lock()
	managementDenied = true
	mu.Unlock()
	settings, password, _ := f.s.freePushSettings(p)
	if relogged, err := f.s.checkFreeAccountStatus(t.Context(), p.ID, settings, password); err == nil || relogged {
		t.Fatal("management 401 must not trigger account relogin")
	}
	if _, removed, err := f.s.performFreeAccountQuota(t.Context(), p.ID, true); err == nil || removed || f.kicks.Load() != 0 {
		t.Fatal("management 401 must not remove the Team account")
	}
	mu.Lock()
	managementDenied = false
	quotaUsed = 100
	on = true
	mu.Unlock()
	_, _ = f.s.store.UpdateFreeAccount(p.ID, func(v *model.FreeAccountProfile) {
		v.AutoRemove = true
		v.ExhaustionPolicy = "7d"
		v.Sub2AccountID = 123 // A legacy ID must never route a VM account's management key to Sub2.
	})
	if _, removed, err := f.s.performFreeAccountQuota(t.Context(), p.ID, true); err == nil || removed {
		t.Fatal("quota exhaustion must attempt deletion and retain failed cleanup")
	}
	p, _, _ = f.s.store.FreeAccountCredential(p.ID)
	if p.RemoteRemovedAt == nil || p.DownstreamCleaned || p.RemoveStatus != "cleanup_pending" {
		t.Fatal("cleanup state lost")
	}
	if w := callManualRemoval(f.s, p.ID); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	p, _, _ = f.s.store.FreeAccountCredential(p.ID)
	if !p.DownstreamCleaned || p.VM2Stage != "deleted" || p.RemoveStatus != "completed" || creates != 1 || imports != 2 || deletes != 2 || f.kicks.Load() != 1 {
		t.Fatalf("bad lifecycle: creates=%d imports=%d deletes=%d kicks=%d", creates, imports, deletes, f.kicks.Load())
	}
}

func TestSettingsSwitchPinsLegacyButNotUnpushedAccounts(t *testing.T) {
	f := newManualRemoveFixture(t)
	old := f.account(t, "legacy", "mother_kick", false)
	newAccount := f.account(t, "unbound", "mother_kick", false)
	sub := model.DefaultSub2Settings()
	sub.URL = "https://original.example"
	sub.Email = "admin@example.com"
	_, _ = f.s.store.SaveSub2Settings(sub, "original-password")
	_, _ = f.s.store.UpdateFreeAccount(old.ID, func(p *model.FreeAccountProfile) {
		p.PushProvider = "sub2"
		p.Sub2AccountID = 42
		p.PushStatus = "completed"
	})
	r := httptest.NewRequest("PUT", "/api/push-settings", strings.NewReader(`{"provider":"vm2api","vm2api":{"url":"https://vm.example","key":"management","kernel":"ubuntu-24.04","memory":"1g","locale":"en_US.UTF-8","status_check_interval_seconds":120,"quota_check_interval_seconds":120}}`))
	w := httptest.NewRecorder()
	f.s.savePushSettings(w, r)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	old, _, _ = f.s.store.FreeAccountCredential(old.ID)
	target, e := f.s.downstreamTarget(old)
	if e != nil || target.Provider != "sub2" || target.Sub2.URL != sub.URL || target.Secret != "original-password" || old.Sub2AccountID != 42 || old.PushStatus != "completed" {
		t.Fatal("legacy binding lost", e)
	}
	newAccount, _, _ = f.s.store.FreeAccountCredential(newAccount.ID)
	if newAccount.DownstreamConnectionID != "" {
		t.Fatal("unpushed account pinned by settings edit")
	}
	target, e = f.s.downstreamTarget(newAccount)
	if e != nil || target.Provider != "vm2api" {
		t.Fatal("new account should use new default")
	}
}

func TestVMRuntimeSettingsPinOnlyAtFirstPush(t *testing.T) {
	f := newManualRemoveFixture(t)
	p := f.account(t, "unpushed-runtime", "mother_kick", false)
	sub := model.DefaultSub2Settings()
	sub.Provider = "vm2api"
	_, _ = f.s.store.SaveSub2Settings(sub, "")
	cfg := model.DefaultVM2APISettings()
	cfg.URL = "https://vm.example.com"
	_, _ = f.s.store.SaveVM2APISettings(cfg, "secret")
	first, err := f.s.downstreamTarget(p)
	if err != nil || first.VM2API.RuntimeType != "docker" {
		t.Fatal(first, err)
	}
	cfg.RuntimeType, cfg.VCPUs, cfg.DiskGB = "kvm", 4, 40
	_, _ = f.s.store.SaveVM2APISettings(cfg, "")
	latest, err := f.s.downstreamTarget(p)
	if err != nil || latest.VM2API.RuntimeType != "kvm" {
		t.Fatal("unpushed must see latest settings", err)
	}
	_, err = f.s.downstreamTargetLocked(p, true)
	if err != nil {
		t.Fatal(err)
	}
	cfg.RuntimeType, cfg.VCPUs, cfg.DiskGB = "docker", 8, 80
	_, _ = f.s.store.SaveVM2APISettings(cfg, "")
	pinned, err := f.s.downstreamTarget(p)
	if err != nil || pinned.VM2API.RuntimeType != "kvm" || pinned.VM2API.VCPUs != 4 || pinned.VM2API.DiskGB != 40 {
		t.Fatal("bound target changed", err)
	}
}
