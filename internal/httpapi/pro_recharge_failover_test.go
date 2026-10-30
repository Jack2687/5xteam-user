package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"chatgpt-space-merge/internal/gptpay"
	"chatgpt-space-merge/internal/model"
	"chatgpt-space-merge/internal/restock"
)

func TestProRechargeFailoverAllEntrypoints(t *testing.T) {
	for _, mode := range []string{"manual", "scheduled", "restock"} {
		for _, succeeds := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s/success=%v", mode, succeeds), func(t *testing.T) {
				var primaryCalls, backupCalls, logins atomic.Int32
				s, st, email := gptPayFixture(t, func(w http.ResponseWriter, r *http.Request) {
					primaryCalls.Add(1)
					if r.Header.Get("X-API-Key") != "test-api-key" {
						t.Error("wrong primary key")
					}
					fmt.Fprint(w, `{"code":0,"data":{"id":"primary-failed","status":"failed"}}`)
				})
				s.proAutoCancel()
				s.proAutoWG.Wait()
				configureProAutoFixture(t, s)
				backup := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					n := backupCalls.Add(1)
					if r.Header.Get("Authorization") != "Bearer backup-secret" || r.Header.Get("X-API-Key") != "" {
						t.Error("crossed supplier credentials")
					}
					var body struct {
						ProductCode string `json:"productCode"`
						Country     string `json:"country"`
					}
					if r.Method != "POST" || !strings.HasSuffix(r.URL.Path, "/customer/recharges") || json.NewDecoder(r.Body).Decode(&body) != nil || body.ProductCode != "pro20x" || body.Country != "US" {
						t.Error("wrong backup purchase intent")
					}
					status := "failed"
					if n == 2 && succeeds {
						status = "success"
					}
					fmt.Fprintf(w, `{"code":0,"data":{"taskId":"backup-%d","status":%q,"order":{"status":%q}}}`, n, status, status)
				}))
				defer backup.Close()
				primary, _, _ := st.GPTPaySettings()
				if _, err := st.SaveGPTPaySettings(gptpay.Settings{Provider: gptpay.CMSNav, URL: backup.URL, PlanCode: "pro5", Country: "US", TermsAccepted: true}, "backup-secret"); err != nil {
					t.Fatal(err)
				}
				primary.BackupProvider = gptpay.CMSNav
				if _, err := st.SaveGPTPaySettings(primary, ""); err != nil {
					t.Fatal(err)
				}
				cfg, password, _, _ := st.ProSettings()
				cfg.RechargeReplacementCount, cfg.RechargeFailoverCount = 1, 2
				if _, err := st.SaveProSettings(cfg, password, ""); err != nil {
					t.Fatal(err)
				}
				card, secret, _ := st.GPTPayCard("test-card")
				capacity := 10
				card.MaxAccounts = &capacity
				if _, err := st.SaveGPTPayCard(card, secret); err != nil {
					t.Fatal(err)
				}
				s.proAutoHooks = &proAutomationHooks{
					Session: func(ctx context.Context, current string, rpc func(string, map[string]any) (any, error)) error {
						logins.Add(1)
						_, err := rpc("pro_recharge", map[string]any{"session": map[string]any{"access_token": proAutoToken(current, "login"), "accessToken": proAutoToken(current, "login"), "user": map[string]any{"email": current}, "account": map[string]any{"id": "personal-id"}}})
						if err != nil {
							return err
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
					c.Enabled, c.Mode, c.GroupID, c.FailureLimit = true, "auto", 1, 1
					var err error
					c, err = st.SaveProRestockConfig(c)
					if err != nil {
						t.Fatal(err)
					}
					tasks, err := st.ReserveProRestock(restock.Decision{ID: "failover-batch", At: time.Now(), Version: c.Version, Suggested: 1, Snapshot: restock.Snapshot{NewPlan: restockPlan(primary.Provider, primary.PlanCode, primary.Country, primary.CreditQuantity), Binding: restockBinding(cfg), GroupID: 1}}, "", 0)
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
				for i := 0; i < 4; i++ {
					spare := fmt.Sprintf("spare-%d@example.com", i)
					if _, err := st.SaveMailAccount(model.MailAccountProfile{Email: spare, ManagementScope: "pro"}, model.MailAccountCredentials{Email: spare}); err != nil {
						t.Fatal(err)
					}
				}
				req := httptest.NewRequest("POST", "/", strings.NewReader(`{"provider":"tokenseek","card_id":"test-card","plan_code":"pro20"}`)).WithContext(ctx)
				req.SetPathValue("email", email)
				response := httptest.NewRecorder()
				s.startProAutomation(response, req)
				if response.Code != 202 {
					t.Fatal(response.Body.String())
				}
				last := waitProAutoFinished(t, s, email)
				originalID := last.ProAuto.OrderID
				for attempt := 1; attempt <= 3; attempt++ {
					if !last.ProAuto.Replacement.Pending {
						t.Fatalf("chain ended before attempt %d: %+v", attempt, last.ProAuto)
					}
					s.dispatchProRechargeReplacements(context.Background())
					previous, _, _ := st.MailAccountCredential(last.Email)
					if previous.ProAuto.Replacement.NextEmail == "" {
						t.Fatal("missing successor", previous.ProAuto)
					}
					if mode == "restock" {
						pay, _, _ := st.GPTPaySettings()
						snap := restock.Snapshot{At: time.Now(), Valid: true, LoadValid: true, Binding: restockBinding(cfg), GroupID: 1, NewPlan: restockPlan(pay.Provider, pay.PlanCode, pay.Country, pay.CreditQuantity)}
						if _, _, err := s.restockTasks(snap, cfg, c); err != nil {
							t.Fatal(err)
						}
						if attempt > 1 {
							// The backup default product has an invalid template, but this
							// committed task still has its valid original product.
							cfg.EJHPlanTemplates = map[string]string{"pro5": "missing-template"}
							if _, err := st.SaveProSettings(cfg, password, ""); err != nil {
								t.Fatal(err)
							}
							s.proRestock.monitor = func(context.Context, restock.Config, model.ProSettings, string) (restock.Snapshot, error) {
								snap.At = time.Now()
								snap.Accounts = []restock.Account{{ID: 1, Ready: true, Capacity: 10}}
								return snap, nil
							}
							if err := s.tickProRestock(context.Background(), true); err != nil {
								t.Fatal(err)
							}
						} else {
							s.dispatchProRestock(context.Background(), c, cfg, snap)
						}
					}
					last = waitProAutoFinished(t, s, previous.ProAuto.Replacement.NextEmail)
					if last.ProAuto.Replacement.Attempts != attempt || last.ProAuto.Replacement.ProviderSwitched != (attempt > 1) {
						t.Fatal(last.ProAuto)
					}
				}
				if primaryCalls.Load() != 2 || backupCalls.Load() != 2 || logins.Load() != 4 {
					t.Fatal("wrong number of attempts", primaryCalls.Load(), backupCalls.Load(), logins.Load(), last.ProAuto.Error, last.ProAuto.Replacement.Message)
				}
				if last.ProAuto.Replacement.Pending || last.ProAuto.Replacement.NextEmail != "" {
					t.Fatal("unbounded failover", last.ProAuto)
				}
				if succeeds && (last.ProAuto.Steps["oauth"] != "completed" || last.PushStatus != "completed") {
					t.Fatal("did not finish", last.ProAuto)
				}
				if !succeeds && last.ProAuto.Status != "failed" {
					t.Fatal("not exhausted", last.ProAuto)
				}
				original, _, _ := st.MailAccountCredential(email)
				if original.ProAuto.OrderID != originalID {
					t.Fatal("original order changed")
				}
				active, key, _ := st.GPTPaySettings()
				if active.Provider != gptpay.CMSNav || active.BackupProvider != gptpay.Tokenseek || key != "backup-secret" {
					t.Fatal("roles not exchanged")
				}
				run := model.ProScheduleRun{ID: "chain", Status: "running", Tasks: []model.ProScheduleTask{{Email: email, Status: "running"}}}
				s.reconcileProSchedule(&run)
				if run.Tasks[0].Email != last.Email || (run.Tasks[0].Status == "completed") != succeeds {
					t.Fatal("lost task chain", run)
				}
				if mode == "restock" {
					tasks, _ := st.ProRestockTasks()
					if len(tasks) != 4 {
						t.Fatal("wrong task count", len(tasks))
					}
					for _, task := range tasks {
						if task.DecisionID != "failover-batch" {
							t.Fatal("split batch")
						}
					}
				}
			})
		}
	}
}

