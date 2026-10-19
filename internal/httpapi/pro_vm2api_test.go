package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"chatgpt-space-merge/internal/model"
	"chatgpt-space-merge/internal/store"
)

type proVMFixture struct {
	s                      *Server
	email                  string
	settings               model.ProSettings
	calls                  []string
	creates                []map[string]any
	assignment, slot, mode string
	groupIDs               []string
	scheduled              bool
	failCreate, failDelete bool
}

func newProVMFixture(t *testing.T, runtime string) *proVMFixture {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	f := &proVMFixture{s: &Server{store: st}, email: "pro-vm@example.com", settings: model.DefaultProSettings()}
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer pro-management-key" {
			t.Error("Pro used another scope's key")
			http.Error(w, "unauthorized", 401)
			return
		}
		f.calls = append(f.calls, r.Method+" "+r.URL.Path)
		respond := func(data any) { json.NewEncoder(w).Encode(map[string]any{"ok": true, "data": data}) }
		fail := func() {
			w.WriteHeader(503)
			json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": map[string]any{"code": "temporary", "message": "retry later"}})
		}
		slot := func() any {
			return map[string]any{"vm": map[string]any{"id": f.slot, "assignment_id": f.assignment, "execution_mode": f.mode, "container_name": "kin-pro-vm", "status": "running", "schedulable": f.scheduled, "group_ids": f.groupIDs}, "container": map[string]any{"id": "runtime", "running": true}}
		}
		switch {
		case r.URL.Path == "/api/panel/space-capabilities":
			respond(map[string]any{"version": 1, "docker_per_slot": true, "kvm_per_slot": true, "delete_account": true, "proxy_selection": true, "account_groups": true})
		case r.URL.Path == "/api/panel/proxies":
			respond(map[string]any{"proxies": []any{}})
		case r.URL.Path == "/api/panel/vms/create":
			var body map[string]any
			json.NewDecoder(r.Body).Decode(&body)
			f.creates = append(f.creates, body)
			raw, _ := json.Marshal(body["group_ids"])
			json.Unmarshal(raw, &f.groupIDs)
			assignment := body["assignment_id"].(string)
			if f.assignment != "" && f.assignment != assignment {
				t.Error("retry created another assignment")
			}
			f.assignment, f.mode = assignment, body["execution_mode"].(string)
			h := sha256.Sum256([]byte(assignment))
			f.slot = fmt.Sprintf("space-%x", h[:12])
			if f.failCreate {
				f.failCreate = false
				fail()
				return
			}
			respond(slot())
		case r.URL.Path == "/api/panel/vms/import":
			var body map[string]any
			json.NewDecoder(r.Body).Decode(&body)
			if body["vm_id"] != f.slot || body["email"] != f.email || body["access_token"] == "" {
				t.Error("invalid import")
			}
			respond(map[string]any{})
		case r.Method == "DELETE":
			if r.Header.Get("X-Space-Assignment") != f.assignment || r.URL.Path != "/api/panel/vms/"+f.slot {
				t.Error("wrong delete target")
			}
			if f.failDelete {
				f.failDelete = false
				fail()
				return
			}
			respond(map[string]any{"deleted": f.slot})
		case strings.HasSuffix(r.URL.Path, "/schedulable"):
			var body map[string]bool
			json.NewDecoder(r.Body).Decode(&body)
			f.scheduled = body["schedulable"]
			respond(map[string]any{})
		case strings.HasSuffix(r.URL.Path, "/openai-quota/refresh"):
			respond(map[string]any{"windows": []any{map[string]any{"id": "5h", "used_percent": 10, "window_minutes": 300}, map[string]any{"id": "7d", "used_percent": 100, "window_minutes": 10080}}})
		case r.Method == "GET" && r.URL.Path == "/api/panel/vms/"+f.slot:
			respond(slot())
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(remote.Close)
	f.settings.Provider = "vm2api"
	f.settings.VM2API.URL, f.settings.VM2API.RuntimeType = remote.URL, runtime
	f.settings.VM2API.BindProxy, f.settings.VM2API.ProxyIDs = true, []string{"proxy-a", "proxy-b"}
	f.settings.VM2API.VCPUs, f.settings.VM2API.DiskGB = 4, 40
	if _, err = st.SaveProSettings(f.settings, "", "", "pro-management-key"); err != nil {
		t.Fatal(err)
	}
	team := model.DefaultVM2APISettings()
	team.URL = remote.URL
	if _, err = st.SaveVM2APISettings(team, "team-key-must-not-be-used"); err != nil {
		t.Fatal(err)
	}
	if _, err = st.SaveMailAccount(model.MailAccountProfile{Email: f.email}, model.MailAccountCredentials{Email: f.email, AccessToken: "at", RefreshToken: "rt"}); err != nil {
		t.Fatal(err)
	}
	if _, err = st.UpdateMailAccountManagementScope(f.email, "pro"); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *proVMFixture) count(suffix string) int {
	n := 0
	for _, call := range f.calls {
		if strings.HasSuffix(call, suffix) {
			n++
		}
	}
	return n
}

