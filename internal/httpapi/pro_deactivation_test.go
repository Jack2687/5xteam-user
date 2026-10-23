package httpapi

import (
	"chatgpt-space-merge/internal/ejh"
	"chatgpt-space-merge/internal/gptpay"
	"chatgpt-space-merge/internal/restock"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"chatgpt-space-merge/internal/model"
)

func deletionProtocolFixture(t *testing.T, posts *atomic.Int32, mode *string) func(context.Context, map[string]any, func(string, map[string]any) (any, error)) (map[string]any, error) {
	t.Helper()
	return func(ctx context.Context, input map[string]any, call func(string, map[string]any) (any, error)) (map[string]any, error) {
		if input["check_only"] == true {
			if input["account_id"] != "personal" {
				t.Error("lost original deletion identity")
			}
			return map[string]any{"status": *mode, "message": "fixture confirmation"}, nil
		}
		for _, stage := range []string{"identity", "stage", "submitting"} {
			if _, err := call(stage, map[string]any{"account_id": "personal", "user_id": "pro-user"}); err != nil {
				return nil, err
			}
		}
		posts.Add(1)
		if *mode == "unknown" {
			return nil, errors.New("fixture response lost")
		}
		if _, err := call("accepted", nil); err != nil {
			return nil, err
		}
		return map[string]any{"status": "queued", "message": "fixture queued"}, nil
	}
}

func deletionLoginFixture(context.Context, string) (map[string]any, error) {
	return map[string]any{"success": true, "access_token": "fresh-deletion-at", "session_token": "fresh-deletion-cookie"}, nil
}

func enableDeletion(t *testing.T, f *proExecutionFixture) {
	t.Helper()
	f.s.stopProAutomations()
	v, _, _, err := f.s.store.ProSettings()
	if err != nil {
		t.Fatal(err)
	}
	v.AutoDeleteEnabled, v.AutoMergeEnabled, v.TargetAdminID = true, false, ""
	if _, err = f.s.store.SaveProSettings(v, "", ""); err != nil {
		t.Fatal(err)
	}
}

func TestProDeletionAcceptedGroupsAndNoReplay(t *testing.T) {
	for _, failGroups := range []bool{false, true} {
		t.Run(fmt.Sprint(failGroups), func(t *testing.T) {
			f := newProGroupFixture(t)
			enableDeletion(t, f.proExecutionFixture)
			var posts atomic.Int32
			mode := "pending"
			f.s.proDeleteHooks = &proDeactivationHooks{Login: deletionLoginFixture, Protocol: deletionProtocolFixture(t, &posts, &mode)}
			f.fail.Store(failGroups)
			p, err := f.s.performProDisposition(t.Context(), f.email)
			if (err != nil) != failGroups || !p.DeletionAccepted() || p.DeletionConfirmed() || posts.Load() != 1 {
				t.Fatalf("accept: %v %+v", err, p.ProDeactivation)
			}
			if p.SpaceMergedOnce || p.TransferStatus == "completed" || p.RemoveStatus == "completed" || len(f.calls) > 0 {
				t.Fatal("deletion fabricated or executed merge facts")
			}
			if f.writes.Load() != 1 {
				t.Fatal("accepted deletion did not immediately update groups")
			}
			f.fail.Store(false)
			p, err = f.s.performProDeactivation(t.Context(), f.email)
			if err != nil || p.ProPostMergeGroupsPending() || posts.Load() != 1 || p.DeletionConfirmed() {
				t.Fatalf("retry: %v %+v", err, p.ProDeactivation)
			}
			mode = "confirmed"
			p, err = f.s.performProDeactivation(t.Context(), f.email)
			if err != nil || !p.DeletionConfirmed() || posts.Load() != 1 {
				t.Fatalf("confirm: %v %+v", err, p.ProDeactivation)
			}
			if _, err = f.s.performProDeactivation(t.Context(), f.email); err != nil || posts.Load() != 1 {
				t.Fatal("repeated final deletion", err)
			}
			rec := f.request(f.s.startProAutomation, "POST", "/auto-pro", `{"retry_current":true}`)
			if rec.Code != 409 {
				t.Fatal("submitted deletion re-entered provisioning", rec.Code)
			}
		})
	}
}

