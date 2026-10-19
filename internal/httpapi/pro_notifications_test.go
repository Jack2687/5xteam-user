package httpapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"chatgpt-space-merge/internal/model"
)

func captureProNotifications(s *Server) {
	s.stopNotifications()
	s.notifications.queue = make(chan notificationEvent, 32)
}

func proNotificationKinds(t *testing.T, s *Server) string {
	t.Helper()
	kinds := []string{}
	for len(s.notifications.queue) > 0 {
		event := <-s.notifications.queue
		if strings.Contains(event.Text, "secret-fixture") {
			t.Fatal("raw error leaked to robot")
		}
		if event.Kind == "pro_failed" && strings.Contains(event.Text, "失败位置：\n") {
			t.Fatal("failure notification lacks stage label")
		}
		kinds = append(kinds, event.Kind)
	}
	return strings.Join(kinds, ",")
}

func TestNotificationProAutomationLifecycle(t *testing.T) {
	for _, tc := range []struct{ name, want string }{
		{"success", "pro_start,pro_pushed"},
		{"login_failure", "pro_failed"},
		{"card_disabled_before_submit", "pro_failed"},
		{"payment_failure", "pro_start,pro_failed"},
		{"unknown_payment", "pro_start,pro_failed"},
		{"oauth_failure", "pro_start,pro_failed"},
		{"push_failure", "pro_start,pro_failed"},
		{"quota_failure", "pro_start,pro_pushed"},
		{"interrupted", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var server *Server
			purchases := 0
			s, st, email := gptPayFixture(t, func(w http.ResponseWriter, r *http.Request) {
				purchases++
				if len(server.notifications.queue) != 1 {
					t.Error("actual payment submission must follow exactly one start notice")
				}
				if tc.name == "payment_failure" || tc.name == "unknown_payment" {
					code := 402
					if tc.name == "unknown_payment" {
						code = 503
					}
					w.WriteHeader(code)
					fmt.Fprintf(w, `{"code":%d01,"message":"secret-fixture"}`, code)
					return
				}
				fmt.Fprint(w, `{"code":0,"data":{"id":"notice-order","status":"success"}}`)
			})
			server = s
			captureProNotifications(s)
			configureProAutoFixture(t, s)
			s.proAutoHooks = &proAutomationHooks{
				Session: func(ctx context.Context, email string, rpc func(string, map[string]any) (any, error)) error {
					if len(s.notifications.queue) != 0 {
						t.Error("login/queue must not emit start")
					}
					if tc.name == "login_failure" {
						return errors.New("login secret-fixture")
					}
					if tc.name == "interrupted" {
						s.proAutoMu.Lock()
						s.proAutoJobs[email]()
						s.proAutoMu.Unlock()
						return context.Canceled
					}
					if tc.name == "card_disabled_before_submit" {
						card, secret, _ := st.GPTPayCard("test-card")
						card.Enabled = false
						if _, err := st.SaveGPTPayCard(card, secret); err != nil {
							return err
						}
					}
					args := map[string]any{"session": map[string]any{"access_token": proAutoToken(email, "before"), "user": map[string]any{"email": email}, "account": map[string]any{"id": "personal-id"}}}
					if _, err := rpc("pro_recharge", args); err != nil {
						return err
					}
					// Duplicate RPC cannot create or announce another payment.
					if _, err := rpc("pro_recharge", args); err == nil {
						t.Error("duplicate recharge accepted")
					}
					if tc.name == "oauth_failure" {
						return errors.New("oauth secret-fixture")
					}
					_, err := rpc("pro_tokens", map[string]any{"tokens": map[string]any{"access_token": proAutoToken(email, "after"), "refresh_token": "new-rt"}})
					return err
				},
				Push: func(ctx context.Context, email string) (model.MailAccountProfile, error) {
					if tc.name == "push_failure" {
						return model.MailAccountProfile{}, errors.New("push secret-fixture")
					}
					return st.UpdateProAccount(email, func(p *model.MailAccountProfile) { p.PushStatus = "completed" })
				},
				Quota: func(ctx context.Context, email string, auto bool) (model.MailAccountProfile, error) {
					if tc.name == "quota_failure" {
						return model.MailAccountProfile{}, errors.New("quota secret-fixture")
					}
					return st.UpdateProAccount(email, func(p *model.MailAccountProfile) { p.Quota7D = &model.FreeQuotaWindow{UsedPercent: 10} })
				},
			}
			rec := payCall(t, s.startProAutomation, "email", email, map[string]any{"card_id": "test-card", "plan_code": "pro20"})
			if rec.Code != http.StatusAccepted {
				t.Fatal(rec.Body.String())
			}
			p := waitProAutoFinished(t, s, email)
			if got := proNotificationKinds(t, s); got != tc.want {
				t.Fatalf("notifications %s, want %s", got, tc.want)
			}
			if strings.HasPrefix(tc.want, "pro_start") && purchases != 1 || !strings.HasPrefix(tc.want, "pro_start") && purchases != 0 {
				t.Fatal("unexpected number of purchases", purchases)
			}
			if p.ProAuto.Status == "waiting_quota" {
				if err := s.runProAutomationTail(context.Background(), email); err != nil {
					t.Fatal(err)
				}
			} else if p.ProAuto.Status == "failed" {
				if err := s.autoProUpdate(email, p.ProAuto.Stage, "failed", "secret-fixture"); err != nil {
					t.Fatal(err)
				}
			}
			if got := proNotificationKinds(t, s); got != "" {
				t.Fatal("duplicate notice on saved state or quota resume", got)
			}
		})
	}
}
