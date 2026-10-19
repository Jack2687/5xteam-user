package httpapi

import (
	"bytes"
	"chatgpt-space-merge/internal/model"
	"chatgpt-space-merge/internal/vm2api"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type vmQualityFixture struct {
	*qualityFixture
	cfg          model.VM2APISettings
	server       *httptest.Server
	target       string
	reads        atomic.Int32
	vmDeletes    atomic.Int32
	failRead     atomic.Bool
	failVMDelete atomic.Bool
	muVM         sync.Mutex
	readSizes    []int
	snapshotHook func(*vm2api.QualitySnapshot, int32)
}

func newVMQualityFixture(t *testing.T) *vmQualityFixture {
	t.Helper()
	f := &vmQualityFixture{qualityFixture: newQualityFixture(t)}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer global-vm-key" {
			t.Error("wrong management key")
		}
		reply := func(v any) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "data": v})
		}
		switch {
		case r.URL.Path == "/api/panel/vm-quality/capabilities":
			reply(map[string]any{"version": 1, "read_only": true, "assignment_guard": true})
		case r.URL.Path == "/api/panel/vm-quality/results" && r.Method == "POST":
			n := f.reads.Add(1)
			if f.failRead.Load() {
				w.WriteHeader(503)
				_, _ = w.Write([]byte(`{"ok":false,"error":{"message":"offline"}}`))
				return
			}
			var input struct {
				Slots []vm2api.QualitySlotRef `json:"slots"`
			}
			_ = json.NewDecoder(r.Body).Decode(&input)
			snapshot := vm2api.QualitySnapshot{Version: 1, Revision: "vm-r1", ServerNow: time.Now().UTC()}
			for _, ref := range input.Slots {
				snapshot.Slots = append(snapshot.Slots, vm2api.QualitySlot{QualitySlotRef: ref, Identity: "identity-" + ref.ID, Current: true, Result: &vm2api.QualityResult{ID: 1, Revision: "vm-r1", Source: "manual", Status: "degraded", StartedAt: snapshot.ServerNow.Add(-time.Second), CheckedAt: snapshot.ServerNow, Reason: "答案不匹配", QuestionID: "candy", QuestionName: "糖果题"}})
			}
			f.muVM.Lock()
			f.readSizes = append(f.readSizes, len(input.Slots))
			hook := f.snapshotHook
			f.muVM.Unlock()
			if hook != nil {
				hook(&snapshot, n)
			}
			reply(snapshot)
		case strings.HasPrefix(r.URL.Path, "/api/panel/vms/") && r.Method == "DELETE":
			f.vmDeletes.Add(1)
			id := strings.TrimPrefix(r.URL.Path, "/api/panel/vms/")
			if r.Header.Get("X-Space-Assignment") != "assignment-"+id {
				t.Error("delete missing pinned assignment")
			}
			if f.kicks.Load() == 0 {
				t.Error("slot deleted before mother kick")
			}
			if f.failVMDelete.Load() {
				w.WriteHeader(502)
				_, _ = w.Write([]byte(`{"ok":false,"error":{"message":"docker unavailable"}}`))
				return
			}
			reply(map[string]any{})
		default:
			t.Errorf("unexpected request (must only read results or delete): %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(f.server.Close)
	var err error
	f.cfg = model.DefaultVM2APISettings()
	f.cfg.URL = f.server.URL
	f.cfg, err = f.st.SaveVM2APISettings(f.cfg, "global-vm-key")
	if err != nil {
		t.Fatal(err)
	}
	f.target, err = f.st.SaveDownstreamTarget(model.DownstreamTarget{Provider: "vm2api", VM2API: f.cfg, Secret: "global-vm-key"})
	if err != nil {
		t.Fatal(err)
	}
	f.push.Provider = "vm2api"
	f.push, err = f.st.SaveSub2Settings(f.push, "")
	if err != nil {
		t.Fatal(err)
	}
	f.q = model.DefaultQualitySettings()
	f.q.Provider = "vm2api"
	f.q.Enabled = true
	f.q.Action = "kick"
	f.q.Concurrency = 4
	f.save(t)
	return f
}
func (f *vmQualityFixture) addVM(t *testing.T, id int64) model.FreeAccountProfile {
	t.Helper()
	p := f.add(t, id)
	p, err := f.st.UpdateFreeAccount(p.ID, func(p *model.FreeAccountProfile) {
		past := time.Now().UTC().Add(-time.Minute)
		p.CreatedAt = past
		p.JoinedAt = &past
		p.PushedAt = &past
		p.Sub2AccountID = 0
		p.PushProvider = "vm2api"
		p.DownstreamConnectionID = f.target
		p.VM2SlotID = fmt.Sprintf("slot-%d", id)
		p.VM2AssignmentID = "assignment-" + p.VM2SlotID
		p.VM2ContainerRunning = true
		p.VM2Schedulable = true
	})
	if err != nil {
		t.Fatal(err)
	}
	return p
}
func (f *vmQualityFixture) run() { f.s.runRemoteQualityBatch(context.Background(), true, "") }

func TestVMQualityFreshnessAndIdentity(t *testing.T) {
	f := newVMQualityFixture(t)
	p := f.addVM(t, 1)
	c := vm2api.New(f.cfg, "global-vm-key")
	snapshot, err := c.QualityResults(context.Background(), []vm2api.QualitySlotRef{{ID: p.VM2SlotID, AssignmentID: p.VM2AssignmentID}})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"red", "green", "yellow", "wrong-slot", "wrong-assignment", "no-identity", "old-config", "missing-result", "expired", "future", "previous-cycle", "unknown", "malformed-source", "no-start"} {
		t.Run(name, func(t *testing.T) {
			snap := snapshot
			slot := snapshot.Slots[0]
			v := *slot.Result
			slot.Result = &v
			expected := false
			switch name {
			case "red", "green", "yellow":
				expected = true
				if name == "green" {
					v.Status = "normal"
				}
				if name == "yellow" {
					v.Status = "error"
				}
			case "wrong-slot":
				slot.ID = "other"
			case "wrong-assignment":
				slot.AssignmentID = "new-cycle"
			case "no-identity":
				slot.Identity = ""
			case "old-config":
				v.Revision = "old"
			case "missing-result":
				slot.Result = nil
			case "expired":
				snap.ServerNow = snap.ServerNow.Add(time.Hour)
			case "future":
				v.CheckedAt = snap.ServerNow.Add(time.Minute)
			case "previous-cycle":
				v.StartedAt = p.CreatedAt.Add(-time.Second)
			case "unknown":
				v.Status = "unknown"
			case "malformed-source":
				v.Source = ""
			case "no-start":
				v.StartedAt = time.Time{}
			}
			if got := vmQualityFresh(f.q, p, snap, slot); got != expected {
				t.Fatalf("fresh=%v want=%v", got, expected)
			}
		})
	}
}
func TestVMQualityRedForcesMotherKickThenDeletesBoundContainerOnce(t *testing.T) {
	f := newVMQualityFixture(t)
	p := f.addVM(t, 1)
	f.run()
	v := f.read(t, p.ID)
	if f.kicks.Load() != 1 || f.vmDeletes.Load() != 1 || v.RemoveMethod != "mother_kick" || v.RemoveStatus != "completed" || !v.DownstreamCleaned || v.VM2Stage != "deleted" || v.VM2ContainerRunning || !v.Quality.Excluded || !v.Quality.ActionStarted || v.Quality.ActionStatus != "completed" {
		t.Fatalf("incomplete action: kicks=%d deletes=%d profile=%+v", f.kicks.Load(), f.vmDeletes.Load(), v)
	}
	f.run()
	if f.kicks.Load() != 1 || f.vmDeletes.Load() != 1 {
		t.Fatal("duplicate result repeated action")
	}
	if f.reads.Load() != 2 {
		t.Fatalf("expected batch and locked recheck, got %d", f.reads.Load())
	}
	if err := f.s.flushAuditEvents(context.Background()); err != nil {
		t.Fatal(err)
	}
	history, err := f.st.QualityHistory(p.ID, 20)
	if err != nil || len(history) < 2 {
		t.Fatalf("missing result/action history: %d %v", len(history), err)
	}
}
func TestVMQualityNonRedAndUnreadableResultsNeverKick(t *testing.T) {
	for _, name := range []string{"normal", "error", "stale", "old-config", "missing", "mismatch", "offline", "changed-before-kick"} {
		t.Run(name, func(t *testing.T) {
			f := newVMQualityFixture(t)
			p := f.addVM(t, 1)
			if name == "offline" {
				f.failRead.Store(true)
			}
			f.snapshotHook = func(s *vm2api.QualitySnapshot, n int32) {
				v := &s.Slots[0]
				switch name {
				case "normal", "error":
					v.Result.Status = name
				case "stale":
					v.Result.CheckedAt = s.ServerNow.Add(-time.Hour)
				case "old-config":
					v.Current = false
				case "missing":
					v.Result = nil
				case "mismatch":
					v.AssignmentID = "replacement"
				case "changed-before-kick":
					if n > 1 {
						v.Result.Status = "normal"
						v.Result.ID = 2
					}
				}
			}
			f.run()
			v := f.read(t, p.ID)
			if f.kicks.Load() != 0 || f.vmDeletes.Load() != 0 || v.Quality.Excluded || v.Quality.ActionStarted {
				t.Fatal("non-actionable result caused removal")
			}
		})
	}
}
func TestVMQualityCleanupSurvivesDisabledQueriesAndSwitchedServer(t *testing.T) {
	f := newVMQualityFixture(t)
	p := f.addVM(t, 1)
	f.failVMDelete.Store(true)
	f.run()
	v := f.read(t, p.ID)
	if f.kicks.Load() != 1 || v.RemoveStatus != "cleanup_pending" || v.Quality.ActionStatus != "failed" {
		t.Fatal("failed cleanup not durable")
	}
	f.failVMDelete.Store(false)
	f.failRead.Store(true)
	f.q.Enabled = false
	f.save(t)
	cfg := f.cfg
	cfg.URL = "http://127.0.0.1:1"
	_, _ = f.st.SaveVM2APISettings(cfg, "different-key")
	f.push.Provider = "cpa"
	_, _ = f.st.SaveSub2Settings(f.push, "")
	f.s.Close()
	var err error
	f.s, err = New(f.st, nil)
	if err != nil {
		t.Fatal(err)
	}
	f.run()
	v = f.read(t, p.ID)
	if f.kicks.Load() != 1 || f.vmDeletes.Load() != 2 || v.RemoveStatus != "completed" || v.Quality.ActionStatus != "completed" || !v.Quality.Excluded {
		t.Fatalf("cleanup did not resume: %+v", v.Quality)
	}
	if f.reads.Load() != 2 {
		t.Fatal("cleanup unnecessarily read detection service")
	}
}
func TestVMQualityBatchAndSettingsIsolation(t *testing.T) {
	f := newVMQualityFixture(t)
	for i := int64(1); i <= 12; i++ {
		f.addVM(t, i)
	}
	f.snapshotHook = func(s *vm2api.QualitySnapshot, _ int32) {
		for i := range s.Slots {
			s.Slots[i].Result.Status = "normal"
		}
	}
	f.run()
	if len(f.readSizes) != 1 || f.readSizes[0] != 12 {
		t.Fatalf("not batched: %v", f.readSizes)
	}
	sub, err := f.st.QualitySettings()
	if err != nil || sub.Action != "groups" || sub.Provider == "vm2api" {
		t.Fatal("Sub2 settings overwritten")
	}
	vm, err := f.st.QualitySettings("vm2api")
	if err != nil || vm.Action != "kick" || vm.Revision == sub.Revision {
		t.Fatal("VM settings not independent")
	}
	status := f.s.qualityStatus()
	summary := status["summary"].(model.QualitySummary)
	if status["provider"] != "vm2api" || summary.Normal != 12 || summary.Total != 12 {
		t.Fatalf("incorrect VM stats: %+v", status)
	}
	for _, action := range []string{"groups", "restore"} {
		q := f.q
		q.Action = action
		if _, err = f.st.SaveQualitySettings(q); err == nil {
			t.Fatal("non-kick action accepted")
		}
	}
}
func TestVMQualitySettingsCapabilitiesAndProviderGuard(t *testing.T) {
	f := newVMQualityFixture(t)
	save := func(q model.QualitySettings) int {
		body, _ := json.Marshal(q)
		rec := httptest.NewRecorder()
		f.s.saveQualitySettings(rec, httptest.NewRequest("PUT", "/api/quality/settings", bytes.NewReader(body)))
		return rec.Code
	}
	if code := save(f.q); code != 200 {
		t.Fatalf("save HTTP %d", code)
	}
	q := f.q
	q.Provider = "sub2"
	if save(q) != 409 {
		t.Fatal("stale provider form accepted")
	}
	q = f.q
	q.Action = "groups"
	if save(q) != 400 {
		t.Fatal("VM group action accepted")
	}
	p := f.addVM(t, 1)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/", nil)
	req.SetPathValue("id", p.ID)
	f.s.restoreQualityAccount(rec, req)
	if rec.Code != 409 {
		t.Fatal("VM manual restore allowed")
	}
	cfg := f.cfg
	cfg.URL = "http://127.0.0.1:1"
	_, _ = f.st.SaveVM2APISettings(cfg, "")
	f.run()
	if f.reads.Load() != 0 || f.kicks.Load() != 0 {
		t.Fatal("old account queried against new server")
	}
}

