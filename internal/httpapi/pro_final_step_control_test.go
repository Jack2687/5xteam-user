package httpapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"chatgpt-space-merge/internal/model"
	"chatgpt-space-merge/internal/refundmerchant"
)

func TestProAutoFinalStepPauseAllModesKeepsQuotaAndRechecksResume(t *testing.T) {
	for _, action := range []string{"merge", "deactivate", "merchant", "merge_v2"} {
		t.Run(action, func(t *testing.T) {
			f := newProExecutionFixture(t)
			f.s.stopProAutomations()
			seedDeleteTail(t, f)
			cfg, _, _, _ := f.s.store.ProSettings()
			cfg.Provider = "sub2"
			cfg.AutoMergeEnabled, cfg.AutoDeleteEnabled = action == "merge", action == "deactivate"
			cfg.AutoMerchantEnabled, cfg.AutoMergeV2Enabled = action == "merchant", action == "merge_v2"
			if _, err := f.s.store.SaveProSettings(cfg, "", ""); err != nil {
				t.Fatal(err)
			}
			queries, usage := 0, 95.0
			f.s.proAutoHooks = &proAutomationHooks{Quota: func(_ context.Context, email string, _ bool) (model.MailAccountProfile, error) {
				queries++
				return f.s.store.UpdateProAccount(email, func(p *model.MailAccountProfile) { p.Quota7D = &model.FreeQuotaWindow{UsedPercent: usage} })
			}}
			setAutoDeletePaused(t, f.s, true)
			for range 2 {
				if err := f.s.runProAutomationTail(t.Context(), f.email); err != nil {
					t.Fatal(err)
				}
			}
			p, _, _ := f.s.store.MailAccountCredential(f.email)
			if queries != 2 || p.ProAuto.Status != "waiting_quota" || p.ProAuto.NextCheckAt == nil || p.DispositionStarted() || len(f.calls) != 0 {
				t.Fatalf("paused %s did not keep quota only: %+v", action, p.ProAuto)
			}
			if _, err := f.s.startProDispositionJob(f.email, nil, nil); !errors.Is(err, errProAutoFinalStepPaused) {
				t.Fatal("background admission", err)
			}
			if _, err := f.s.performProDisposition(t.Context(), f.email); !errors.Is(err, errProAutoFinalStepPaused) {
				t.Fatal("direct automatic admission", err)
			}
			setAutoDeletePaused(t, f.s, false)
			if queries != 2 || len(f.calls) != 0 {
				t.Fatal("resume directly executed work")
			}
			usage = 10
			if err := f.s.runProAutomationTail(t.Context(), f.email); err != nil {
				t.Fatal(err)
			}
			p, _, _ = f.s.store.MailAccountCredential(f.email)
			if queries != 3 || p.ProAuto.Status != "waiting_quota" || p.DispositionStarted() || len(f.calls) != 0 {
				t.Fatal("resume ignored current quota")
			}
		})
	}
}

func TestProAutoFinalStepPauseCancelsQueuedModesAcrossRestart(t *testing.T) {
	for _, action := range []string{"merge", "deactivate", "merchant", "merge_v2"} {
		t.Run(action, func(t *testing.T) {
			f := newProExecutionFixture(t)
			f.s.stopProAutomations()
			value, _ := f.s.proLocks.LoadOrStore("account:"+f.email, &sync.Mutex{})
			lock := value.(*sync.Mutex)
			lock.Lock()
			if _, err := f.s.startProEndJob(f.email, nil, nil, action); err != nil {
				lock.Unlock()
				t.Fatal(err)
			}
			setAutoDeletePaused(t, f.s, true)
			setAutoDeletePaused(t, f.s, false)
			lock.Unlock()
			f.s.proMergeWG.Wait()
			p, _, _ := f.s.store.MailAccountCredential(f.email)
			if len(f.calls) != 0 || p.DispositionStarted() || p.ProWorkflowRunning {
				t.Fatal("cancelled queued job was revived")
			}
		})
	}
}

