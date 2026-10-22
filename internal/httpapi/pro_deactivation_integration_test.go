package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"chatgpt-space-merge/internal/gptpay"
	"chatgpt-space-merge/internal/model"
	"chatgpt-space-merge/internal/workflow"
)

// Only external services are simulated. This starts through the HTTP handler,
// pays through the real client, pushes/queries/changes groups through Sub2 HTTP,
// and runs the production Python deletion entry point over its actual JSON RPC.
func TestProDeletionCompleteAutomation(t *testing.T) {
	python := workflow.FindPython()
	if python == "" {
		t.Skip("complete deletion integration requires Python; run separately from the Go-only build image")
	}
	worker, err := filepath.Abs("../protocol_pro_deactivate.py")
	if err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []string{"success", "wait_quota", "refresh_login", "push_failure", "group_failure", "lost_submission_response", "explicit_rejection", "confirmation_401"} {
		t.Run(scenario, func(t *testing.T) {
			var purchases, logins, pushes, posts, groupWrites, reads, refreshes atomic.Int32
			var failPush, failGroups, reject, loseResponse, expired, confirmUnauthorized, confirmed atomic.Bool
			var usage atomic.Int32
			usage.Store(80)
			if scenario == "wait_quota" {
				usage.Store(79)
			}
			failPush.Store(scenario == "push_failure")
			failGroups.Store(scenario == "group_failure")
			reject.Store(scenario == "explicit_rejection")
			loseResponse.Store(scenario == "lost_submission_response")
			expired.Store(scenario == "refresh_login")
			confirmUnauthorized.Store(scenario == "confirmation_401")
			var initialSaved func()
			s, st, email := gptPayFixture(t, func(w http.ResponseWriter, r *http.Request) {
				initialSaved()
				switch r.URL.Path {
				case "/api/v1/gpt-recharge/orders":
					purchases.Add(1)
					var in gptpay.CreateInput
					if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
						t.Error(err)
					}
					if in.PlanCode != "pro20" || in.Session.AccessToken != proAutoToken("pro-test@example.com", "before") {
						t.Error("incorrect payment/session")
					}
					fmt.Fprint(w, `{"code":0,"data":{"id":"delete-order","status":"processing"}}`)
				case "/api/v1/gpt-recharge/orders/status":
					fmt.Fprint(w, `{"code":0,"data":{"orders":[{"id":"delete-order","status":"success"}]}}`)
				default:
					t.Errorf("unexpected payment endpoint %s", r.URL.Path)
					http.NotFound(w, r)
				}
			})
			// Stop background clocks; explicitly exercise their due-item entry points.
			s.stopProAutomations()
			s.proAutoMu.Lock()
			s.proAutoClosed = false
			s.proAutoMu.Unlock()
			initialSaved = func() {
				p, c, err := st.MailAccountCredential(email)
				if err != nil || p.ProAuto.Steps["login"] != "completed" || c.AccessToken != proAutoToken(email, "before") || !strings.Contains(c.ChatGPTSession, "first-cookie") {
					t.Error("payment before durable login")
				}
			}
			var mu sync.Mutex
			groups := []int64{1}
			remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				p, c, readErr := st.MailAccountCredential(email)
				if readErr != nil {
					t.Error(readErr)
					http.Error(w, "store", 500)
					return
				}
				switch r.URL.Path {
				case "/api/v1/auth/login":
					fmt.Fprint(w, `{"code":0,"data":{"access_token":"fixture"}}`)
				case "/api/v1/admin/accounts":
					pushes.Add(1)
					if p.ProAuto.Steps["oauth"] != "completed" || c.AccessToken != proAutoToken(email, "after") || c.RefreshToken != "after-rt" {
						t.Error("push before post-payment OAuth saved")
					}
					if failPush.Load() {
						http.Error(w, "push fixture failure", 503)
						return
					}
					fmt.Fprint(w, `{"code":0,"data":{"id":42,"name":"fixture"}}`)
				case "/api/v1/admin/accounts/42/stats":
					fmt.Fprint(w, `{"code":0,"data":{"summary":{"total_standard_cost":12,"total_actual_cost":6}}}`)
				case "/api/v1/admin/openai/accounts/42/quota":
					if p.PushStatus != "completed" {
						t.Error("quota queried before push")
					}
					fmt.Fprintf(w, `{"code":0,"data":{"rate_limit":{"secondary_window":{"used_percent":%d,"limit_window_seconds":604800}}}}`, usage.Load())
				case "/api/v1/admin/accounts/42":
					if !p.DeletionAccepted() {
						t.Error("groups changed before accepted deletion")
					}
					mu.Lock()
					defer mu.Unlock()
					if r.Method == "PUT" {
						groupWrites.Add(1)
						if failGroups.Load() {
							http.Error(w, "group fixture failure", 502)
							return
						}
						var in struct {
							Groups []int64 `json:"group_ids"`
						}
						if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
							t.Error(err)
						}
						groups = in.Groups
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{"id": 42, "group_ids": groups}})
				case "/backend-api/me":
					if expired.Load() && r.Header.Get("Authorization") != "Bearer refreshed-web-at" {
						w.WriteHeader(401)
						fmt.Fprint(w, `{}`)
						return
					}
					fmt.Fprintf(w, `{"email":%q,"id":"user-id"}`, email)
				case "/backend-api/accounts/check/v4-2023-04-27":
					reads.Add(1)
					if p.DeletionSubmitted() && confirmUnauthorized.Load() {
						w.WriteHeader(401)
						fmt.Fprint(w, `{}`)
						return
					}
					fmt.Fprintf(w, `{"accounts":{"default":{"account":{"account_id":"personal-id","structure":"personal","account_user_role":"account-owner","is_deactivated":%t}}}}`, confirmed.Load())
				case "/backend-api/sentinel/req":
					var in map[string]any
					if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
						t.Error(err)
					}
					if in["flow"] != "chatgpt_account_deactivate" {
						t.Error("wrong Sentinel flow")
					}
					fmt.Fprint(w, `{"token":"fixture-challenge","proofofwork":{"required":false},"turnstile":{"required":false}}`)
				case "/backend-api/accounts/deactivate":
					posts.Add(1)
					body, _ := io.ReadAll(r.Body)
					if r.Method != "POST" || len(body) != 0 || r.Header.Get("Chatgpt-Account-Id") != "personal-id" || !strings.Contains(r.Header.Get("Openai-Sentinel-Token"), "fixture-challenge") {
						t.Error("wrong destructive request")
					}
					if !p.DeletionSubmitted() || p.ProAuto.Steps["quota"] != "completed" {
						t.Error("deletion before durable submission checkpoint/threshold")
					}
					if reject.Load() {
						w.WriteHeader(403)
						fmt.Fprint(w, `{}`)
						return
					}
					if loseResponse.Load() {
						w.WriteHeader(522)
						fmt.Fprint(w, `{}`)
						return
					}
					fmt.Fprint(w, `{"status":"queued"}`)
				default:
					t.Errorf("unexpected external request %s %s", r.Method, r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			t.Cleanup(remote.Close)
			settings := st.Settings()
			settings.ProxyURL = remote.URL
			if err := st.SaveSettings(settings); err != nil {
				t.Fatal(err)
			}
			cfg := model.DefaultProSettings()
			cfg.AutoDeleteEnabled, cfg.AutoMergeEnabled, cfg.TargetAdminID, cfg.RetryCount = true, false, "", 0
			cfg.Sub2.URL, cfg.Sub2.Email, cfg.Sub2.GroupIDs = remote.URL, "fixture@example.com", []int64{1}
			cfg.PostMergeGroupIDs, cfg.QuotaUsedThreshold, cfg.QuotaCheckIntervalSeconds = []int64{7, 8}, 80, 30
			if _, err := st.SaveProSettings(cfg, "fixture", ""); err != nil {
				t.Fatal(err)
			}
			s.proAutoHooks = &proAutomationHooks{PollDelay: time.Millisecond, Session: func(ctx context.Context, email string, rpc func(string, map[string]any) (any, error)) error {
				logins.Add(1)
				if _, err := st.UpdateProAccount(email, func(p *model.MailAccountProfile) { p.ProAuto.ExitIP = "198.51.100.8" }); err != nil {
					return err
				}
				if _, err := rpc("pro_recharge", map[string]any{"session": map[string]any{"access_token": proAutoToken(email, "before"), "accessToken": proAutoToken(email, "before"), "session_token": "first-cookie", "user": map[string]any{"email": email}, "account": map[string]any{"id": "personal-id"}}, "exit_ip": "198.51.100.8"}); err != nil {
					return err
				}
				_, err := rpc("pro_tokens", map[string]any{"tokens": map[string]any{"access_token": proAutoToken(email, "after"), "refresh_token": "after-rt"}, "web_session": map[string]any{"accessToken": "after-web-at", "session_token": "after-cookie"}, "exit_ip": "198.51.100.8"})
				return err
			}, Merge: func(context.Context, string) (model.MailAccountProfile, error) {
				t.Error("delete mode ran four-step merge")
				return model.MailAccountProfile{}, errors.New("unexpected merge")
			}}
			s.proDeleteHooks = &proDeactivationHooks{Protocol: func(ctx context.Context, input map[string]any, call func(string, map[string]any) (any, error)) (map[string]any, error) {
				cmd := exec.CommandContext(ctx, python, "-X", "utf8", "testdata/pro_deactivation_worker.py", remote.URL, worker)
				return runOAuthChild(ctx, cmd, input, call, func(line string) { t.Log(line) })
			}, Login: func(context.Context, string) (map[string]any, error) {
				refreshes.Add(1)
				return map[string]any{"success": true, "access_token": "refreshed-web-at", "session_token": "refreshed-cookie"}, nil
			}}
			start := func() model.MailAccountProfile {
				t.Helper()
				rec := payCall(t, s.startProAutomation, "email", email, map[string]any{"card_id": "test-card", "plan_code": "pro20", "retry_current": true})
				if rec.Code != 202 {
					t.Fatalf("start: %d %s", rec.Code, rec.Body.String())
				}
				s.queueDueProAutomation(email)
				return waitProAutoFinished(t, s, email)
			}
			p := start()
			if scenario == "push_failure" {
				if p.ProAuto.Status != "failed" || posts.Load() != 0 {
					t.Fatal("push failure did not stop deletion", p.ProAuto)
				}
				failPush.Store(false)
				p = start()
			}
			if scenario == "wait_quota" {
				if p.ProAuto.Status != "waiting_quota" || posts.Load() != 0 {
					t.Fatal("deleted below quota threshold", p.ProAuto)
				}
				usage.Store(80)
				if _, err := st.UpdateProAccount(email, func(p *model.MailAccountProfile) { past := time.Now().Add(-time.Second); p.ProAuto.NextCheckAt = &past }); err != nil {
					t.Fatal(err)
				}
				s.queueDueProAutomation(email)
				p = waitProAutoFinished(t, s, email)
			}
			if scenario == "explicit_rejection" {
				if p.DeletionSubmitted() || p.ProAuto.Status != "failed" || groupWrites.Load() != 0 {
					t.Fatal("rejection was accepted", p.ProAuto)
				}
				reject.Store(false)
				p = start()
			}
			wantPosts := int32(1)
			if scenario == "explicit_rejection" {
				wantPosts = 2
			}
			if purchases.Load() != 1 || logins.Load() != 1 || posts.Load() != wantPosts || p.ProAuto.Status != "awaiting_deletion" || p.DeletionConfirmed() {
				t.Fatalf("initial chain: purchases=%d logins=%d posts=%d state=%+v deletion=%+v", purchases.Load(), logins.Load(), posts.Load(), p.ProAuto, p.ProDeactivation)
			}
			if scenario == "lost_submission_response" {
				if p.DeletionAccepted() || groupWrites.Load() != 0 {
					t.Fatal("uncertain submission treated as accepted")
				}
			} else if !p.DeletionAccepted() || groupWrites.Load() != 1 {
				t.Fatal("queued deletion did not immediately attempt grouping")
			}
			advance := func() model.MailAccountProfile {
				t.Helper()
				if _, err := st.UpdateProAccount(email, func(p *model.MailAccountProfile) {
					past := time.Now().Add(-time.Second)
					p.ProDeactivation.NextCheckAt = &past
				}); err != nil {
					t.Fatal(err)
				}
				s.tickProDeactivations(t.Context())
				done := make(chan struct{})
				go func() { s.proMergeWG.Wait(); close(done) }()
				select {
				case <-done:
				case <-time.After(10 * time.Second):
					t.Fatal("deletion continuation timed out")
				}
				p, _, err := st.MailAccountCredential(email)
				if err != nil {
					t.Fatal(err)
				}
				return p
			}
			if scenario == "group_failure" {
				if !p.ProPostMergeGroupsPending() {
					t.Fatal("lost failed grouping")
				}
				before := reads.Load()
				failGroups.Store(false)
				p = advance()
				if reads.Load() <= before || p.ProPostMergeGroupsPending() {
					t.Fatal("group retry should also check deletion confirmation")
				}
			}
			p = advance()
			if scenario == "confirmation_401" {
				if !p.DeletionConfirmed() || p.ProDeactivation.ConfirmationSource != "http_401" {
					t.Fatal("401 did not confirm submitted deletion")
				}
			} else {
				if p.DeletionConfirmed() || p.ProAuto.Status != "awaiting_deletion" {
					t.Fatal("pending incorrectly completed deletion")
				}
				confirmed.Store(true)
				p = advance()
			}
			if !p.DeletionConfirmed() || p.ProAuto.Status != "completed" || p.ProDeactivation.NextCheckAt != nil || p.ProPostMergeGroupsPending() {
				t.Fatalf("chain did not finish: %+v %+v", p.ProAuto, p.ProDeactivation)
			}
			for _, step := range []string{"login", "recharge", "oauth", "push", "quota", "deactivate"} {
				if p.ProAuto.Steps[step] != "completed" {
					t.Errorf("unfinished %s: %s", step, p.ProAuto.Steps[step])
				}
			}
			for _, step := range []string{"credentials", "verify", "submit", "confirm"} {
				if p.ProDeactivation.Steps[step] != "completed" {
					t.Errorf("unfinished deletion %s", step)
				}
			}
			if p.SpaceMergedOnce || p.TransferStatus == "completed" || p.RemoveStatus == "completed" {
				t.Fatal("deletion fabricated merge facts")
			}
			mu.Lock()
			sameGroups := slices.Equal(groups, []int64{7, 8})
			mu.Unlock()
			if !sameGroups {
				t.Fatal("wrong final groups")
			}
			beforeReads, beforeWrites := reads.Load(), groupWrites.Load()
			if _, err := s.performProDeactivation(t.Context(), email); err != nil {
				t.Fatal(err)
			}
			if posts.Load() != wantPosts || purchases.Load() != 1 || reads.Load() != beforeReads || groupWrites.Load() != beforeWrites {
				t.Fatal("completed continuation replayed external work")
			}
			_, credential, err := st.MailAccountCredential(email)
			if err != nil || credential.AccessToken != proAutoToken(email, "after") || credential.RefreshToken != "after-rt" {
				t.Fatal("deletion corrupted provisioned OAuth")
			}
			if refreshes.Load() != wantPosts {
				t.Fatal("each new deletion must log in once; checks must not log in", refreshes.Load(), wantPosts)
			}
			rec := payCall(t, s.startProAutomation, "email", email, map[string]any{"retry_current": true})
			if rec.Code != 409 {
				t.Fatal("completed deletion allowed repurchase")
			}
			t.Logf("login/payment/push/quota/deletion/grouping/confirmation complete; payments=%d deletion POSTs=%d", purchases.Load(), posts.Load())
		})
	}
}
