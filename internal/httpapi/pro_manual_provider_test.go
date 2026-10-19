package httpapi

import (
	"chatgpt-space-merge/internal/ejh"
	"context"
	"fmt"
	"net/http"
	"sync/atomic"
	"testing"

	"chatgpt-space-merge/internal/model"
)

func TestManualEJHIssuanceUsesOwnTemplateAndBudget(t *testing.T) {
	for _, budget := range []string{"19.99", "20"} {
		t.Run(budget, func(t *testing.T) {
			s, calls := ejhTestServer(t, 200)
			cfg, _, _ := s.store.EJHSettings()
			cfg.Templates = []ejh.Template{{ID: "manual-ejh", Name: "fixture", ValidDays: 30, OpenInput: ejh.OpenInput{CardType: "fixture", CardAmount: "20", CardCurrency: "USD", BillCurrency: "USD", UseTime: 1, MCC: "ecom"}}}
			if _, err := s.store.SaveEJHSettings(cfg, ""); err != nil {
				t.Fatal(err)
			}
			p := model.DefaultProSettings()
			p.CardProvider, p.EJHAutoOpen, p.EJHTemplateID = "nexovcc", true, "manual-ejh"
			p.EJHDailyOpenLimit, p.EJHDailyAmountLimit = 1, budget
			if _, err := s.store.SaveProSettings(p, "", ""); err != nil {
				t.Fatal(err)
			}
			ctx := context.WithValue(context.Background(), proManualCardProviderKey{}, "ejh")
			card, err := s.ensureProviderCard(ctx)
			if budget == "19.99" {
				if err == nil || calls.Load() != 0 {
					t.Fatal("budget bypass", err, calls.Load())
				}
			} else if err != nil || card.Source != "ejh" || !card.Available || calls.Load() != 1 {
				t.Fatal("manual EJH issuance", card.ID, err, calls.Load())
			}
		})
	}
}

func TestManualProCanSelectEitherIssuerRegardlessOfWindow(t *testing.T) {
	for _, window := range []string{"none", "global", "blocked"} {
		for _, issuer := range []string{"ejh", "nexovcc"} {
			t.Run(window+"/"+issuer, func(t *testing.T) {
				var payments, pushes atomic.Int32
				s, f, email, c := restockWindowIntegration(t, func(w http.ResponseWriter, r *http.Request) {
					payments.Add(1)
					fmt.Fprint(w, `{"code":0,"data":{"id":"manual-selected","status":"success"}}`)
				})
				p, _, _, _ := s.store.ProSettings()
				p.CardProvider = "ejh"
				cardID := ""
				if issuer == "ejh" {
					p.CardProvider = "nexovcc"
					cardID = "test-card"
				}
				if _, err := s.store.SaveProSettings(p, "", ""); err != nil {
					t.Fatal(err)
				}
				if window == "none" {
					c.NoRestockEnabled = false
				} else if window == "blocked" {
					c.NoRestockWindows[0].AllowedProvider = ""
				}
				if _, err := s.store.SaveProRestockConfig(c); err != nil {
					t.Fatal(err)
				}
				s.proAutoHooks = &proAutomationHooks{
					Session: func(ctx context.Context, current string, rpc func(string, map[string]any) (any, error)) error {
						profile, _, _ := s.store.MailAccountCredential(current)
						card, _, err := s.store.GPTPayCard(profile.ProAuto.CardID)
						if err != nil || (issuer == "nexovcc" && card.Source != "nexovcc") || (issuer == "ejh" && card.ID != "test-card") {
							t.Error("manual choice ignored", issuer, card.ID, card.Source, err)
						}
						if err = windowRecharge(s, current, rpc); err != nil {
							return err
						}
						_, err = rpc("pro_tokens", map[string]any{"tokens": map[string]any{"access_token": proAutoToken(current, "new"), "refresh_token": "fixture-rt"}})
						return err
					},
					Push: func(_ context.Context, current string) (model.MailAccountProfile, error) {
						pushes.Add(1)
						return s.store.UpdateProAccount(current, func(p *model.MailAccountProfile) {
							p.PushStatus, p.PushProvider, p.Sub2AccountID = "completed", "sub2", 88
						})
					},
					Quota: func(_ context.Context, current string, _ bool) (model.MailAccountProfile, error) {
						return s.store.UpdateProAccount(current, func(p *model.MailAccountProfile) { p.Quota7D = &model.FreeQuotaWindow{UsedPercent: 10} })
					},
				}
				pay, _, _ := s.store.GPTPaySettings()
				w := payCall(t, s.startProAutomation, "email", email, map[string]any{"provider": pay.Provider, "plan_code": pay.PlanCode, "card_id": cardID, "card_provider": issuer})
				if w.Code != http.StatusAccepted {
					t.Fatal(w.Code, w.Body.String())
				}
				profile := waitProAutoFinished(t, s, email)
				wantOpens := int32(0)
				if issuer == "nexovcc" {
					wantOpens = 1
				}
				if payments.Load() != 1 || pushes.Load() != 1 || f.opens.Load() != wantOpens || profile.ProAuto.Status != "waiting_quota" {
					t.Fatal(profile.ProAuto, payments.Load(), pushes.Load(), f.opens.Load())
				}
				after, _, _, _ := s.store.ProSettings()
				if after.CardProvider != p.CardProvider {
					t.Fatal("manual choice overwrote default")
				}
			})
		}
	}
}