func TestProAutoFinalStepPauseMergeV2KeepsAcceptanceAndResumesOnlyRemainingSteps(t *testing.T) {
	f := mergeV2Fixture(t)
	logins := 0
	f.s.proMergeV2Login = func(ctx context.Context, _ string) (map[string]any, error) {
		logins++
		result, err := mockMergeV2Submit(ctx)
		setAutoDeletePaused(t, f.s, true)
		return result, err
	}
	if err := f.s.runProAutomationTail(t.Context(), f.email); err != nil {
		t.Fatal(err)
	}
	p, _, _ := f.s.store.MailAccountCredential(f.email)
	if !p.MergeV2Accepted() || p.MergeV2Completed() || p.ProMergeV2.AutoResumeAt == nil || p.ProMergeV2.Status != "interrupted" || f.calls["remove"] != 0 {
		t.Fatalf("lost accepted checkpoint: %+v", p.ProMergeV2)
	}
	f.s.tickProPausedMergeV2(t.Context())
	f.s.proMergeWG.Wait()
	if f.calls["remove"] != 0 {
		t.Fatal("paused continuation ran")
	}
	setAutoDeletePaused(t, f.s, false)
	if f.calls["remove"] != 0 {
		t.Fatal("resume button ran continuation")
	}
	f.s.tickProPausedMergeV2(t.Context())
	f.s.proMergeWG.Wait()
	if f.calls["remove"] != 0 {
		t.Fatal("resumed before timer")
	}
	_, err := f.s.store.UpdateProAccount(f.email, func(p *model.MailAccountProfile) {
		past := time.Now().Add(-time.Second)
		p.ProMergeV2.AutoResumeAt = &past
	})
	if err != nil {
		t.Fatal(err)
	}
	f.s.tickProPausedMergeV2(t.Context())
	f.s.proMergeWG.Wait()
	p, _, _ = f.s.store.MailAccountCredential(f.email)
	if !p.MergeV2Completed() || logins != 1 || f.calls["invite"] != 1 || f.calls["remove"] != 1 {
		t.Fatal("continuation repeated submission or did not finish", p.ProMergeV2, f.calls)
	}
}

func TestProAutoFinalStepPauseMergeV2UnknownNeverAutoResubmits(t *testing.T) {
	f := mergeV2Fixture(t)
	f.s.proMergeV2Login = func(ctx context.Context, _ string) (map[string]any, error) {
		o := ctx.Value(mergeV2LoginKey{}).(*mergeV2LoginOptions)
		for _, method := range []string{"merge_v2_ready", "merge_v2_submitting"} {
			if _, err := o.Call(method, map[string]any{"team_id": o.TeamID, "user_id": "pro-user"}); err != nil {
				return nil, err
			}
		}
		setAutoDeletePaused(t, f.s, true)
		return nil, ctx.Err()
	}
	if err := f.s.runProAutomationTail(t.Context(), f.email); err != nil {
		t.Fatal(err)
	}
	p, _, _ := f.s.store.MailAccountCredential(f.email)
	if p.ProMergeV2.Status != "unknown" || p.ProMergeV2.SubmittedAt == nil || p.ProMergeV2.AutoResumeAt != nil {
		t.Fatal("uncertain request became auto resumable")
	}
	setAutoDeletePaused(t, f.s, false)
	f.s.tickProPausedMergeV2(t.Context())
	f.s.proMergeWG.Wait()
	if f.calls["remove"] != 0 {
		t.Fatal("removed member with unknown result")
	}
}

func TestProAutoFinalStepMergeV2ResumeFailureDoesNotRetryEveryTick(t *testing.T) {
	f := mergeV2Fixture(t)
	_, err := f.s.store.UpdateProAccount(f.email, func(p *model.MailAccountProfile) {
		past := time.Now().Add(-time.Second)
		p.ProMergeV2 = &model.ProMergeV2{Status: "interrupted", Stage: "invite", AdminID: "missing-admin", TeamID: "original-team", AutoResumeAt: &past}
	})
	if err != nil {
		t.Fatal(err)
	}
	f.s.tickProPausedMergeV2(t.Context())
	f.s.proMergeWG.Wait()
	p, _, _ := f.s.store.MailAccountCredential(f.email)
	if p.ProMergeV2.AutoResumeAt != nil || p.ProLastError == "" {
		t.Fatal("failed continuation remains auto scheduled")
	}
	items, err := f.s.store.DuePausedProMergeV2(time.Now().Add(time.Hour))
	if err != nil || len(items) != 0 {
		t.Fatal("ordinary failure would auto retry", err)
	}
}

