package httpapi

import (
	"chatgpt-space-merge/internal/model"
	"chatgpt-space-merge/internal/nexovcc"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestBalanceFallbackEJHRejectedToNexovccWithoutDuplicate(t *testing.T) {
	for _, pending := range []bool{false, true} {
		t.Run(map[bool]string{false: "complete", true: "import_pending"}[pending], func(t *testing.T) {
			s, issued, _, _ := allocationFixture(t)
			other, f := nexFixtureServer(t)
			f.failDetail = pending
			ncfg, key, _ := other.store.NexovccSettings()
			ncfg.Templates[0].PlanCode = "pro20"
			if _, err := s.store.SaveNexovccSettings(ncfg, key, false); err != nil {
				t.Fatal(err)
			}
			s.nexovcc.transport = func(ctx context.Context, r nexovcc.Request) (nexovcc.Response, error) {
				response, err := other.nexovcc.transport(ctx, r)
				response.Body = strings.ReplaceAll(response.Body, "4242424242424242", "4000000000000002")
				return response, err
			}
			cfg, _, _, _ := s.store.ProSettings()
			cfg.CardProvider = "ejh"
			cfg.BalanceFallbackEnabled = true
			cfg.NexovccAutoOpen = true
			cfg.NexovccTemplateID = "template"
			cfg.NexovccPlanTemplates = map[string]string{"pro20": "template"}
			cfg.NexovccDailyOpenLimit = 10
			cfg.NexovccDailyAmountLimit = "100.00"
			if _, err := s.store.SaveProSettings(cfg, "", ""); err != nil {
				t.Fatal(err)
			}
			s.ejh.client.HTTP.Transport = ejhBalanceTransport(func(r *http.Request) (*http.Response, error) {
				body := `{"code":"100004","msg":"balance too low"}`
				if strings.HasSuffix(r.URL.Path, "/authenticate") {
					body = `{"code":"000000","data":{"authToken":"local-only","validity":3600000}}`
				}
				return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}, nil
			})
			pay, _, _ := s.store.GPTPaySettings()
			pay.PlanCode = "pro20"
			ctx := context.WithValue(context.Background(), proPaymentConfigKey{}, pay)
			card, _, err := s.ensureProviderCardWithFallback(ctx)
			if f.opens.Load() != 1 || issued.Load() != 0 {
				t.Fatal("wrong issuance count", f.opens.Load(), issued.Load(), err)
			}
			if !pending && (err != nil || card.Source != "nexovcc") {
				t.Fatal(card, err)
			}
			if pending {
				if err == nil {
					t.Fatal("missing import pending")
				}
				_, _, _ = s.ensureProviderCardWithFallback(ctx)
				if f.opens.Load() != 1 {
					t.Fatal("pending card duplicated")
				}
			}
		})
	}
}

func TestBalanceFallbackUsesOtherIssuerSamePlanAndHonorsWindow(t *testing.T) {
	for _, mode := range []string{"enabled", "disabled", "window_off", "unknown", "stop"} {
		t.Run(mode, func(t *testing.T) {
			s, issued, _, _ := allocationFixture(t)
			other, f := nexFixtureServer(t)
			cfg, k, _ := other.store.NexovccSettings()
			cfg.Templates[0].PlanCode = "pro20"
			if _, e := s.store.SaveNexovccSettings(cfg, k, false); e != nil {
				t.Fatal(e)
			}
			ec, key, _ := s.store.EJHSettings()
			ec.Templates[0].PlanCode = "pro20"
			if _, e := s.store.SaveEJHSettings(ec, key); e != nil {
				t.Fatal(e)
			}
			p, _, _, _ := s.store.ProSettings()
			p.CardProvider = "nexovcc"
			p.NexovccAutoOpen = true
			p.NexovccTemplateID = "template"
			p.NexovccDailyOpenLimit = 10
			p.NexovccDailyAmountLimit = "100.00"
			p.BalanceFallbackEnabled = mode != "disabled"
			p.EJHPlanTemplates = map[string]string{"pro20": "allocation"}
			p.NexovccPlanTemplates = map[string]string{"pro20": "template"}
			if _, e := s.store.SaveProSettings(p, "", ""); e != nil {
				t.Fatal(e)
			}
			base := other.nexovcc.transport
			s.nexovcc.transport = func(ctx context.Context, r nexovcc.Request) (nexovcc.Response, error) {
				if r.Path == "/api/getBalanceInfo" && mode != "unknown" {
					return nexReply([]any{map[string]any{"money_id": 3, "balance": "0.00", "freeze_balance": "0.00"}}), nil
				}
				return base(ctx, r)
			}
			f.unknown = mode == "unknown"
			ctx := context.Background()
			if mode == "window_off" {
				ctx = context.WithValue(ctx, proPurchaseContextKey{}, &model.ProPurchaseRoute{CardProvider: "nexovcc", TemplateID: "template", PlanCode: "pro20", BalanceFallbackEnabled: false})
			}
			checks := []func() error{}
			if mode == "stop" {
				checks = append(checks, func() error { return context.Canceled })
			}
			card, result, e := s.ensureProviderCardWithFallback(ctx, checks...)
			if mode == "enabled" {
				if e != nil || card.Source != "ejh" || issued.Load() != 1 || f.opens.Load() != 0 {
					t.Fatal("fallback issuance mismatch", card, e, issued.Load(), f.opens.Load())
				}
				r, _ := result.Value(proBalanceFallbackKey{}).(*model.ProPurchaseRoute)
				if r == nil || r.TemplateID != "allocation" || r.PlanCode != "pro20" {
					t.Fatal("wrong fallback plan", r)
				}
			} else {
				if e == nil || issued.Load() != 0 {
					t.Fatal("unsafe backup issuance", mode, e, issued.Load())
				}
				if mode == "disabled" && !errors.Is(e, errIssuerInsufficientBalance) {
					t.Fatal(e)
				}
			}
		})
	}
}
func TestBalanceFallbackTimetableKeepsDistinctWindowRules(t *testing.T) {
	p, pay, c := timetableFixture()
	c.NoRestockEnabled = false
	p.PurchaseWindows = []model.ProPurchaseWindow{{Start: "00:00", End: "12:00", CardProvider: "nexovcc", PlanCode: "pro20", BalanceFallbackEnabled: true, BalanceFallbackProvider: "ejh"}, {Start: "12:00", End: "00:00", CardProvider: "nexovcc", PlanCode: "pro20"}}
	now := time.Date(2026, 10, 27, 11, 0, 0, 0, time.FixedZone("CST", 8*3600))
	table := proRestockDailyTimetable(p, pay, c, now)
	if len(table.Periods) != 2 || !table.Current.BalanceFallbackEnabled || table.Current.BalanceFallbackProvider != "ejh" {
		t.Fatal(table)
	}
	table = proRestockDailyTimetable(p, pay, c, now.Add(time.Hour))
	if table.Current.BalanceFallbackEnabled {
		t.Fatal("fallback leaked across window")
	}
}

