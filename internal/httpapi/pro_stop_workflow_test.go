package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"chatgpt-space-merge/internal/model"
)

func TestProStopIndependentDeletion(t *testing.T) {
	for _, phase := range []string{"login", "queued", "submitted"} {
		t.Run(phase, func(t *testing.T) {
			f := newProGroupFixture(t)
			enableDeletion(t, f.proExecutionFixture)
			entered := make(chan struct{})
			var posts atomic.Int32
			f.s.proDeleteHooks = &proDeactivationHooks{
				Login: func(ctx context.Context, email string) (map[string]any, error) {
					if phase == "login" {
						close(entered)
						<-ctx.Done()
						return nil, ctx.Err()
					}
					return deletionLoginFixture(ctx, email)
				},
				Protocol: func(ctx context.Context, input map[string]any, call func(string, map[string]any) (any, error)) (map[string]any, error) {
					if input["check_only"] == true {
						return map[string]any{"status": "confirmed"}, nil
					}
					for _, stage := range []string{"identity", "submitting", "accepted"} {
						if _, err := call(stage, map[string]any{"account_id": "personal", "user_id": "pro-user"}); err != nil {
							return nil, err
						}
						if stage == "submitting" {
							posts.Add(1)
						}
					}
					close(entered)
					<-ctx.Done()
					return nil, ctx.Err()
				},
			}
			if phase == "queued" {
				f.s.proMergeMu.Lock()
				f.s.proMergeRunning = 100
				f.s.proMergeMu.Unlock()
			}
			if r := f.request(f.s.deactivateProAccount, "POST", "/deactivate", ""); r.Code != 202 {
				t.Fatal(r.Code, r.Body.String())
			}
			if phase != "queued" {
				select {
				case <-entered:
				case <-time.After(5 * time.Second):
					t.Fatal("worker did not reach stop point")
				}
			}
			// The UI must expose an independent task even without a ProAuto flow ID.
			r := f.request(f.s.getProAutomation, "GET", "/auto-pro", "")
			var response struct {
				Data struct {
					Running bool `json:"pro_workflow_running"`
				}
			}
			if json.Unmarshal(r.Body.Bytes(), &response) != nil || !response.Data.Running {
				t.Fatal("independent workflow not exposed", r.Body.String())
			}
			if r = f.request(f.s.stopProAutomation, "POST", "/auto-pro/stop", ""); r.Code != 202 {
				t.Fatal(r.Code, r.Body.String())
			}
			f.waitMerge(t)
			if phase == "queued" {
				f.s.proMergeMu.Lock()
				f.s.proMergeRunning = 0
				f.s.proMergeMu.Unlock()
			}
			p, _, _ := f.s.store.MailAccountCredential(f.email)
			expectedPosts := int32(0)
			if phase == "submitted" {
				expectedPosts = 1
			}
			if posts.Load() != expectedPosts || !p.ProAuto.StopRequested || p.ProAuto.Status != "stopped" || p.ProWorkflowRunning {
				t.Fatalf("stop failed: posts=%d auto=%+v", posts.Load(), p.ProAuto)
			}
			if phase == "submitted" && (!p.DeletionAccepted() || p.ProDeactivation.NextCheckAt != nil || f.writes.Load() != 0) {
				t.Fatal("lost submission or cleanup continued after stop")
			}
			// A late callback and a settings save cannot re-arm stopped work.
			_ = f.s.autoProUpdate(f.email, "deactivate", "interrupted", "context canceled")
			cfg, _, _, _ := f.s.store.ProSettings()
			cfg.DeletionCheckIntervalSeconds = 11
			if _, err := f.s.store.SaveProSettings(cfg, "", ""); err != nil {
				t.Fatal(err)
			}
			if err := f.s.store.RecoverProWorkflows(); err != nil {
				t.Fatal(err)
			}
			for _, action := range []string{"automatic", "deactivate", "merge"} {
				if _, err := f.s.startProEndJob(f.email, nil, nil, action); err == nil {
					t.Fatal("stale job admitted", action)
				}
			}
			due, err := f.s.store.DueProDeactivations(time.Now().Add(time.Hour))
			p, _, _ = f.s.store.MailAccountCredential(f.email)
			if err != nil || len(due) > 0 || p.ProAuto.Status != "stopped" || (p.ProDeactivation != nil && p.ProDeactivation.NextCheckAt != nil) {
				t.Fatal("stop revived", err)
			}
			// Explicit resume after acceptance only checks; it never repeats DELETE.
			if phase == "submitted" {
				if r = f.request(f.s.deactivateProAccount, "POST", "/deactivate", ""); r.Code != 202 {
					t.Fatal(r.Body.String())
				}
				f.waitMerge(t)
				p, _, _ = f.s.store.MailAccountCredential(f.email)
				if !p.DeletionConfirmed() || p.ProAuto.StopRequested || posts.Load() != 1 {
					t.Fatal("explicit resume lost checkpoint")
				}
			}
		})
	}
}

