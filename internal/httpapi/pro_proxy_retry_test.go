package httpapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"chatgpt-space-merge/internal/model"
)

func TestProProxyRetryConfiguredWait(t *testing.T) {
	for _, mode := range []string{"success", "exhausted", "stop", "updated_config", "paid", "unknown_payment"} {
		t.Run(mode, func(t *testing.T) {
			var logins, payments atomic.Int32
			s, st, email := gptPayFixture(t, func(w http.ResponseWriter, _ *http.Request) {
				payments.Add(1)
				if mode == "unknown_payment" {
					w.WriteHeader(503)
					fmt.Fprint(w, `{"code":50301}`)
					return
				}
				fmt.Fprint(w, `{"code":0,"data":{"id":"paid","status":"success"}}`)
			})
			s.proAutoCancel()
			s.proAutoWG.Wait()
			configureProAutoFixture(t, s)
			settings, password, _, _ := st.ProSettings()
			settings.RetryCount, settings.RetryIntervalSeconds = 0, 1
			if mode == "stop" {
				settings.RetryIntervalSeconds = 10
			}
			if _, err := st.SaveProSettings(settings, password, ""); err != nil {
				t.Fatal(err)
			}
			proxyErr := errors.New("network error: CONNECT tunnel failed, response 504")
			var flowID, paymentID string
			var previous time.Time
			s.proAutoHooks = &proAutomationHooks{
				Session: func(ctx context.Context, current string, rpc func(string, map[string]any) (any, error)) error {
					n := int(logins.Add(1))
					p, _, _ := st.MailAccountCredential(current)
					if n == 1 {
						flowID, paymentID = p.ProAuto.ID, p.ProAuto.PaymentOrderID()
					}
					if current != email || p.ProAuto.ID != flowID || p.ProAuto.CardID != "test-card" || p.ProAuto.RetryAttempts != 0 || p.ProAuto.ProxyRetryAttempts != n-1 || p.ProAuto.PaymentOrderID() != paymentID {
						t.Error("proxy recovery changed account/card/payment identity or consumed business retries")
					}
					interval := time.Second
					if mode == "updated_config" {
						interval = 2 * time.Second
					}
					if !previous.IsZero() && time.Since(previous) < interval {
						t.Error("retry skipped configured wait")
					}
					previous = time.Now()
					if n == 1 && mode == "updated_config" {
						settings.RetryIntervalSeconds = 2
						if _, err := st.SaveProSettings(settings, password, ""); err != nil {
							t.Error(err)
						}
					}
					if mode == "exhausted" || (n == 1 && mode != "paid" && mode != "unknown_payment") {
						return proxyErr
					}
					if _, err := rpc("pro_recharge", map[string]any{"session": map[string]any{"access_token": proAutoToken(current, "initial"), "user": map[string]any{"email": current}, "account": map[string]any{"id": "personal-id"}}}); err != nil {
						return fmt.Errorf("%v: %w", err, proxyErr)
					}
					if mode == "paid" {
						return proxyErr
					}
					_, err := rpc("pro_tokens", map[string]any{"tokens": map[string]any{"access_token": proAutoToken(current, "new"), "refresh_token": "rt"}})
					return err
				},
				Push: func(_ context.Context, email string) (model.MailAccountProfile, error) {
					return st.UpdateProAccount(email, func(p *model.MailAccountProfile) { p.PushStatus = "completed" })
				},
				Quota: func(_ context.Context, email string, _ bool) (model.MailAccountProfile, error) {
					return st.UpdateProAccount(email, func(p *model.MailAccountProfile) { p.Quota7D = &model.FreeQuotaWindow{UsedPercent: 10} })
				},
			}
			rec := payCall(t, s.startProAutomation, "email", email, map[string]any{"provider": "tokenseek", "card_id": "test-card", "plan_code": "pro20"})
			if rec.Code != 202 {
				t.Fatal(rec.Body.String())
			}
			if mode != "paid" && mode != "unknown_payment" {
				deadline := time.Now().Add(2 * time.Second)
				for {
					p, _, _ := st.MailAccountCredential(email)
					if at := p.ProAuto.RetryAt; at != nil {
						want := time.Duration(settings.RetryIntervalSeconds) * time.Second
						if delta := at.Sub(p.ProAuto.UpdatedAt); delta < want-100*time.Millisecond || delta > want+100*time.Millisecond {
							t.Fatalf("scheduled wait %v, want %v", delta, want)
						}
						break
					}
					if time.Now().After(deadline) {
						t.Fatal("no published retry deadline")
					}
					time.Sleep(5 * time.Millisecond)
				}
			}
			if mode == "stop" {
				if rec := payCall(t, s.stopProAutomation, "email", email, map[string]any{}); rec.Code != 202 {
					t.Fatal(rec.Body.String())
				}
			}
			p := waitProAutoFinished(t, s, email)
			wantLogins, wantPayments, wantStatus := int32(2), int32(1), "waiting_quota"
			switch mode {
			case "exhausted":
				wantLogins, wantPayments, wantStatus = 4, 0, "failed"
				if !strings.Contains(p.ProAuto.Error, "代理隧道连续不可用") {
					t.Fatal(p.ProAuto.Error)
				}
			case "stop":
				wantLogins, wantPayments, wantStatus = 1, 0, "stopped"
			case "paid", "unknown_payment":
				wantLogins, wantPayments, wantStatus = 1, 1, "failed"
			}
			if logins.Load() != wantLogins || payments.Load() != wantPayments || p.ProAuto.Status != wantStatus || p.ProAuto.RetryAt != nil {
				t.Fatalf("logins=%d payments=%d state=%+v", logins.Load(), payments.Load(), p.ProAuto)
			}
		})
	}
}
