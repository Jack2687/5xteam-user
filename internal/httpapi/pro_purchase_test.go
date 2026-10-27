package httpapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"chatgpt-space-merge/internal/model"
	"chatgpt-space-merge/internal/restock"
)

func TestProPurchaseDefaultRejectsOldPlanBeforeReservation(t *testing.T) {
	for _, provider := range []string{"ejh", "nexovcc"} {
		t.Run(provider, func(t *testing.T) {
			var s *Server
			var email string
			if provider == "ejh" {
				s, _, _, email = allocationFixture(t)
			} else {
				s, _, email, _ = restockWindowIntegration(t, func(http.ResponseWriter, *http.Request) { t.Error("unexpected payment") })
			}
			st := s.store
			setPlan := func(plan string) {
				if provider == "ejh" {
					cfg, _, _ := st.EJHSettings()
					cfg.Templates[0].PlanCode = plan
					if _, err := st.SaveEJHSettings(cfg, ""); err != nil {
						t.Fatal(err)
					}
				} else {
					cfg, credentials, _ := st.NexovccSettings()
					cfg.Templates[0].PlanCode = plan
					if _, err := st.SaveNexovccSettings(cfg, credentials, false); err != nil {
						t.Fatal(err)
					}
				}
			}
			p, _, _, _ := st.ProSettings()
			p.CardProvider, p.PurchaseScheduleEnabled = provider, false
			if _, err := st.SaveProSettings(p, "", ""); err != nil {
				t.Fatal(err)
			}
			setPlan("pro5")
			old, err := s.ensureProviderCard(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			setPlan("pro20")
			pay, _, _ := st.GPTPaySettings()
			selected, _, err := s.resolveProPurchase(p, pay, restock.DefaultConfig(), time.Now())
			if err != nil || selected.PurchaseRoute == nil {
				t.Fatal("default route missing", err)
			}
			cards, err := s.automaticProCards(selected, true)
			if err != nil || len(cards) != 0 {
				t.Fatal("old 5x card offered for 20x", len(cards), err)
			}
			ctx := context.WithValue(context.Background(), proPurchaseContextKey{}, selected.PurchaseRoute)
			if _, _, err = s.reserveProStartCard(ctx, email, old.ID, false, false); err == nil || !strings.Contains(err.Error(), "模板") {
				t.Fatal("mismatched card reserved")
			}
			policy, err := st.ProRestockConfig()
			if err != nil {
				t.Fatal(err)
			}
			policy.NoRestockEnabled = false
			policy.Enabled, policy.Mode, policy.GroupID = true, "auto", 1
			policy, err = st.SaveProRestockConfig(policy)
			if err != nil {
				t.Fatal(err)
			}
			now := time.Now()
			tasks, err := st.ReserveProRestock(restock.Decision{ID: "default-template-guard", At: now, Version: policy.Version, Suggested: 1, Snapshot: restock.Snapshot{NewPlan: restockPlan(pay.Provider, pay.PlanCode, pay.Country, pay.CreditQuantity)}}, "", 0)
			if err != nil || len(tasks) != 1 {
				t.Fatal("reserve restock intent", err)
			}
			task := tasks[0]
			task.Status, task.SubmittedAt = "submitting", &now
			if err = st.ClaimProRestockDispatch(task); err != nil {
				t.Fatal(err)
			}
			if err = st.ReserveProCard(task.Email, old.ID, false, task.ID); err == nil || !strings.Contains(err.Error(), "模板") {
				t.Fatal("transaction allowed wrong template", err)
			}
			c, _, err := st.GPTPayCard(old.ID)
			if err != nil || c.PendingAccounts != 0 {
				t.Fatal("rejected card retained reservation", err)
			}
		})
	}
}

func activePurchaseWindow(provider, plan string) model.ProPurchaseWindow {
	now := time.Now().In(time.FixedZone("CST", 8*3600))
	return model.ProPurchaseWindow{Start: now.Add(-time.Hour).Format("15:04"), End: now.Add(time.Hour).Format("15:04"), CardProvider: provider, PlanCode: plan}
}

func TestProPurchaseScheduledAndRestockFullFlow(t *testing.T) {
	for _, provider := range []string{"ejh", "nexovcc"} {
		for _, trigger := range []string{"restock", "scheduled", "restock-default", "scheduled-default"} {
			for _, plan := range []string{"pro5", "pro20"} {
				t.Run(provider+"/"+trigger+"/"+plan, func(t *testing.T) {
					var s *Server
					var email string
					var c restock.Config
					if provider == "nexovcc" {
						s, _, email, c = restockWindowIntegration(t, func(w http.ResponseWriter, r *http.Request) {
							fmt.Fprint(w, `{"code":0,"data":{"id":"routed-paid","status":"success"}}`)
						})
						cfg, credentials, _ := s.store.NexovccSettings()
						cfg.Templates[0].PlanCode = plan
						if _, err := s.store.SaveNexovccSettings(cfg, credentials, false); err != nil {
							t.Fatal(err)
						}
					} else {
						s, _, _, email = allocationFixture(t)
						cfg, _, _ := s.store.EJHSettings()
						cfg.Templates[0].PlanCode = plan
						if _, err := s.store.SaveEJHSettings(cfg, ""); err != nil {
							t.Fatal(err)
						}
						c = restock.DefaultConfig()
						c.Enabled, c.Mode, c.GroupID = true, "auto", 1
						c.CapacityPercent, c.Confirmations = 100, 1
						c.TimeEnabled = false
						c.MinBatch, c.MaxBatch, c.MaxInflight = 1, 1, 2
					}
					st := s.store
					c.NoRestockEnabled = false
					var err error
					c, err = st.SaveProRestockConfig(c)
					if err != nil {
						t.Fatal(err)
					}
					p, _, _, _ := st.ProSettings()
					p.PurchaseScheduleEnabled = true
					p.PurchaseWindows = []model.ProPurchaseWindow{activePurchaseWindow(provider, plan)}
					p.PurchaseWindows[0].ExtraSub2GroupIDs = []int64{15, 1, 15}
					groups := []int64{1, 15}
					if strings.HasSuffix(trigger, "-default") {
						groups = []int64{1}
					}
					remote, pushes := purchaseSub2Fixture(t, groups)
					p.Sub2.URL = remote.URL
					p.EJHPlanTemplates = map[string]string{plan: "allocation"}
					p.NexovccPlanTemplates = map[string]string{plan: "template"}
					p.Concurrency = 1
					if provider == "ejh" {
						p.CardProvider = "nexovcc"
					} else {
						p.CardProvider = "ejh"
					}
					expectedGroups := []int64{1, 15}
					if strings.HasSuffix(trigger, "-default") {
						p.PurchaseScheduleEnabled = false
						p.CardProvider = provider
						expectedGroups = []int64{1}
						payment, _, _ := st.GPTPaySettings()
						payment.PlanCode = plan
						if _, err = st.SaveGPTPaySettings(payment, ""); err != nil {
							t.Fatal(err)
						}
					}
					if _, err = st.SaveProSettings(p, "", ""); err != nil {
						t.Fatal(err)
					}
					pay, _, _ := st.GPTPaySettings()
					originalPlan := pay.PlanCode
					s.proRestock.monitor = func(context.Context, restock.Config, model.ProSettings, string) (restock.Snapshot, error) {
						now := time.Now()
						return restock.Snapshot{At: now, CollectionStartedAt: now, Valid: true, LoadValid: true, GroupID: 1, Binding: restockBinding(p), NewPlan: restockPlan(pay.Provider, pay.PlanCode, pay.Country, pay.CreditQuantity), Accounts: []restock.Account{{ID: 1, Ready: true, Capacity: 10, Used: 9}}}, nil
					}
					s.proAutoHooks = &proAutomationHooks{Session: func(ctx context.Context, current string, rpc func(string, map[string]any) (any, error)) error {
						// Editing the rules after launch must not change the issued card or payment.
						live, _, _, e := st.ProSettings()
						if e != nil {
							return e
						}
						live.PurchaseScheduleEnabled = false
						live.PurchaseWindows[0].ExtraSub2GroupIDs = []int64{99}
						if _, e = st.SaveProSettings(live, "", ""); e != nil {
							return e
						}
						if e = windowRecharge(s, current, rpc); e != nil {
							return e
						}
						_, e = rpc("pro_tokens", map[string]any{"tokens": map[string]any{"access_token": proAutoToken(current, "new"), "refresh_token": "fixture-rt"}})
						return e
					}, Push: s.performProPush, Quota: func(context.Context, string, bool) (model.MailAccountProfile, error) {
						return model.MailAccountProfile{}, errors.New("fixture stops at quota")
					}}
					if strings.HasPrefix(trigger, "restock") {
						windowTick(t, s)
					} else {
						s.tickProSchedule(true)
					}
					profile := waitProAutoFinished(t, s, email)
					if profile.ProAuto.Status != "waiting_quota" || profile.ProAuto.PlanCode != plan {
						t.Fatal("workflow plan", profile.ProAuto)
					}
					if pushes.Load() != 1 || !slices.Equal(profile.ProAuto.Sub2PushGroupIDs, expectedGroups) || profile.ProAuto.RestockBinding != restockBinding(p) {
						t.Fatal("push groups or monitoring binding changed", pushes.Load(), profile.ProAuto)
					}
					orders, _, err := st.GPTPayOrders(10, 0, email)
					if err != nil || len(orders) != 1 {
						t.Fatal(orders, err)
					}
					_, snapshot, err := st.GPTPayOrder(orders[0].ID)
					if err != nil || snapshot.Input.PlanCode != plan {
						t.Fatal("payment plan", snapshot.Input.PlanCode, err)
					}
					card, _, err := st.GPTPayCard(orders[0].CardID)
					if err != nil || card.Source != provider {
						t.Fatal("issuer", card.Source, err)
					}
					saved, _, _ := st.GPTPaySettings()
					if saved.PlanCode != originalPlan {
						t.Fatal("global payment changed")
					}
				})
			}
		}
	}
}

func TestProPurchaseTemplateMismatchAndExistingCards(t *testing.T) {
	s, _, _, _ := allocationFixture(t)
	st := s.store
	cfg, _, _ := st.EJHSettings()
	cfg.Templates[0].PlanCode = "pro5"
	if _, err := st.SaveEJHSettings(cfg, ""); err != nil {
		t.Fatal(err)
	}
	card, err := s.ensureEJHCard(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	p, _, _, _ := st.ProSettings()
	r := &model.ProPurchaseRoute{CardProvider: "ejh", TemplateID: "allocation", PlanCode: "pro5"}
	cards, err := s.automaticProCards(p.WithPurchaseRoute(r), true)
	if err != nil || len(cards) != 1 || cards[0].ID != card.ID {
		t.Fatal(cards, err)
	}
	r.PlanCode = "pro20"
	if s.validatePurchaseTemplate(r) == nil {
		t.Fatal("wrong classification allowed")
	}
	// Changing the template's label must not reclassify cards issued as 5x.
	cfg.Templates[0].PlanCode = "pro20"
	st.SaveEJHSettings(cfg, "")
	cards, err = s.automaticProCards(p.WithPurchaseRoute(r), true)
	if err != nil || len(cards) != 0 {
		t.Fatal("old 5x reused for 20x", cards, err)
	}
	r.TemplateID = "deleted"
	if s.validatePurchaseTemplate(r) == nil {
		t.Fatal("deleted template allowed")
	}
}

func TestProPurchaseConfigurationSaveAndClassification(t *testing.T) {
	s, _, _, _ := allocationFixture(t)
	cfg, _, _ := s.store.EJHSettings()
	cfg.Templates[0].PlanCode = "pro20"
	if _, err := s.store.SaveEJHSettings(cfg, ""); err != nil {
		t.Fatal(err)
	}
	p, _, _, _ := s.store.ProSettings()
	p.PurchaseScheduleEnabled = true
	p.PurchaseWindows = []model.ProPurchaseWindow{activePurchaseWindow("ejh", "pro20")}
	p.EJHPlanTemplates = map[string]string{"pro20": "allocation"}
	pay, _, _ := s.store.GPTPaySettings()
	w := payCall(t, s.saveProAutomationSettings, "", "", map[string]any{"automation": p, "gptpay": pay})
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	saved, _, _, _ := s.store.ProSettings()
	if !saved.PurchaseScheduleEnabled || len(saved.PurchaseWindows) != 1 || saved.EJHPlanTemplates["pro20"] != "allocation" {
		t.Fatal("configuration lost", saved)
	}
	p.EJHPlanTemplates["pro5"] = "allocation"
	w = payCall(t, s.saveProAutomationSettings, "", "", map[string]any{"automation": p, "gptpay": pay})
	if w.Code != 400 {
		t.Fatal("mismatched template accepted", w.Code)
	}
	saved, _, _, _ = s.store.ProSettings()
	if saved.EJHPlanTemplates["pro5"] != "" {
		t.Fatal("failed save mutated configuration")
	}
}