func TestProVMFullPushRetryQuotaAndPinnedReauthorization(t *testing.T) {
	for _, runtime := range []string{"docker", "kvm"} {
		t.Run(runtime, func(t *testing.T) {
			f := newProVMFixture(t, runtime)
			f.settings.VM2API.GroupIDs = []string{"pool_aaaaaaaa", "pool_bbbbbbbb"}
			if _, err := f.s.store.SaveProSettings(f.settings, "", ""); err != nil {
				t.Fatal(err)
			}
			f.failCreate = true
			if _, err := f.s.performProPush(t.Context(), f.email); err == nil {
				t.Fatal("expected ambiguous creation")
			}
			p, _, _ := f.s.store.MailAccountCredential(f.email)
			if p.VM2 == nil || p.VM2.AssignmentID != f.assignment || p.PushStatus != "failed" {
				t.Fatal("binding not retained on failure")
			}
			p, err := f.s.performProPush(t.Context(), f.email)
			if err != nil {
				t.Fatal(err)
			}
			if p.PushStatus != "completed" || !p.VM2.Running || !p.VM2.Schedulable || p.VM2.RuntimeType != runtime {
				t.Fatalf("not ready: %+v", p.VM2)
			}
			if len(f.creates) != 2 || f.creates[1]["assignment_id"] != f.creates[0]["assignment_id"] {
				t.Fatal("non-idempotent retry")
			}
			body := f.creates[0]
			if got, _ := json.Marshal(body["group_ids"]); string(got) != `["pool_aaaaaaaa","pool_bbbbbbbb"]` {
				t.Fatal("groups not assigned", string(got))
			}
			if body["execution_mode"] != runtime+"_per_slot" || body["start"] != true || len(body["proxy_ids"].([]any)) != 2 {
				t.Fatalf("wrong create: %+v", body)
			}
			machine := body["machine"].(map[string]any)
			if runtime == "kvm" && (machine["vcpus"] != float64(4) || machine["disk_gb"] != float64(40)) {
				t.Fatal("KVM spec lost")
			}
			if _, err = f.s.performProQuota(t.Context(), f.email, false); err != nil {
				t.Fatal(err)
			}
			if f.count("/schedulable") != 1 {
				t.Fatal("quota changed scheduling")
			}
			// Switch endpoint, shape and provider after the first successful push.
			next := f.settings
			next.Provider = "cpa"
			next.CPA.URL = "http://cpa.invalid"
			next.VM2API.URL = "http://other.invalid"
			next.VM2API.RuntimeType = "docker"
			next.VM2API.GroupIDs = []string{"pool_cccccccc"}
			if _, err = f.s.store.SaveProSettings(next, "", "cpa-key", "new-vm-key"); err != nil {
				t.Fatal(err)
			}
			f.scheduled = false
			if err = f.s.store.SaveMailAccountOAuth(f.email, "new-at", "new-rt"); err != nil {
				t.Fatal(err)
			}
			if err = f.s.syncProVMAfterOAuth(t.Context(), f.email); err != nil {
				t.Fatal(err)
			}
			p, _, _ = f.s.store.MailAccountCredential(f.email)
			if len(f.creates) != 2 || p.VM2.RuntimeType != runtime || p.VM2.Schedulable || f.count("/schedulable") != 1 || f.count("/vms/import") != 2 {
				t.Fatal("reauthorization changed runtime or resumed scheduling")
			}
			if _, err = f.s.performProQuota(t.Context(), f.email, false); err != nil {
				t.Fatal("settings switch lost old connection:", err)
			}
		})
	}
}