func TestProDeletionUnknownRestartAndRecovery(t *testing.T) {
	f := newProGroupFixture(t)
	enableDeletion(t, f.proExecutionFixture)
	var posts atomic.Int32
	mode := "unknown"
	f.s.proDeleteHooks = &proDeactivationHooks{Login: deletionLoginFixture, Protocol: deletionProtocolFixture(t, &posts, &mode)}
	p, err := f.s.performProDeactivation(t.Context(), f.email)
	if err == nil || !p.DeletionSubmitted() || p.DeletionAccepted() || f.writes.Load() != 0 {
		t.Fatal("uncertain submission treated as accepted")
	}
	_, err = f.s.store.UpdateProAccount(f.email, func(p *model.MailAccountProfile) {
		p.ProDeactivation.NextCheckAt = nil
		p.ProDeactivation.Status = "running"
		p.ProWorkflowRunning = true
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = f.s.store.RecoverProWorkflows(); err != nil {
		t.Fatal(err)
	}
	p, _, _ = f.s.store.MailAccountCredential(f.email)
	if p.ProDeactivation.NextCheckAt == nil || p.ProWorkflowRunning {
		t.Fatal("restart lost reconciliation")
	}
	mode = "pending"
	p, err = f.s.performProDeactivation(t.Context(), f.email)
	if err != nil || p.DeletionAccepted() || posts.Load() != 1 {
		t.Fatal("uncertain deletion replayed", err)
	}
	mode = "confirmed"
	p, err = f.s.performProDeactivation(t.Context(), f.email)
	if err != nil || !p.DeletionConfirmed() || !p.DeletionAccepted() || posts.Load() != 1 || f.writes.Load() != 1 {
		t.Fatal("explicit confirmation did not recover cleanup", err)
	}
}

func TestProDeletionIndependentIntervalAndConfirmedStopsChecks(t *testing.T) {
	f := newProGroupFixture(t)
	enableDeletion(t, f.proExecutionFixture)
	var posts atomic.Int32
	mode := "pending"
	protocol := deletionProtocolFixture(t, &posts, &mode)
	calls := 0
	f.s.proDeleteHooks = &proDeactivationHooks{Login: deletionLoginFixture, Protocol: func(ctx context.Context, input map[string]any, call func(string, map[string]any) (any, error)) (map[string]any, error) {
		calls++
		// Simulate editing the interval while an upstream request is running.
		cfg, _, _, _ := f.s.store.ProSettings()
		cfg.DeletionCheckIntervalSeconds, cfg.QuotaCheckIntervalSeconds = 37, 900
		if _, err := f.s.store.SaveProSettings(cfg, "", ""); err != nil {
			t.Fatal(err)
		}
		return protocol(ctx, input, call)
	}}
	before := time.Now()
	p, err := f.s.performProDeactivation(t.Context(), f.email)
	if err != nil {
		t.Fatal(err)
	}
	if next := p.ProDeactivation.NextCheckAt; next == nil || next.Before(before.Add(37*time.Second)) || next.After(time.Now().Add(37*time.Second)) {
		t.Fatal("independent interval not applied", next)
	}
	mode = "confirmed"
	p, err = f.s.performProDeactivation(t.Context(), f.email)
	if err != nil || !p.DeletionConfirmed() || p.ProDeactivation.NextCheckAt != nil {
		t.Fatal("confirmation did not stop timer", err)
	}
	confirmedCalls := calls
	// Explicit cleanup retries must not query deletion or schedule another retry.
	if _, err = f.s.store.UpdateProAccount(f.email, func(p *model.MailAccountProfile) { p.ProPostMergeGroups.Status = "failed" }); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.groups = []int64{1}
	f.mu.Unlock()
	f.fail.Store(true)
	p, err = f.s.performProDeactivation(t.Context(), f.email)
	if err == nil || calls != confirmedCalls || !p.DeletionConfirmed() || p.ProDeactivation.NextCheckAt != nil {
		t.Fatalf("cleanup retry: protocol calls=%d want=%d err=%v state=%+v", calls, confirmedCalls, err, p.ProDeactivation)
	}
	f.fail.Store(false)
	p, err = f.s.performProDeactivation(t.Context(), f.email)
	if err != nil || calls != confirmedCalls || p.ProDeactivation.NextCheckAt != nil || p.ProPostMergeGroupsPending() {
		t.Fatal("cleanup completion did not clear clock", err)
	}
	if _, err = f.s.performProDeactivation(t.Context(), f.email); err != nil || calls != confirmedCalls || posts.Load() != 1 {
		t.Fatal("finished deletion called upstream again")
	}
	due, err := f.s.store.DueProDeactivations(time.Now().Add(24 * time.Hour))
	if err != nil || len(due) != 0 {
		t.Fatal("completed deletion scheduled again", err)
	}
}

func TestProDeletionLoginDoesNotReplaceOAuth(t *testing.T) {
	f := newProExecutionFixture(t)
	enableDeletion(t, f)
	var posts atomic.Int32
	mode := "pending"
	attempts := 0
	logins := 0
	proto := deletionProtocolFixture(t, &posts, &mode)
	f.s.proDeleteHooks = &proDeactivationHooks{
		Protocol: func(ctx context.Context, p map[string]any, call func(string, map[string]any) (any, error)) (map[string]any, error) {
			attempts++
			if logins != 1 {
				t.Fatal("deletion protocol ran before fresh login")
			}
			if p["session"].(map[string]any)["access_token"] != "web-token" {
				t.Fatal("new web session not used")
			}
			return proto(ctx, p, call)
		},
		Login: func(context.Context, string) (map[string]any, error) {
			logins++
			return map[string]any{"success": true, "access_token": "web-token", "session_token": "web-session"}, nil
		},
	}
	if _, err := f.s.performProDeactivation(t.Context(), f.email); err != nil {
		t.Fatal(err)
	}
	_, c, err := f.s.store.MailAccountCredential(f.email)
	if err != nil || c.AccessToken != "child-token" || c.RefreshToken != "child-rt" || logins != 1 || attempts != 1 || posts.Load() != 1 {
		t.Fatal("deletion login replaced OAuth credentials")
	}
}

func TestProDeletionFreshLoginFailureStopsBeforeProtocol(t *testing.T) {
	for _, mode := range []string{"error", "failed", "missing_token"} {
		t.Run(mode, func(t *testing.T) {
			f := newProExecutionFixture(t)
			enableDeletion(t, f)
			f.s.proDeleteHooks = &proDeactivationHooks{
				Login: func(context.Context, string) (map[string]any, error) {
					if mode == "error" {
						return nil, errors.New("login unavailable")
					}
					return map[string]any{"success": mode == "missing_token"}, nil
				},
				Protocol: func(context.Context, map[string]any, func(string, map[string]any) (any, error)) (map[string]any, error) {
					t.Fatal("login failure reused old credentials")
					return nil, nil
				},
			}
			p, err := f.s.performProDeactivation(t.Context(), f.email)
			if err == nil || p.DeletionSubmitted() || p.ProDeactivation.Steps["credentials"] != "failed" {
				t.Fatal("login failure did not stop before submission", err)
			}
		})
	}
}

func TestProDeletionConfirmationContinuesWhileGroupingFails(t *testing.T) {
	f := newProGroupFixture(t)
	enableDeletion(t, f.proExecutionFixture)
	now := time.Now().Add(-time.Minute)
	_, err := f.s.store.UpdateProAccount(f.email, func(p *model.MailAccountProfile) {
		p.ProDeactivation = &model.ProDeactivation{Status: "queued", SubmittedAt: &now, AcceptedAt: &now, AccountID: "personal", Steps: map[string]string{"submit": "completed", "confirm": "waiting"}}
	})
	if err != nil {
		t.Fatal(err)
	}
	cfg, _, _, _ := f.s.store.ProSettings()
	p, _, _ := f.s.store.MailAccountCredential(f.email)
	if _, err = f.s.prepareProPostMergeGroups(p, cfg); err != nil {
		t.Fatal(err)
	}
	f.fail.Store(true)
	checks := 0
	f.s.proDeleteHooks = &proDeactivationHooks{
		Login: func(context.Context, string) (map[string]any, error) {
			t.Fatal("confirmation logged in again")
			return nil, nil
		},
		Protocol: func(_ context.Context, input map[string]any, _ func(string, map[string]any) (any, error)) (map[string]any, error) {
			checks++
			if input["check_only"] != true {
				t.Fatal("confirmation resubmitted deletion")
			}
			return map[string]any{"status": "confirmed", "confirmation_source": "http_401", "message": "HTTP 401"}, nil
		},
	}
	p, err = f.s.performProDeactivation(t.Context(), f.email)
	if err == nil || !p.DeletionConfirmed() || p.ProDeactivation.ConfirmationSource != "sub2_http_401" || p.ProDeactivation.Steps["confirm"] != "completed" || !p.ProPostMergeGroupsPending() || p.ProDeactivation.NextCheckAt != nil {
		t.Fatal("grouping failure blocked confirmation", err, p.ProDeactivation)
	}
	if _, err = f.s.performProDeactivation(t.Context(), f.email); err == nil || checks != 1 {
		t.Fatal("confirmed deletion queried again", err, checks)
	}
	f.fail.Store(false)
	p, err = f.s.performProDeactivation(t.Context(), f.email)
	if err != nil || checks != 1 || p.ProDeactivation.NextCheckAt != nil {
		t.Fatal("cleanup did not finish independently", err)
	}
}

func TestProDeletionQuotaRoutesAndThreshold(t *testing.T) {
	for _, used := range []int{79, 80} {
		t.Run(fmt.Sprint(used), func(t *testing.T) {
			f := newProExecutionFixture(t)
			enableDeletion(t, f)
			var posts atomic.Int32
			mode := "pending"
			f.s.proDeleteHooks = &proDeactivationHooks{Login: deletionLoginFixture, Protocol: deletionProtocolFixture(t, &posts, &mode)}
			remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/v1/auth/login":
					fmt.Fprint(w, `{"code":0,"data":{"access_token":"fixture"}}`)
				case "/api/v1/admin/accounts/42/stats":
					fmt.Fprint(w, `{"code":0,"data":{"summary":{"total_standard_cost":0}}}`)
				case "/api/v1/admin/openai/accounts/42/quota":
					fmt.Fprintf(w, `{"code":0,"data":{"rate_limit":{"secondary_window":{"used_percent":%d,"limit_window_seconds":604800}}}}`, used)
				default:
					t.Errorf("unexpected %s", r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			defer remote.Close()
			cfg, _, _, _ := f.s.store.ProSettings()
			cfg.Sub2.URL, cfg.Sub2.Email, cfg.QuotaUsedThreshold = remote.URL, "fixture@example.com", 80
			if _, err := f.s.store.SaveProSettings(cfg, "fixture", ""); err != nil {
				t.Fatal(err)
			}
			_, _ = f.s.store.UpdateProAccount(f.email, func(p *model.MailAccountProfile) { p.PushProvider = "sub2" })
			if _, err := f.s.performProQuota(t.Context(), f.email, true); err != nil {
				t.Fatal(err)
			}
			f.waitMerge(t)
			want := int32(0)
			if used >= 80 {
				want = 1
			}
			if posts.Load() != want || len(f.calls) != 0 {
				t.Fatal("wrong automatic quota action", posts.Load(), f.calls)
			}
		})
	}
}

func TestProDeletionTimeoutAndQuality(t *testing.T) {
	t.Run("timeout", func(t *testing.T) {
		f := newProExecutionFixture(t)
		enableDeletion(t, f)
		var posts atomic.Int32
		mode := "pending"
		f.s.proDeleteHooks = &proDeactivationHooks{Login: deletionLoginFixture, Protocol: deletionProtocolFixture(t, &posts, &mode)}
		_, err := f.s.store.UpdateProAccount(f.email, func(p *model.MailAccountProfile) {
			at := time.Now().Add(-2 * time.Hour)
			yes := true
			p.ProPushStartedAt = &at
			p.ProPushTimeoutEnabled = &yes
			p.ProPushTimeoutMinutes = 1
			p.PushProvider = "sub2"
		})
		if err != nil {
			t.Fatal(err)
		}
		f.s.tickProForceMerge(t.Context())
		f.waitMerge(t)
		if posts.Load() != 1 || len(f.calls) != 0 {
			t.Fatal("timeout did not select deletion")
		}
	})
	t.Run("quality", func(t *testing.T) {
		f := newProQualityFixture(t, "merge")
		enableDeletion(t, f.proExecutionFixture)
		var posts atomic.Int32
		mode := "pending"
		f.s.proDeleteHooks = &proDeactivationHooks{Login: deletionLoginFixture, Protocol: deletionProtocolFixture(t, &posts, &mode)}
		f.run(t)
		f.waitMerge(t)
		if posts.Load() != 1 || len(f.calls) != 0 {
			t.Fatal("quality did not select deletion", f.profile(t).ProLastError)
		}
	})
}

func TestProDeletionFullAutoAndDailyTail(t *testing.T) {
	for _, daily := range []bool{false, true} {
		t.Run(fmt.Sprint(daily), func(t *testing.T) {
			f := newProExecutionFixture(t)
			enableDeletion(t, f)
			var posts atomic.Int32
			mode := "pending"
			f.s.proDeleteHooks = &proDeactivationHooks{Login: deletionLoginFixture, Protocol: deletionProtocolFixture(t, &posts, &mode)}
			enableEJHPaymentFixture(t, f.s.store)
			card, err := f.s.store.ImportEJHCard(ejh.Card{OrderNo: "delete-tail", Number: "4242424242424242", Validity: "1299", CVV: "012", Status: "1", CardCurrency: "USD"}, true)
			if err != nil {
				t.Fatal(err)
			}
			at := time.Now().Add(-time.Minute)
			order := gptpay.Order{ID: "delete-paid", Email: f.email, CardID: card.ID, Status: "success", PlanCode: "pro20", CreatedAt: at, UpdatedAt: at}
			snap := gptpay.Snapshot{Input: gptpay.CreateInput{CardSecret: gptpay.CardSecret{Number: "4242424242424242", Year: 2099, Month: 12, CVV: "012"}}}
			if err = f.s.store.CreateGPTPayOrder(order, snap); err != nil {
				t.Fatal(err)
			}
			c := restock.DefaultConfig()
			c.DailyEJHMergeEnabled = daily
			c.DailyEJHMergeTime = "17:30"
			if _, err = f.s.store.SaveProRestockConfig(c); err != nil {
				t.Fatal(err)
			}
			_, err = f.s.store.UpdateProAccount(f.email, func(p *model.MailAccountProfile) {
				p.ProAuto = model.ProAutoState{ID: "delete-auto", OrderID: order.ID, Status: "running", QuotaUsedThreshold: 80, QuotaIntervalSeconds: 30, Steps: map[string]string{"oauth": "completed", "push": "completed", "recharge": "completed"}}
				if daily {
					p.ProDailyMerge = &model.ProDailyMergeRequest{At: at, OrderID: order.ID, AutomationID: "delete-auto"}
				}
			})
			if err != nil {
				t.Fatal(err)
			}
			quotaCalls := 0
			f.s.proAutoHooks = &proAutomationHooks{Quota: func(context.Context, string, bool) (model.MailAccountProfile, error) {
				quotaCalls++
				return f.s.store.UpdateProAccount(f.email, func(p *model.MailAccountProfile) {
					p.Quota7D = &model.FreeQuotaWindow{UsedPercent: 85, LimitWindowSeconds: 604800}
				})
			}, Merge: func(context.Context, string) (model.MailAccountProfile, error) {
				t.Error("delete mode ran merge")
				return model.MailAccountProfile{}, nil
			}}
			if err = f.s.runProAutomationTail(t.Context(), f.email); err != nil {
				t.Fatal(err)
			}
			p, _, _ := f.s.store.MailAccountCredential(f.email)
			if posts.Load() != 1 || p.ProAuto.Status != "awaiting_deletion" || p.ProAuto.NextCheckAt != nil || p.SpaceMergedOnce {
				t.Fatalf("incorrect deletion tail: %+v", p.ProAuto)
			}
			if daily && (quotaCalls != 0 || !p.ProDailyMerge.Completed) || !daily && quotaCalls != 1 {
				t.Fatal("wrong quota/daily disposition")
			}
			cases, err := f.s.store.EJHCasesForEmails([]string{f.email})
			if err != nil || len(cases) != 1 || cases[0].RequestedAt == nil || cases[0].RequestSource != "account_deactivation" {
				t.Fatal("accepted deletion did not mark refund requested", err)
			}
			if len(cases[0].Returns) != 0 || cases[0].Refunds != 0 || cases[0].Reversals != 0 {
				t.Fatal("deletion acceptance fabricated a financial refund")
			}
			mode = "confirmed"
			if _, err = f.s.performProDeactivation(t.Context(), f.email); err != nil {
				t.Fatal(err)
			}
			p, _, _ = f.s.store.MailAccountCredential(f.email)
			if p.ProAuto.Status != "completed" || posts.Load() != 1 {
				t.Fatal("confirmation did not finish original automation")
			}
		})
	}
}

func TestProDeletionSettingsExclusiveAndNoMother(t *testing.T) {
	s, st, _ := gptPayFixture(t, func(http.ResponseWriter, *http.Request) { t.Error("settings contacted upstream") })
	cfg := model.DefaultProSettings()
	cfg.AutoDeleteEnabled = true
	input := map[string]any{"automation": cfg, "gptpay": map[string]any{"plan_code": "pro5"}}
	if rec := payCall(t, s.saveProAutomationSettings, "", "", input); rec.Code != 200 {
		t.Fatal(rec.Body.String())
	}
	saved, _, _, _ := st.ProSettings()
	if !saved.AutoDeleteEnabled || saved.AutoMergeEnabled || saved.TargetAdminID != "" {
		t.Fatal("delete mode not saved")
	}
	cfg.AutoMergeEnabled = true
	input["automation"] = cfg
	if rec := payCall(t, s.saveProAutomationSettings, "", "", input); rec.Code != 400 {
		t.Fatal("conflicting actions accepted")
	}
	if _, err := st.SaveProSettings(cfg, "", ""); err == nil {
		t.Fatal("store accepted conflicting actions")
	}
}

func TestProDeletionManualResumeAndOppositeAction(t *testing.T) {
	for _, history := range []string{"stopped", "migration", "merged"} {
		t.Run(history, func(t *testing.T) {
			f := newProExecutionFixture(t)
			enableDeletion(t, f)
			var posts atomic.Int32
			mode := "pending"
			f.s.proDeleteHooks = &proDeactivationHooks{Login: deletionLoginFixture, Protocol: deletionProtocolFixture(t, &posts, &mode)}
			if _, err := f.s.store.UpdateProAccount(f.email, func(p *model.MailAccountProfile) {
				switch history {
				case "stopped":
					p.ProAuto.StopRequested = true
				case "migration":
					p.ProMigration = &model.ProMigrationState{Paused: true}
				case "merged":
					p.SpaceMergedOnce = true
					p.TransferStatus = "completed"
					p.RemoveStatus = "completed"
				}
			}); err != nil {
				t.Fatal(err)
			}
			if rec := f.request(f.s.deactivateProAccount, "POST", "/deactivate", ""); rec.Code != 202 {
				t.Fatal(rec.Code, rec.Body.String())
			}
			f.waitMerge(t)
			p, _, _ := f.s.store.MailAccountCredential(f.email)
			if !p.DeletionAccepted() || p.ProAuto.StopRequested || p.ProMigration != nil && p.ProMigration.Paused {
				t.Fatal("explicit deletion resume remained paused")
			}
			mode = "confirmed"
			if _, err := f.s.performProDeactivation(t.Context(), f.email); err != nil {
				t.Fatal(err)
			}
			cfg, _, _, _ := f.s.store.ProSettings()
			cfg.TargetAdminID = f.admin.ID
			if _, err := f.s.store.SaveProSettings(cfg, "", ""); err != nil {
				t.Fatal(err)
			}
			if rec := f.request(f.s.mergeProAccount, "POST", "/merge", ""); rec.Code != 202 {
				t.Fatal("deleted account manual merge blocked", rec.Code, rec.Body.String())
			}
			f.waitMerge(t)
			if posts.Load() != 1 {
				t.Fatal("manual merge replayed deletion")
			}
		})
	}
}