func TestVMQualityObserveOnlyNeverExcludesAndCanRecover(t *testing.T) {
	f := newVMQualityFixture(t)
	p := f.addVM(t, 1)
	f.q.Action = "observe"
	f.save(t)
	f.run()
	f.run()
	v := f.read(t, p.ID)
	if v.Quality.Status != "degraded" || !v.Quality.Degraded || v.Quality.Excluded || v.Quality.ActionStarted || v.Quality.Action != "" || v.RemoveStatus != "pending" || !v.VM2ContainerRunning || !v.VM2Schedulable || f.kicks.Load() != 0 || f.vmDeletes.Load() != 0 {
		t.Fatalf("observe mode mutated lifecycle: %+v", v)
	}
	if err := f.s.flushAuditEvents(context.Background()); err != nil {
		t.Fatal(err)
	}
	history, _ := f.st.QualityHistory(p.ID, 20)
	if len(history) != 1 {
		t.Fatalf("duplicate sample history: %d", len(history))
	}
	f.snapshotHook = func(s *vm2api.QualitySnapshot, _ int32) {
		s.Slots[0].Result.Status = "normal"
		s.Slots[0].Result.ID = 2
	}
	f.run()
	v = f.read(t, p.ID)
	if v.Quality.Degraded || v.Quality.Status != "normal" || f.kicks.Load() != 0 {
		t.Fatal("latest normal result not reflected")
	}
	f.snapshotHook = nil
	f.q.Action = "kick"
	f.save(t)
	f.run()
	if f.kicks.Load() != 1 || f.vmDeletes.Load() != 1 {
		t.Fatal("explicitly selected kick did not process red result")
	}
}
func TestVMQualitySwitchToObserveBeforeKickCancelsAction(t *testing.T) {
	f := newVMQualityFixture(t)
	p := f.addVM(t, 1)
	f.snapshotHook = func(_ *vm2api.QualitySnapshot, n int32) {
		if n == 2 {
			q := f.q
			q.Action = "observe"
			if _, err := f.st.SaveQualitySettings(q); err != nil {
				t.Error(err)
			}
		}
	}
	f.run()
	v := f.read(t, p.ID)
	if v.Quality.ActionStarted || v.Quality.Excluded || f.kicks.Load() != 0 || f.vmDeletes.Load() != 0 {
		t.Fatal("switch to observe failed to cancel pending kick")
	}
}
func TestVMQualityConcurrentPollsCannotRepeatKick(t *testing.T) {
	f := newVMQualityFixture(t)
	p := f.addVM(t, 1)
	c := vm2api.New(f.cfg, "global-vm-key")
	snapshot, err := c.QualityResults(context.Background(), []vm2api.QualitySlotRef{{ID: p.VM2SlotID, AssignmentID: p.VM2AssignmentID}})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			f.s.applyVMQuality(context.Background(), p, f.q, f.cfg, snapshot, snapshot.Slots[0], nil)
		}()
	}
	wg.Wait()
	if f.kicks.Load() != 1 || f.vmDeletes.Load() != 1 {
		t.Fatalf("repeated action: %d %d", f.kicks.Load(), f.vmDeletes.Load())
	}
}
