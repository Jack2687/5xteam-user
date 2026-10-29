package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"chatgpt-space-merge/internal/model"
	"chatgpt-space-merge/internal/restock"
)

func TestProRechargeReplacementAllFullAutoEntrypoints(t *testing.T) {
	for _, mode := range []string{"manual", "scheduled", "restock"} {
		for _, failure := range []string{"before_order", "rejected", "rejected_twice", "unknown", "oauth"} {
			t.Run(mode+"/"+failure, func(t *testing.T) {
				var purchases atomic.Int32
				s, st, email := gptPayFixture(t, func(w http.ResponseWriter, r *http.Request) {
					n := purchases.Add(1)
					if n == 1 && failure == "rejected" || n <= 2 && failure == "rejected_twice" {
						fmt.Fprint(w, `{"code":0,"data":{"id":"rejected","status":"failed","failureReason":"arbitrary failure"}}`)
						return
					}
					if n == 1 && failure == "unknown" {
						w.WriteHeader(503)
						fmt.Fprint(w, `{"error":"unknown outcome"}`)
						return
					}
					fmt.Fprintf(w, `{"code":0,"data":{"id":"success-%d","status":"success","settlementStatus":"settled"}}`, n)
				})
				s.proAutoCancel()
				s.proAutoWG.Wait()
				configureProAutoFixture(t, s)
				cfg, password, _, _ := st.ProSettings()
				cfg.RechargeReplacementCount = 2
				cfg.RetryCount = 2
				cfg.RetryIntervalSeconds = 1
				if _, err := st.SaveProSettings(cfg, password, ""); err != nil {
					t.Fatal(err)
				}
				// Plenty of per-card capacity lets unknown-order occupancy remain
				// reserved while a distinct account starts its own order.
				card, secret, _ := st.GPTPayCard("test-card")
				limit := 10
				card.MaxAccounts = &limit
				if _, err := st.SaveGPTPayCard(card, secret); err != nil {
					t.Fatal(err)
				}
				var mu sync.Mutex
				var logins []string
				s.proAutoHooks = &proAutomationHooks{
					Session: func(ctx context.Context, current string, rpc func(string, map[string]any) (any, error)) error {
						mu.Lock()
						logins = append(logins, current)
						mu.Unlock()
						if current == email && failure == "before_order" {
							_ = s.autoProUpdate(current, "login", "completed", "ok")
							_ = s.autoProUpdate(current, "recharge", "running", "prepare")
							return errors.New("any arbitrary step-two error")
						}
						_, err := rpc("pro_recharge", map[string]any{"session": map[string]any{"access_token": proAutoToken(current, "login"), "user": map[string]any{"email": current}, "account": map[string]any{"id": "personal-id"}}})
						if err != nil {
							return err
						}
						if failure == "oauth" {
							return errors.New("step three failed")
						}
						_, err = rpc("pro_tokens", map[string]any{"tokens": map[string]any{"access_token": proAutoToken(current, "paid"), "refresh_token": "new-rt"}})
						return err
					},
					Push: func(_ context.Context, current string) (model.MailAccountProfile, error) {
						return st.UpdateProAccount(current, func(p *model.MailAccountProfile) { p.PushStatus = "completed" })
					},
					Quota: func(_ context.Context, current string, _ bool) (model.MailAccountProfile, error) {
						return st.UpdateProAccount(current, func(p *model.MailAccountProfile) { p.Quota7D = &model.FreeQuotaWindow{UsedPercent: 1} })
					},
				}
				ctx := context.Background()
				var c restock.Config
				if mode == "scheduled" {
					ctx = context.WithValue(ctx, proScheduledContextKey{}, true)
				}
				if mode == "restock" {
					c = restock.DefaultConfig()
					c.Enabled, c.Mode, c.GroupID = true, "auto", 1
					c.FailureLimit = 1
					var err error
					c, err = st.SaveProRestockConfig(c)
					if err != nil {
						t.Fatal(err)
					}
					pay, _, _ := st.GPTPaySettings()
					tasks, err := st.ReserveProRestock(restock.Decision{ID: "batch", At: time.Now(), Version: c.Version, Suggested: 1, Snapshot: restock.Snapshot{NewPlan: restockPlan(pay.Provider, pay.PlanCode, pay.Country, pay.CreditQuantity), Binding: restockBinding(cfg), GroupID: 1}}, "", 0)
					if err != nil || len(tasks) != 1 {
						t.Fatal(tasks, err)
					}
					task := tasks[0]
					task.Status = "submitting"
					if err = st.ClaimProRestockDispatch(task); err != nil {
						t.Fatal(err)
					}
					ctx = context.WithValue(ctx, proRestockContextKey{}, task.ID)
				}
				for _, spare := range []string{"spare-1@example.com", "spare-2@example.com"} {
					if _, err := st.SaveMailAccount(model.MailAccountProfile{Email: spare, ManagementScope: "pro"}, model.MailAccountCredentials{Email: spare}); err != nil {
						t.Fatal(err)
					}
				}
				raw, _ := json.Marshal(map[string]any{"provider": "tokenseek", "card_id": "test-card", "plan_code": "pro20"})
				r := httptest.NewRequest("POST", "/", strings.NewReader(string(raw))).WithContext(ctx)
				r.SetPathValue("email", email)
				w := httptest.NewRecorder()
				s.startProAutomation(w, r)
				if w.Code != 202 {
					t.Fatal(w.Body.String())
				}
				original := waitProAutoFinished(t, s, email)
				s.dispatchProRechargeReplacements(context.Background())
				if failure == "oauth" {
					p, _, _ := st.MailAccountCredential(email)
					if p.ProAuto.Replacement.Pending || p.ProAuto.Replacement.NextEmail != "" || p.ProAuto.RetryAttempts != 0 {
						t.Fatal(p.ProAuto)
					}
					return
				}
				if mode == "restock" {
					pay, _, _ := st.GPTPaySettings()
					snap := restock.Snapshot{At: time.Now(), Valid: true, LoadValid: true, Binding: restockBinding(cfg), GroupID: 1, NewPlan: restockPlan(pay.Provider, pay.PlanCode, pay.Country, pay.CreditQuantity)}
					if _, _, err := s.restockTasks(snap, cfg, c); err != nil {
						t.Fatal(err)
					}
					s.dispatchProRestock(context.Background(), c, cfg, snap)
				}
				root, _, _ := st.MailAccountCredential(email)
				if root.ProAuto.Replacement.NextEmail == "" {
					t.Fatal("did not pick successor", root.ProAuto)
				}
				next := waitProAutoFinished(t, s, root.ProAuto.Replacement.NextEmail)
				wantAttempts := 1
				if failure == "rejected_twice" {
					wantAttempts = 2
					if !next.ProAuto.Replacement.Pending {
						t.Fatal("second failure did not queue another account", next.ProAuto)
					}
					s.dispatchProRechargeReplacements(context.Background())
					if mode == "restock" {
						pay, _, _ := st.GPTPaySettings()
						snap := restock.Snapshot{At: time.Now(), Valid: true, LoadValid: true, Binding: restockBinding(cfg), GroupID: 1, NewPlan: restockPlan(pay.Provider, pay.PlanCode, pay.Country, pay.CreditQuantity)}
						if _, _, err := s.restockTasks(snap, cfg, c); err != nil {
							t.Fatal(err)
						}
						s.dispatchProRestock(context.Background(), c, cfg, snap)
					}
					second, _, _ := st.MailAccountCredential(next.Email)
					if second.ProAuto.Replacement.NextEmail == "" {
						t.Fatal("missing third account", second.ProAuto)
					}
					next = waitProAutoFinished(t, s, second.ProAuto.Replacement.NextEmail)
				}
				if next.ProAuto.Steps["oauth"] != "completed" || next.PushStatus != "completed" || next.ProAuto.Replacement.Attempts != wantAttempts {
					t.Fatalf("successor did not finish provisioning: %+v", next.ProAuto)
				}
				if root.ProAuto.OrderID != original.ProAuto.OrderID || root.ProAuto.ID != original.ProAuto.ID {
					t.Fatal("original evidence overwritten")
				}
				mu.Lock()
				defer mu.Unlock()
				if len(logins) != wantAttempts+1 || logins[0] != email || logins[1] == email {
					t.Fatal("retried original account", logins)
				}
				run := model.ProScheduleRun{ID: "schedule", Status: "running", Tasks: []model.ProScheduleTask{{Email: email, Status: "running"}}}
				s.reconcileProSchedule(&run)
				if run.Tasks[0].Email != next.Email || run.Tasks[0].Status != "completed" {
					t.Fatal("batch lost successor", run)
				}
			})
		}
	}
}