func TestProVMCleanupRetrySkipsSuccessfulTeamOperations(t *testing.T) {
	f := newProVMFixture(t, "kvm")
	if _, err := f.s.performProPush(t.Context(), f.email); err != nil {
		t.Fatal(err)
	}
	_, err := f.s.store.UpdateProAccount(f.email, func(p *model.MailAccountProfile) {
		p.SpaceMergedOnce = true
		p.InviteStatus, p.AcceptStatus, p.TransferStatus, p.RemoveStatus = "completed", "completed", "completed", "team_removed"
		p.ProAuto = model.ProAutoState{ID: "paid", Status: "interrupted", Stage: "merge", Steps: map[string]string{"merge": "pending"}, StartedAt: time.Now()}
	})
	if err != nil {
		t.Fatal(err)
	}
	f.failDelete = true
	if _, err = f.s.performProMerge(t.Context(), f.email); err == nil {
		t.Fatal("expected delete failure")
	}
	p, _, _ := f.s.store.MailAccountCredential(f.email)
	if p.VM2.Stage != "delete_failed" || p.RemoveStatus != "team_removed" || p.ProAuto.Status == "completed" {
		t.Fatalf("lost cleanup progress: %+v", p.VM2)
	}
	if p.ProStages().Steps["merge"] != "failed" || p.ProStages().Errors["merge"] == "" {
		t.Fatal("cleanup failure missing from account status")
	}
	p, err = f.s.performProMerge(t.Context(), f.email)
	if err != nil {
		t.Fatal(err)
	}
	if p.VM2.DeletedAt == nil || p.RemoveStatus != "completed" || p.VM2.Running || p.VM2.Schedulable || p.ProAuto.Status != "completed" {
		t.Fatalf("cleanup incomplete: %+v", p)
	}
	before := len(f.calls)
	if _, err = f.s.performProMerge(t.Context(), f.email); err != nil {
		t.Fatal(err)
	}
	if len(f.calls) != before {
		t.Fatal("completed cleanup repeated remote operations")
	}
	if _, err = f.s.performProPush(t.Context(), f.email); err == nil {
		t.Fatal("deleted slot resurrected")
	}
}

func TestProVMDeletePreservesLocalBindingUntilRemoteCleanupSucceeds(t *testing.T) {
	f := newProVMFixture(t, "docker")
	if _, err := f.s.performProPush(t.Context(), f.email); err != nil {
		t.Fatal(err)
	}
	f.failDelete = true
	if err := f.s.deleteProAccount(f.email, t.Context()); err == nil {
		t.Fatal("expected failure")
	}
	p, _, err := f.s.store.MailAccountCredential(f.email)
	if err != nil || p.VM2 == nil || p.VM2.DeletedAt != nil {
		t.Fatal("lost retry binding")
	}
	if err = f.s.deleteProAccount(f.email, t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, _, err = f.s.store.MailAccountCredential(f.email); err == nil {
		t.Fatal("local account not deleted")
	}
}

func TestProVMUnsavedTestAndProxyLookupUseProKey(t *testing.T) {
	f := newProVMFixture(t, "kvm")
	for _, handler := range []http.HandlerFunc{f.s.testProVM, f.s.getProVMProxies} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("POST", "/", strings.NewReader(`{"vm2api":{"url":"`+f.settings.VM2API.URL+`"}}`))
		handler(w, r)
		if w.Code != 200 {
			t.Fatalf("%d: %s", w.Code, w.Body.String())
		}
	}
	w := httptest.NewRecorder()
	f.s.getProSettings(w, httptest.NewRequest("GET", "/", nil))
	if strings.Contains(w.Body.String(), "pro-management-key") || strings.Contains(w.Body.String(), "team-key") {
		t.Fatal("settings leaked a key")
	}
}