func TestProStopCancelsBothRegistriesOnlyForAccount(t *testing.T) {
	f := newProExecutionFixture(t)
	auto, cancelAuto := context.WithCancel(context.Background())
	defer cancelAuto()
	merge, cancelMerge := context.WithCancel(context.Background())
	defer cancelMerge()
	other, cancelOther := context.WithCancel(context.Background())
	defer cancelOther()
	f.s.proAutoMu.Lock()
	f.s.proAutoJobs = map[string]context.CancelFunc{f.email: cancelAuto, "other@example.test": cancelOther}
	f.s.proAutoMu.Unlock()
	f.s.proMergeMu.Lock()
	f.s.proMergeJobs = map[string]context.CancelFunc{f.email: cancelMerge, "other@example.test": cancelOther}
	f.s.proMergeMu.Unlock()
	if r := f.request(f.s.stopProAutomation, "POST", "/stop", ""); r.Code != 202 {
		t.Fatal(r.Code)
	}
	if !errors.Is(auto.Err(), context.Canceled) || !errors.Is(merge.Err(), context.Canceled) || other.Err() != nil {
		t.Fatal("wrong cancellation scope")
	}
	now := time.Now()
	p, err := f.s.store.UpdateProAccount(f.email, func(p *model.MailAccountProfile) {
		p.ProAuto.Status = "waiting_quota"
		p.ProAuto.NextCheckAt = &now
		p.ProAuto.RetryAt = &now
		p.ProForceMergeNextAt = &now
	})
	if err != nil || p.ProAuto.Status != "stopped" || p.ProAuto.NextCheckAt != nil || p.ProAuto.RetryAt != nil || p.ProForceMergeNextAt != nil {
		t.Fatal("late result revived stop", err)
	}
}

func TestProPinnedTargetDoesNotInheritOtherSiteGroups(t *testing.T) {
	f := newProExecutionFixture(t)
	cfg, _, _, _ := f.s.store.ProSettings()
	cfg.Provider = "sub2"
	cfg.Sub2.URL = "https://new.example.test"
	cfg.Sub2.Email = "admin@example.test"
	cfg.PostMergeGroupIDs = []int64{8}
	if _, err := f.s.store.SaveProSettings(cfg, "password", ""); err != nil {
		t.Fatal(err)
	}
	for _, same := range []bool{false, true} {
		pinned := cfg.Sub2
		if !same {
			pinned.URL = "https://old.example.test"
			pinned.AuthMode = "siwc"
		}
		id, err := f.s.store.SaveDownstreamTarget(model.DownstreamTarget{Scope: "pro", Provider: "sub2", Sub2: pinned, Secret: "old-secret"})
		if err != nil {
			t.Fatal(err)
		}
		got, secret, _, err := f.s.proAccountPushSettings(model.MailAccountProfile{ProRestockTargetID: id})
		if err != nil || got.Sub2.URL != pinned.URL || secret != "old-secret" {
			t.Fatal("lost pinned credentials", err)
		}
		if same && len(got.PostMergeGroupIDs) != 1 || !same && len(got.PostMergeGroupIDs) != 0 {
			t.Fatal("groups crossed downstream sites")
		}
	}
}
