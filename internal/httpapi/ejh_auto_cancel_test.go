package httpapi

import (
	"context"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"chatgpt-space-merge/internal/ejh"
	"chatgpt-space-merge/internal/gptpay"
	"chatgpt-space-merge/internal/store"
)

func ejhAutoCancelAPIFixture(t *testing.T) (*Server, ejh.CardRecord, []ejh.Trade) {
	t.Helper()
	s, _ := ejhTestServer(t, 200)
	cfg, _, _ := s.store.EJHSettings()
	cfg.AutoCancelEnabled = true
	cfg.DefaultMaxAccounts = 1
	if _, err := s.store.SaveEJHSettings(cfg, ""); err != nil {
		t.Fatal(err)
	}
	remote := ejh.Card{OrderNo: "auto-issuer", Number: "4242424242424242", CVV: "123", Validity: "1299", Status: "1", CardCurrency: "USD"}
	card, err := s.store.ImportEJHCard(remote, true)
	if err != nil {
		t.Fatal(err)
	}
	email := "auto-recycle@example.com"
	allocationAccount(t, s, email)
	_, secret, err := s.store.GPTPayCard(card.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.store.CreateGPTPayOrder(gptpay.Order{ID: "auto-payment", Email: email, CardID: card.ID, Status: "success", CreatedAt: time.Now()}, gptpay.Snapshot{Input: gptpay.CreateInput{CardSecret: secret}}); err != nil {
		t.Fatal(err)
	}
	p := ejh.Trade{OrderNo: remote.OrderNo, TradeNo: "original", Type: "1", Status: "2", CardAmount: "20", CardCurrency: "USD"}
	r := p
	r.TradeNo = "return"
	r.Type = "5"
	r.BillAmount = "18.8"
	r.BillCurrency = "USD"
	if err = s.store.EJHSaveTrades(card.ID, []ejh.Trade{p, r}); err != nil {
		t.Fatal(err)
	}
	if err = s.store.EJHMatchTrade(store.EJHID("trade", r.OrderNo+"|return|5"), "auto-payment", store.EJHID("trade", p.OrderNo+"|original|1"), "20", "USD", "fixture refund", true); err != nil {
		t.Fatal(err)
	}
	var saved ejh.CardRecord
	if err = s.store.EJHGet("card", card.ID, &saved); err != nil {
		t.Fatal(err)
	}
	if err = s.store.EJHAutoCancelCheck(card.ID); err != nil {
		t.Fatal("fixture eligibility", err)
	}
	return s, saved, []ejh.Trade{p, r}
}

func TestEJHAutoCancelRefreshAndSingleSubmission(t *testing.T) {
	for _, scenario := range []string{"completed", "timeout", "processing", "pending_category", "closed_card", "disabled", "query_error", "wrong_card", "empty_snapshot", "missing_refund", "changed_amount", "raised_limit", "switch_off_during_check", "issuer_closing", "issuer_busy"} {
		t.Run(scenario, func(t *testing.T) {
			s, card, trades := ejhAutoCancelAPIFixture(t)
			remote := card.Remote
			var methods []string
			submitted := false
			s.ejh.client.HTTP = ejhFixtureTransport(t, func(method string, data map[string]any) any {
				methods = append(methods, method)
				if data["orderNo"] != remote.OrderNo {
					t.Fatal("wrong card request")
				}
				switch method {
				case "card.query.info":
					v := remote
					if scenario == "query_error" {
						return &ejh.APIError{Code: "fixture_error"}
					}
					if scenario == "wrong_card" {
						v.OrderNo = "other"
					}
					if scenario == "issuer_closing" {
						v.Status = "8"
					}
					if scenario == "raised_limit" || scenario == "switch_off_during_check" {
						cfg, _, _ := s.store.EJHSettings()
						if scenario == "raised_limit" {
							cfg.DefaultMaxAccounts = 2
						} else {
							cfg.AutoCancelEnabled = false
						}
						if _, err := s.store.SaveEJHSettings(cfg, ""); err != nil {
							t.Fatal(err)
						}
					}
					if submitted && scenario == "completed" {
						v.Status = "2"
					}
					if submitted && scenario == "processing" {
						v.Status = "8"
					}
					return v
				case "card.trade.info":
					rows := append([]ejh.Trade{}, trades...)
					if scenario == "empty_snapshot" {
						rows = []ejh.Trade{}
					}
					if scenario == "missing_refund" {
						rows = rows[:1]
					}
					if scenario == "changed_amount" {
						rows[1].CardAmount = "10"
					}
					return map[string]any{"tradeInfos": rows}
				case "card.cancel.info":
					if submitted {
						t.Fatal("duplicate cancellation")
					}
					submitted = true
					if scenario == "timeout" {
						return &ejh.APIError{Code: "timeout"}
					}
					return map[string]any{"orderNo": remote.OrderNo, "status": "SUCCESS"}
				default:
					t.Fatal("unexpected endpoint", method)
					return nil
				}
			})
			switch scenario {
			case "pending_category":
				if _, err := s.store.SetEJHCardNormalPending(card.ID, true); err != nil {
					t.Fatal(err)
				}
			case "closed_card":
				card.Remote.Status = "2"
				if err := s.store.EJHPut("card", card.ID, card); err != nil {
					t.Fatal(err)
				}
			case "disabled":
				cfg, _, _ := s.store.EJHSettings()
				cfg.AutoCancelEnabled = false
				if _, err := s.store.SaveEJHSettings(cfg, ""); err != nil {
					t.Fatal(err)
				}
			case "issuer_busy":
				s.ejh.mu.Lock()
			}
			s.tickEJHAutoCancel(context.Background())
			if scenario == "issuer_busy" {
				s.ejh.mu.Unlock()
			}
			want := scenario == "completed" || scenario == "timeout" || scenario == "processing"
			if submitted != want {
				var state ejhAutoCancelState
				_ = s.store.EJHGet("auto_cancel", "latest", &state)
				t.Fatalf("submitted=%v methods=%v state=%+v", submitted, methods, state)
			}
			if scenario == "pending_category" || scenario == "closed_card" || scenario == "disabled" || scenario == "issuer_busy" {
				if len(methods) > 0 {
					t.Fatal("excluded card requested", methods)
				}
				return
			}
			before := len(methods)
			s.tickEJHAutoCancel(context.Background())
			if len(methods) != before {
				t.Fatal("ignored interval")
			}
			// A new runtime uses the persisted clock instead of restarting the schedule.
			restarted := &Server{store: s.store}
			restarted.ejh.client = s.ejh.client
			restarted.tickEJHAutoCancel(context.Background())
			if len(methods) != before {
				t.Fatal("restart ignored interval")
			}
			if !want {
				return
			}
			past := time.Now().Add(-time.Hour)
			if err := s.store.EJHPut("auto_cancel", "latest", ejhAutoCancelState{LastAttempt: &past}); err != nil {
				t.Fatal(err)
			}
			restarted.tickEJHAutoCancel(context.Background())
			if len(methods) != before {
				t.Fatal("persisted operation was resubmitted", methods)
			}
			var op ejh.Operation
			if err := s.store.EJHGet("operation", store.EJHID("auto-cancel", card.ID), &op); err != nil {
				t.Fatal(err)
			}
			wantStatus := map[string]string{"completed": "completed", "timeout": "unknown", "processing": "processing"}[scenario]
			if op.Status != wantStatus || !op.Automatic {
				t.Fatal(op.Status)
			}
			if scenario == "processing" || scenario == "timeout" {
				op.NextCheckAt = &past
				if err := s.store.EJHPut("operation", op.ID, op); err != nil {
					t.Fatal(err)
				}
				restarted.tickEJHCancellations(context.Background())
				if methods[len(methods)-1] != "card.query.info" {
					t.Fatal("monitor must only query")
				}
			}
			w := httptest.NewRecorder()
			s.ejhDashboard(w, httptest.NewRequest("GET", "/", nil))
			if w.Code != 200 || strings.Contains(w.Body.String(), remote.Number) {
				t.Fatal("dashboard leaked card or failed")
			}
		})
	}
}

func TestEJHAutoCancelSettingsLegacyClient(t *testing.T) {
	s, _, _ := ejhAutoCancelAPIFixture(t)
	cfg, _, _ := s.store.EJHSettings()
	body := fmt.Sprintf(`{"enabled":true,"url":%q,"app_key":%q,"default_max_accounts":1,"sync_interval_seconds":300,"alert_hours":24}`, cfg.URL, cfg.AppKey)
	w := httptest.NewRecorder()
	s.saveEJHSettings(w, httptest.NewRequest("PUT", "/", strings.NewReader(body)))
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	cfg, _, _ = s.store.EJHSettings()
	if !cfg.AutoCancelEnabled || cfg.AutoCancelIntervalSeconds != 300 {
		t.Fatal("legacy client lost settings")
	}
}

func TestEJHAutoCancellationDoesNotConsumeOpeningBudget(t *testing.T) {
	for _, status := range []string{"completed", "failed", "processing", "unknown"} {
		t.Run(status, func(t *testing.T) {
			s, issued, paid, _ := allocationFixture(t)
			pro, _, _, err := s.store.ProSettings()
			if err != nil {
				t.Fatal(err)
			}
			pro.EJHDailyOpenLimit = 1
			pro.EJHDailyAmountLimit = "20"
			if _, err = s.store.SaveProSettings(pro, "", ""); err != nil {
				t.Fatal(err)
			}
			op := ejh.Operation{ID: "historical-auto-close", Method: "cancel", Automatic: true, Status: status, CreatedAt: time.Now(), UpdatedAt: time.Now()}
			if err = s.store.EJHPut("operation", op.ID, op); err != nil {
				t.Fatal(err)
			}
			if _, err = s.ensureEJHCard(context.Background()); err != nil || issued.Load() != 1 || paid.Load() != 0 {
				t.Fatalf("cancellation affected opening budget/cooldown: issued=%d paid=%d error=%v", issued.Load(), paid.Load(), err)
			}
		})
	}
}
