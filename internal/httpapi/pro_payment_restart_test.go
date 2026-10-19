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

func TestProFailedPaymentRestartFullChain(t *testing.T) {
	for _, scenario := range []string{"failed", "processing", "submission_unknown", "login_failed", "stopped", "cmsnav", "restock_failed", "restock_login_failed"} {
		t.Run(scenario, func(t *testing.T) {
			var logins, purchases, replays, lookups, pushes, merges atomic.Int32
			var keys []string
			s, st, email := gptPayFixture(t, func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/status") {
					lookups.Add(1)
					fmt.Fprint(w, `{"code":0,"data":{"orders":[{"id":"original-remote","status":"failed","failureReason":"unpaid"}]}}`)
					return
				}
				id := r.Header.Get("Idempotency-Key")
				if id == "old-order" {
					replays.Add(1)
					fmt.Fprint(w, `{"code":0,"data":{"id":"original-remote","status":"failed"}}`)
					return
				}
				purchases.Add(1)
				keys = append(keys, id)
				var input struct {
					Session gptpay.Session `json:"session"`
				}
				if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
					t.Error(err)
				}
				if input.Session.AccessToken != proAutoToken("pro-test@example.com", "fresh-login") {
					t.Error("new payment did not use fresh login session")
				}
				if scenario == "cmsnav" {
					fmt.Fprint(w, `{"code":0,"data":{"taskId":"new-remote","status":"success"}}`)
				} else {
					fmt.Fprint(w, `{"code":0,"data":{"id":"new-remote","status":"success"}}`)
				}
			})
			configureProAutoFixture(t, s)
			if strings.HasPrefix(scenario, "restock_") {
				s.proAutoCancel()
				c := restock.DefaultConfig()
				c.Enabled, c.Mode, c.GroupID = true, "auto", 1
				saved, err := st.SaveProRestockConfig(c)
				if err != nil {
					t.Fatal(err)
				}
				tasks, err := st.ReserveProRestock(restock.Decision{ID: "manual-retry", At: time.Now(), Version: saved.Version, Suggested: 1}, "", 0)
				if err != nil || len(tasks) != 1 {
					t.Fatal("failed to prepare restock claim", err)
				}
			}
			cfg, key, _ := st.GPTPaySettings()
			if scenario == "cmsnav" {
				cfg.Provider = gptpay.CMSNav
				cfg.TermsAccepted = true
				if _, err := st.SaveGPTPaySettings(cfg, "cmsnav-key"); err != nil {
					t.Fatal(err)
				}
				cfg, key, _ = st.GPTPaySettings()
			}
			orderID := "old-order"
			if strings.HasSuffix(scenario, "login_failed") {
				orderID = ""
			} else {
				status, remote := "failed", "original-remote"
				if scenario == "processing" {
					status = "processing"
				}
				if scenario == "submission_unknown" {
					status = "submission_unknown"
					remote = ""
				}
				p, c, _ := st.MailAccountCredential(email)
				session, _ := gptPaySession(p, c)
				_, secret, _ := st.GPTPayCard("test-card")
				order := gptpay.Order{ID: orderID, Email: email, CardID: "test-card", PlanCode: cfg.PlanCode, Provider: cfg.Provider, Status: status, Error: "支付失败：unpaid", Remote: gptpay.RemoteOrder{ID: remote, Status: status}, CreatedAt: time.Now().Add(-time.Hour)}
				if err := st.CreateGPTPayOrder(order, gptpay.NewSnapshot(cfg, key, secret, session)); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := st.UpdateProAccount(email, func(p *model.MailAccountProfile) {
				p.ProAuto = model.ProAutoState{ID: "old-flow", OrderID: orderID, CardID: "test-card", Status: "failed", Stage: "recharge", StopVersion: 3, Steps: map[string]string{"login": "completed", "recharge": "failed"}, ExitIP: "192.0.2.1"}
				if scenario == "stopped" {
					p.ProAuto.StopRequested = true
					p.ProAuto.Status = "stopped"
				}
				p.ProStageOverrides = map[string]model.ProStageOverride{"login": {Status: "not_started", UpdatedAt: time.Now()}, "recharge": {Status: "not_started", UpdatedAt: time.Now()}}
			}); err != nil {
				t.Fatal(err)
			}
			entered, release := make(chan struct{}), make(chan struct{})
			s.proAutoHooks = &proAutomationHooks{Session: func(ctx context.Context, email string, rpc func(string, map[string]any) (any, error)) error {
				logins.Add(1)
				p, _, _ := st.MailAccountCredential(email)
				if p.ProAuto.ID == "old-flow" || p.ProAuto.OrderID != "" || p.ProAuto.ExitIP != "" || len(p.ProStageOverrides) != 0 || p.ProAuto.StopVersion != 3 || p.ProRestockTaskID != "" {
					t.Error("fresh execution retained old payment/session/annotations")
				}
				close(entered)
				select {
				case <-release:
				case <-ctx.Done():
					return ctx.Err()
				}
				_, err := rpc("pro_recharge", map[string]any{"session": map[string]any{"access_token": proAutoToken(email, "fresh-login"), "accessToken": proAutoToken(email, "fresh-login"), "session_token": "fresh-cookie", "user": map[string]any{"email": email}, "account": map[string]any{"id": "personal-id"}}})
				if err != nil {
					return err
				}
				_, err = rpc("pro_tokens", map[string]any{"tokens": map[string]any{"access_token": proAutoToken(email, "after"), "refresh_token": "fresh-rt"}})
				return err
			}, Push: func(ctx context.Context, email string) (model.MailAccountProfile, error) {
				pushes.Add(1)
				return st.UpdateProAccount(email, func(p *model.MailAccountProfile) { p.PushStatus = "completed" })
			}, Quota: func(ctx context.Context, email string, _ bool) (model.MailAccountProfile, error) {
				return st.UpdateProAccount(email, func(p *model.MailAccountProfile) { p.Quota7D = &model.FreeQuotaWindow{UsedPercent: 100} })
			}, Merge: func(ctx context.Context, email string) (model.MailAccountProfile, error) {
				merges.Add(1)
				return st.UpdateProAccount(email, func(p *model.MailAccountProfile) { p.SpaceMergedOnce = true; p.RemoveStatus = "completed" })
			}}
			rec := payCall(t, s.startProAutomation, "email", email, map[string]any{"retry_current": true})
			if rec.Code != 202 {
				close(release)
				t.Fatal(rec.Code, rec.Body.String())
			}
			select {
			case <-entered:
			case <-time.After(3 * time.Second):
				close(release)
				t.Fatal("fresh login not started")
			}
			if strings.HasPrefix(scenario, "restock_") {
				pro, _, _, _ := st.ProSettings()
				tasks, _, err := s.restockTasks(restock.Snapshot{Binding: restockBinding(pro), GroupID: 1}, pro, restock.Config{GroupID: 1, LeadMinutes: 5, LeadSamples: 2})
				if err != nil {
					close(release)
					t.Fatal(err)
				}
				external := 0
				for _, task := range tasks {
					if task.Status == "external" && task.Email == email {
						external++
					}
				}
				if external != 1 {
					close(release)
					t.Fatal("manual retry disappeared from restock in-flight tracking", tasks)
				}
			}
			duplicate := payCall(t, s.startProAutomation, "email", email, map[string]any{"retry_current": true})
			close(release)
			if duplicate.Code != 202 {
				t.Fatal(duplicate.Body.String())
			}
			p := waitProAutoFinished(t, s, email)
			if p.ProAuto.Status != "completed" || logins.Load() != 1 || purchases.Load() != 1 || pushes.Load() != 1 || merges.Load() != 1 {
				t.Fatalf("chain not completed: %+v logins %d purchases %d push %d merge %d", p.ProAuto, logins.Load(), purchases.Load(), pushes.Load(), merges.Load())
			}
			if p.ProAuto.OrderID == orderID || p.ProAuto.OrderID == "" || len(keys) != 1 || keys[0] != p.ProAuto.OrderID {
				t.Fatal("new payment identity not assigned")
			}
			if orderID != "" {
				old, _, err := st.GPTPayOrder(orderID)
				if err != nil || old.Status != "failed" {
					t.Fatal("original failed order was lost")
				}
			}
			if scenario == "processing" && lookups.Load() != 1 {
				t.Fatal("pending order was not checked")
			}
			if scenario == "submission_unknown" && replays.Load() != 1 {
				t.Fatal("uncertain submission must first replay its original key")
			}
			if again := payCall(t, s.startProAutomation, "email", email, map[string]any{"retry_current": true}); again.Code != 409 {
				t.Fatal("completed flow restarted")
			}
		})
	}
}

func TestProFailedPaymentRetryProjection(t *testing.T) {
	s, st, email := gptPayFixture(t, func(w http.ResponseWriter, r *http.Request) { t.Error("unexpected supplier request") })
	if err := st.CreateGPTPayOrder(gptpay.Order{ID: "failed", Email: email, Status: "failed", Error: "unpaid", CreatedAt: time.Now()}, gptpay.Snapshot{}); err != nil {
		t.Fatal(err)
	}
	st.UpdateProAccount(email, func(p *model.MailAccountProfile) {
		p.ProAuto = model.ProAutoState{ID: "flow", Status: "failed", OrderID: "failed", Stage: "recharge"}
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	req.SetPathValue("email", email)
	s.getProAutomation(rec, req)
	var result struct {
		Data struct {
			PaymentStatus string `json:"payment_status"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil || result.Data.PaymentStatus != "failed" {
		t.Fatal(rec.Body.String())
	}
}
