package httpapi

import (
	"context"
	"errors"
	"fmt"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"chatgpt-space-merge/internal/model"
)

func setAutoDeletePaused(t *testing.T, s *Server, paused bool) {
	t.Helper()
	w := httptest.NewRecorder()
	s.setProAutoFinalStepControl(w, httptest.NewRequest("PUT", "/api/pro-settings/auto-delete-control", strings.NewReader(fmt.Sprintf(`{"paused":%t}`, paused))))
	if w.Code != 200 {
		t.Fatalf("control: %d %s", w.Code, w.Body.String())
	}
}

func seedDeleteTail(t *testing.T, f *proExecutionFixture) {
	t.Helper()
	_, err := f.s.store.UpdateProAccount(f.email, func(p *model.MailAccountProfile) {
		p.ProAuto = model.ProAutoState{ID: "delete-tail", Status: "waiting_quota", Stage: "quota", QuotaUsedThreshold: 80, QuotaIntervalSeconds: 60, Steps: map[string]string{"login": "completed", "recharge": "completed", "oauth": "completed", "push": "completed", "quota": "waiting"}}
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestProAutoDeletePauseKeepsQuotaAndResumeChecksRules(t *testing.T) {
	f := newProGroupFixture(t)
	enableDeletion(t, f.proExecutionFixture)
	seedDeleteTail(t, f.proExecutionFixture)
	var queries, posts atomic.Int32
	usage := 95.0
	mode := "pending"
	f.s.proAutoHooks = &proAutomationHooks{Quota: func(_ context.Context, email string, _ bool) (model.MailAccountProfile, error) {
		queries.Add(1)
		return f.s.store.UpdateProAccount(email, func(p *model.MailAccountProfile) { p.Quota7D = &model.FreeQuotaWindow{UsedPercent: usage} })
	}}
	f.s.proDeleteHooks = &proDeactivationHooks{Login: deletionLoginFixture, Protocol: deletionProtocolFixture(t, &posts, &mode)}
	setAutoDeletePaused(t, f.s, true)
	for range 2 {
		if err := f.s.runProAutomationTail(t.Context(), f.email); err != nil {
			t.Fatal(err)
		}
	}
	p, _, _ := f.s.store.MailAccountCredential(f.email)
	if queries.Load() != 2 || posts.Load() != 0 || p.ProAuto.Status != "waiting_quota" || p.ProAuto.StopRequested || p.ProAuto.NextCheckAt == nil || p.ProAuto.NextCheckAt.Before(time.Now().Add(50*time.Second)) {
		t.Fatalf("pause halted quota or deleted: %+v", p.ProAuto)
	}
	setAutoDeletePaused(t, f.s, false)
	if queries.Load() != 2 || posts.Load() != 0 {
		t.Fatal("resume immediately ran upstream")
	}
	usage = 20
	if err := f.s.runProAutomationTail(t.Context(), f.email); err != nil {
		t.Fatal(err)
	}
	if posts.Load() != 0 {
		t.Fatal("resume bypassed current quota threshold")
	}
	usage = 95
	if err := f.s.runProAutomationTail(t.Context(), f.email); err != nil {
		t.Fatal(err)
	}
	if posts.Load() != 1 {
		t.Fatal("eligible deletion did not resume")
	}
}

func TestProAutoDeletePauseCancelsOnlyDeletion(t *testing.T) {
	f := newProExecutionFixture(t)
	enableDeletion(t, f)
	seedDeleteTail(t, f)
	_, err := f.s.store.UpdateProAccount(f.email, func(p *model.MailAccountProfile) {
		start, enabled := time.Now().Add(-2*time.Hour), true
		p.ProPushStartedAt, p.ProPushTimeoutEnabled, p.ProPushTimeoutMinutes = &start, &enabled, 1
	})
	if err != nil {
		t.Fatal(err)
	}
	entered := make(chan struct{})
	var posts atomic.Int32
	f.s.proDeleteHooks = &proDeactivationHooks{Login: func(ctx context.Context, _ string) (map[string]any, error) {
		close(entered)
		<-ctx.Done()
		return nil, ctx.Err()
	}, Protocol: func(context.Context, map[string]any, func(string, map[string]any) (any, error)) (map[string]any, error) {
		posts.Add(1)
		return nil, nil
	}}
	parent, cancel := context.WithCancel(t.Context())
	defer cancel()
	f.s.proAutoHooks = &proAutomationHooks{Quota: func(_ context.Context, email string, _ bool) (model.MailAccountProfile, error) {
		return f.s.store.UpdateProAccount(email, func(p *model.MailAccountProfile) { p.Quota7D = &model.FreeQuotaWindow{UsedPercent: 95} })
	}}
	done := make(chan error, 1)
	go func() { done <- f.s.runProAutomationTail(parent, f.email) }()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("login not started")
	}
	setAutoDeletePaused(t, f.s, true)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("deletion not cancelled")
	}
	if parent.Err() != nil || posts.Load() != 0 {
		t.Fatal("pause cancelled parent or submitted deletion")
	}
	p, _, _ := f.s.store.MailAccountCredential(f.email)
	if p.ProAuto.Status != "waiting_quota" || p.ProAuto.StopRequested || p.ProAuto.NextCheckAt == nil || p.ProDeactivation.Status != "interrupted" {
		t.Fatalf("lost quota continuation: %+v", p.ProAuto)
	}
	if p.ProForceMergeAttempts != 0 || p.ProForceMergeNextAt != nil {
		t.Fatal("pause consumed a timeout retry")
	}
}

func TestProAutoDeletePauseStillObservesQualityAndRechecksOnResume(t *testing.T) {
	f := newProQualityFixture(t, "merge")
	enableDeletion(t, f.proExecutionFixture)
	var posts atomic.Int32
	mode := "pending"
	f.s.proDeleteHooks = &proDeactivationHooks{Login: deletionLoginFixture, Protocol: deletionProtocolFixture(t, &posts, &mode)}
	setAutoDeletePaused(t, f.s, true)
	f.run(t)
	f.run(t)
	p := f.profile(t)
	if p.ProQuality.Status != "degraded" || p.ProQuality.MergeAttempts != 0 || posts.Load() != 0 {
		t.Fatalf("pause stopped observation or consumed attempts: %+v", p.ProQuality)
	}
	f.setMode("normal")
	setAutoDeletePaused(t, f.s, false)
	f.run(t)
	f.s.proMergeWG.Wait()
	if posts.Load() != 0 {
		t.Fatal("resume deleted an account whose quality recovered")
	}
	f.setMode("bad")
	f.run(t)
	f.s.proMergeWG.Wait()
	if posts.Load() != 1 {
		t.Fatal("new eligible quality result did not resume deletion", f.profile(t).ProQuality)
	}
}

func TestProAutoDeletePauseQueuedWorkAndManualOverride(t *testing.T) {
	f := newProGroupFixture(t)
	enableDeletion(t, f.proExecutionFixture)
	var posts atomic.Int32
	mode := "pending"
	f.s.proDeleteHooks = &proDeactivationHooks{Login: deletionLoginFixture, Protocol: deletionProtocolFixture(t, &posts, &mode)}
	v, _ := f.s.proLocks.LoadOrStore("account:"+f.email, &sync.Mutex{})
	lock := v.(*sync.Mutex)
	lock.Lock()
	if _, err := f.s.startProDispositionJob(f.email, nil, nil); err != nil {
		lock.Unlock()
		t.Fatal(err)
	}
	setAutoDeletePaused(t, f.s, true)
	setAutoDeletePaused(t, f.s, false) // An old queued job must stay cancelled.
	lock.Unlock()
	f.s.proMergeWG.Wait()
	if posts.Load() != 0 {
		t.Fatal("queued deletion survived stop/start")
	}
	setAutoDeletePaused(t, f.s, true)
	if _, err := f.s.startProDispositionJob(f.email, nil, nil); !errors.Is(err, errProAutoFinalStepPaused) {
		t.Fatal("automatic admission was not blocked", err)
	}
	if _, err := f.s.startProEndJob(f.email, nil, nil, "manual_deactivate"); err != nil {
		t.Fatal(err)
	}
	f.s.proMergeWG.Wait()
	if posts.Load() != 1 {
		t.Fatal("explicit manual deletion was blocked")
	}
	p, _, _ := f.s.store.MailAccountCredential(f.email)
	submitted := *p.ProDeactivation.SubmittedAt
	f.s.tickProDeactivations(t.Context())
	f.s.proMergeWG.Wait()
	p, _, _ = f.s.store.MailAccountCredential(f.email)
	if !p.ProDeactivation.SubmittedAt.Equal(submitted) || posts.Load() != 1 {
		t.Fatal("pause erased or replayed submission")
	}
}

func TestProAutoFinalStepPauseBlocksMergeTimeoutButAllowsExplicitManual(t *testing.T) {
	f := newProExecutionFixture(t)
	f.s.stopProAutomations()
	setAutoDeletePaused(t, f.s, true)
	p, _, _ := f.s.store.MailAccountCredential(f.email)
	cfg, _, _, _ := f.s.store.ProSettings()
	start := time.Now().Add(-2 * time.Hour)
	p.ProPushStartedAt = &start
	on := true
	p.ProPushTimeoutEnabled = &on
	p.ProPushTimeoutMinutes = 1
	cfg.AutoDeleteEnabled = true
	if f.s.forcedMergeReason(p, cfg, time.Now()) != "" || forceMergeDue(p, cfg, time.Now()) {
		t.Fatal("paused deletion still forced timeout")
	}
	cfg.AutoDeleteEnabled = false
	if forceMergeDue(p, cfg, time.Now()) {
		t.Fatal("paused final step still forced merge timeout")
	}
	if _, err := f.s.startProEndJob(f.email, nil, nil, "merge"); !errors.Is(err, errProAutoFinalStepPaused) {
		t.Fatal("automatic merge was not blocked", err)
	}
	if _, err := f.s.startProEndJob(f.email, nil, nil, "manual_merge"); err != nil {
		t.Fatal(err)
	}
	f.s.proMergeWG.Wait()
	p, _, _ = f.s.store.MailAccountCredential(f.email)
	if !p.SpaceMergedOnce {
		t.Fatal("pause blocked normal merge", p.ProLastError)
	}
}