func TestProRechargeFailoverSettingsCompatibility(t *testing.T) {
	s, st, _ := gptPayFixture(t, func(http.ResponseWriter, *http.Request) { t.Error("unexpected upstream request") })
	pro := model.DefaultProSettings()
	pro.RechargeFailoverCount = 3
	input := map[string]any{"automation": pro, "gptpay": map[string]any{"plan_code": "pro5"}}
	if rec := payCall(t, s.saveProAutomationSettings, "", "", input); rec.Code != 200 {
		t.Fatal(rec.Body.String())
	}
	encoded, _ := json.Marshal(pro)
	var legacy map[string]any
	_ = json.Unmarshal(encoded, &legacy)
	delete(legacy, "recharge_failover_count")
	input["automation"] = legacy
	if rec := payCall(t, s.saveProAutomationSettings, "", "", input); rec.Code != 200 {
		t.Fatal(rec.Body.String())
	}
	saved, _, _, _ := st.ProSettings()
	if saved.RechargeFailoverCount != 3 {
		t.Fatal("old client reset failover")
	}
	for _, n := range []int{-1, 11} {
		legacy["recharge_failover_count"] = n
		if rec := payCall(t, s.saveProAutomationSettings, "", "", input); rec.Code != 400 {
			t.Fatal("bad limit accepted", n)
		}
	}
	legacy["recharge_failover_count"] = 0
	if rec := payCall(t, s.saveProAutomationSettings, "", "", input); rec.Code != 200 {
		t.Fatal(rec.Body.String())
	}
	saved, _, _, _ = st.ProSettings()
	if saved.RechargeFailoverCount != 0 {
		t.Fatal("cannot disable")
	}
}