func TestProAutoFinalStepPauseMerchantRetainsAcceptedRequest(t *testing.T) {
	f := merchantFixture(t)
	var posts, gets atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			posts.Add(1)
			w.WriteHeader(202)
			fmt.Fprintf(w, `{"items":[{"id":"job","email":%q,"status":"queued"}]}`, f.email)
			return
		}
		gets.Add(1)
		fmt.Fprintf(w, `{"id":"job","email":%q,"status":"succeeded"}`, f.email)
	}))
	defer srv.Close()
	f.s.proMerchantClient = &refundmerchant.Client{BaseURL: srv.URL}
	if err := f.s.runProAutomationTail(t.Context(), f.email); err != nil {
		t.Fatal(err)
	}
	setAutoDeletePaused(t, f.s, true)
	_, err := f.s.store.UpdateProAccount(f.email, func(p *model.MailAccountProfile) {
		past := time.Now().Add(-time.Second)
		p.ProMerchant.NextCheckAt = &past
	})
	if err != nil {
		t.Fatal(err)
	}
	f.s.tickProMerchants(t.Context())
	f.s.proMergeWG.Wait()
	if posts.Load() != 1 || gets.Load() != 0 {
		t.Fatal("paused merchant contacted upstream")
	}
	setAutoDeletePaused(t, f.s, false)
	if posts.Load() != 1 || gets.Load() != 0 {
		t.Fatal("button contacted merchant")
	}
	f.s.tickProMerchants(t.Context())
	f.s.proMergeWG.Wait()
	p, _, _ := f.s.store.MailAccountCredential(f.email)
	if posts.Load() != 1 || gets.Load() != 1 || !p.MerchantCompleted() {
		t.Fatal("merchant resume did not query original request")
	}
}

func TestProAutoFinalStepPauseRunningMergePreservesUncertainTransfer(t *testing.T) {
	f := newProExecutionFixture(t)
	f.s.stopProAutomations()
	seedDeleteTail(t, f)
	f.entered, f.release = make(chan struct{}), make(chan struct{})
	defer close(f.release)
	if _, err := f.s.startProEndJob(f.email, nil, nil, "merge"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-f.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("transfer not reached")
	}
	setAutoDeletePaused(t, f.s, true)
	f.s.proMergeWG.Wait()
	p, _, _ := f.s.store.MailAccountCredential(f.email)
	f.mu.Lock()
	removes := f.calls["remove"]
	f.mu.Unlock()
	if p.TransferStatus != "unknown" || removes != 0 || p.ProAuto.Status == "completed" || p.SpaceMergedOnce {
		t.Fatal("pause lost uncertain result or continued removal", p.TransferStatus, p.ProAuto)
	}
	setAutoDeletePaused(t, f.s, false)
	if _, err := f.s.startProEndJob(f.email, nil, nil, "merge"); !errors.Is(err, errProTransferUncertain) {
		t.Fatal("resume allowed unknown transfer", err)
	}
}

func TestProAutoFinalStepPauseKeepsQualityObservationForAllModes(t *testing.T) {
	for _, action := range []string{"merge", "deactivate", "merchant", "merge_v2"} {
		t.Run(action, func(t *testing.T) {
			f := newProQualityFixture(t, "merge")
			cfg, _, _, _ := f.s.store.ProSettings()
			cfg.AutoMergeEnabled, cfg.AutoDeleteEnabled = action == "merge", action == "deactivate"
			cfg.AutoMerchantEnabled, cfg.AutoMergeV2Enabled = action == "merchant", action == "merge_v2"
			if _, err := f.s.store.SaveProSettings(cfg, "", ""); err != nil {
				t.Fatal(err)
			}
			setAutoDeletePaused(t, f.s, true)
			f.run(t)
			f.run(t)
			p := f.profile(t)
			if p.ProQuality.Status != "degraded" || p.ProQuality.MergeAttempts != 0 || p.DispositionStarted() || len(f.calls) != 0 {
				t.Fatal("pause consumed quality attempt or stopped observation", p.ProQuality)
			}
			f.setMode("normal")
			setAutoDeletePaused(t, f.s, false)
			f.run(t)
			f.s.proMergeWG.Wait()
			if len(f.calls) != 0 || f.profile(t).DispositionStarted() {
				t.Fatal("recovered account still ended service")
			}
		})
	}
}