func TestBalanceFallbackNexovccReserveRejection(t *testing.T) {
	for _, mode := range []string{"reserve", "insufficient", "other_rejection", "http_unknown"} {
		t.Run(mode, func(t *testing.T) {
			s, issued, _, _ := allocationFixture(t)
			other, _ := nexFixtureServer(t)
			ncfg, key, _ := other.store.NexovccSettings()
			ncfg.Templates[0].PlanCode = "pro20"
			if _, err := s.store.SaveNexovccSettings(ncfg, key, false); err != nil {
				t.Fatal(err)
			}
			ec, ek, _ := s.store.EJHSettings()
			ec.Templates[0].PlanCode = "pro20"
			if _, err := s.store.SaveEJHSettings(ec, ek); err != nil {
				t.Fatal(err)
			}
			p, _, _, _ := s.store.ProSettings()
			p.CardProvider, p.NexovccAutoOpen, p.NexovccTemplateID = "nexovcc", true, "template"
			p.NexovccDailyOpenLimit, p.NexovccDailyAmountLimit = 10, "100.00"
			p.BalanceFallbackEnabled = true
			p.EJHPlanTemplates = map[string]string{"pro20": "allocation"}
			if _, err := s.store.SaveProSettings(p, "", ""); err != nil {
				t.Fatal(err)
			}
			creates := 0
			s.nexovcc.transport = func(ctx context.Context, r nexovcc.Request) (nexovcc.Response, error) {
				if r.Path == "/api/card/createUserCard" {
					creates++
					switch mode {
					case "reserve":
						return nexovcc.Response{Status: 200, Body: `{"code":500,"message":"账户余额强制预留: 20 USD, 请先充值"}`}, nil
					case "insufficient":
						return nexovcc.Response{Status: 200, Body: `{"code":500,"message":"账户余额不足"}`}, nil
					case "other_rejection":
						return nexovcc.Response{Status: 200, Body: `{"code":500,"message":"产品不可用"}`}, nil
					default:
						return nexovcc.Response{Status: 500}, nil
					}
				}
				return other.nexovcc.transport(ctx, r)
			}
			card, _, err := s.ensureProviderCardWithFallback(context.Background())
			if creates != 1 {
				t.Fatal("primary issuance repeated", creates)
			}
			ops, _ := s.store.NexovccOperations()
			if len(ops) != 1 || ops[0].Serial != "" || ops[0].CardID != "" {
				t.Fatal(ops)
			}
			if mode == "reserve" || mode == "insufficient" {
				if err != nil || card.Source != "ejh" || issued.Load() != 1 || ops[0].Status != "failed" {
					t.Fatal(card, err, issued.Load(), ops)
				}
			} else if err == nil || issued.Load() != 0 {
				t.Fatal("unsafe fallback", err, issued.Load())
			}
		})
	}
}