func TestProVMAutomationTailAndSettingsSwitch(t *testing.T) {
	f := newProVMFixture(t, "kvm")
	if err := f.s.validateProAutoPush(model.MailAccountProfile{}, f.settings, ""); err != nil {
		t.Fatal(err)
	}
	_, err := f.s.store.UpdateProAccount(f.email, func(p *model.MailAccountProfile) {
		p.ProAuto = model.ProAutoState{ID: "paid-pro", Status: "running", Stage: "push", QuotaUsedThreshold: 100, QuotaIntervalSeconds: 120, Steps: map[string]string{"oauth": "completed", "recharge": "completed"}}
	})
	if err != nil {
		t.Fatal(err)
	}
	merged := false
	f.s.proAutoHooks = &proAutomationHooks{Merge: func(ctx context.Context, email string) (model.MailAccountProfile, error) {
		merged = true
		p, err := f.s.store.UpdateProAccount(email, func(p *model.MailAccountProfile) {
			p.SpaceMergedOnce = true
			p.InviteStatus, p.AcceptStatus, p.TransferStatus, p.RemoveStatus = "completed", "completed", "completed", "team_removed"
		})
		if err != nil {
			return p, err
		}
		return f.s.finishProDownstream(ctx, p)
	}}
	if err = f.s.runProAutomationTail(t.Context(), f.email); err != nil {
		t.Fatal(err)
	}
	p, _, _ := f.s.store.MailAccountCredential(f.email)
	if !merged || p.ProAuto.Status != "completed" || p.VM2.DeletedAt == nil || p.VM2.RuntimeType != "kvm" {
		t.Fatal("automatic VM pipeline not complete")
	}
	before := *p.VM2
	next := f.settings
	next.Provider = "cpa"
	next.CPA.URL = "http://cpa.example"
	body, _ := json.Marshal(proSettingsInput{ProSettings: next, CPAKey: "cpa-secret"})
	w := httptest.NewRecorder()
	f.s.saveProSettings(w, httptest.NewRequest("PUT", "/", strings.NewReader(string(body))))
	if w.Code != 200 {
		t.Fatalf("save: %s", w.Body.String())
	}
	p, _, _ = f.s.store.MailAccountCredential(f.email)
	if p.VM2 == nil || p.VM2.AssignmentID != before.AssignmentID || p.PushProvider != "vm2api" || p.PushStatus != "removed" {
		t.Fatal("provider switch reset existing VM binding")
	}
}

func TestProVMMergeDeletesRuntimeOnlyAfterSuccessfulRemoval(t *testing.T) {
	flow := newProExecutionFixture(t)
	vm := newProVMFixture(t, "kvm")
	vm.s, vm.email = flow.s, flow.email
	vm.settings.TargetAdminID, vm.settings.RetryCount = flow.admin.ID, 0
	if _, err := flow.s.store.SaveProSettings(vm.settings, "", "", "pro-management-key"); err != nil {
		t.Fatal(err)
	}
	if _, err := flow.s.performProPush(t.Context(), flow.email); err != nil {
		t.Fatal(err)
	}
	flow.failStage, flow.failCount = "remove", 1
	if _, err := flow.s.performProMerge(t.Context(), flow.email); err == nil {
		t.Fatal("expected removal failure")
	}
	for _, call := range vm.calls {
		if strings.HasPrefix(call, "DELETE ") {
			t.Fatal("runtime deleted before successful removal")
		}
	}
	vm.failDelete = true
	if _, err := flow.s.performProMerge(t.Context(), flow.email); err == nil {
		t.Fatal("expected runtime deletion failure")
	}
	p, _, _ := flow.s.store.MailAccountCredential(flow.email)
	if p.RemoveStatus != "team_removed" || p.VM2.Stage != "delete_failed" {
		t.Fatal("successful team removal was not persisted")
	}
	if _, err := flow.s.performProMerge(t.Context(), flow.email); err != nil {
		t.Fatal(err)
	}
	flow.mu.Lock()
	defer flow.mu.Unlock()
	for stage, count := range flow.calls {
		want := 1
		if stage == "remove" {
			want = 2
		}
		if count != want {
			t.Errorf("%s called %d times, want %d", stage, count, want)
		}
	}
	p, _, _ = flow.s.store.MailAccountCredential(flow.email)
	if p.VM2.DeletedAt == nil || p.ProStages().Steps["merge"] != "completed" {
		t.Fatal("successful runtime cleanup not reflected in the list")
	}
}
