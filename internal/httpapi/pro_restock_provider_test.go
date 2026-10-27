package httpapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"chatgpt-space-merge/internal/ejh"
	"chatgpt-space-merge/internal/gptpay"
	"chatgpt-space-merge/internal/model"
	"chatgpt-space-merge/internal/nexovcc"
	"chatgpt-space-merge/internal/restock"
)

func providerWindow(t *testing.T, s *Server, provider string) restock.Config {
	t.Helper()
	c := restock.DefaultConfig()
	c.Enabled, c.Mode, c.GroupID = true, "auto", 1
	c.NoRestockEnabled = true
	now := time.Now().In(time.FixedZone("CST", 8*3600))
	c.NoRestockWindows = []restock.DailyWindow{{Start: now.Add(-time.Hour).Format("15:04"), End: now.Add(time.Hour).Format("15:04"), AllowedProvider: provider}}
	c, err := s.store.SaveProRestockConfig(c)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func providerClaim(t *testing.T, s *Server, provider string) (context.Context, restock.Config, restock.Task) {
	t.Helper()
	email := "window@example.com"
	allocationAccount(t, s, email)
	c := providerWindow(t, s, provider)
	now := time.Now()
	pay, _, err := s.store.GPTPaySettings()
	if err != nil {
		t.Fatal(err)
	}
	tasks, err := s.store.ReserveProRestock(restock.Decision{ID: "window-claim", At: now, Version: c.Version, Suggested: 1, Snapshot: restock.Snapshot{NewPlan: restockPlan(pay.Provider, pay.PlanCode, pay.Country, pay.CreditQuantity)}}, "", 0)
	if err != nil || len(tasks) != 1 {
		t.Fatal(tasks, err)
	}
	task := tasks[0]
	task.Status, task.SubmittedAt = "submitting", &now
	if err = s.store.ClaimProRestockDispatch(task); err != nil {
		t.Fatal(err)
	}
	return context.WithValue(context.Background(), proRestockContextKey{}, task.ID), c, task
}

func TestProRestockWindowNexovccOpenUsesOwnBudgetAndPinnedIssuer(t *testing.T) {
	for _, mode := range []string{"success", "budget", "disabled", "invalid-claim"} {
		t.Run(mode, func(t *testing.T) {
			s, f := nexFixtureServer(t)
			p := model.DefaultProSettings()
			p.CardProvider = "ejh"
			p.EJHAutoOpen = false
			p.NexovccAutoOpen = mode != "disabled"
			p.NexovccTemplateID = "template"
			p.NexovccDailyOpenLimit = 1
			p.NexovccDailyAmountLimit = "10.00"
			if mode == "budget" {
				p.NexovccDailyAmountLimit = "9.99"
			}
			if _, _, err := s.store.SaveProAutomationSettings(p, gptpay.Settings{}, ""); err != nil {
				t.Fatal(err)
			}
			ctx, c, task := providerClaim(t, s, "nexovcc")
			if _, err := s.store.SaveGPTPayCard(gptpay.Card{ID: "other", Enabled: true}, gptpay.CardSecret{Number: "5555555555554444", CVV: "123", Month: 12, Year: 2099}); err != nil {
				t.Fatal(err)
			}
			if mode == "invalid-claim" {
				ctx = context.WithValue(ctx, proRestockContextKey{}, "not-a-task")
			}
			// Already claimed work may finish after the window has become fully blocked.
			c.NoRestockWindows[0].AllowedProvider = ""
			if _, err := s.store.SaveProRestockConfig(c); err != nil {
				t.Fatal(err)
			}
			card, err := s.ensureProviderCard(ctx)
			if mode != "success" {
				if err == nil || f.opens.Load() != 0 {
					t.Fatal("guard bypassed", mode, card, err)
				}
				return
			}
			if err != nil || card.Source != "nexovcc" || f.opens.Load() != 1 {
				t.Fatal(card, err)
			}
			if _, _, err = s.reserveProStartCard(ctx, task.Email, "other", true, false); err == nil {
				t.Fatal("wrong issuer accepted")
			}
			if err = s.store.ReserveProCard(task.Email, "other", false, task.ID); err == nil {
				t.Fatal("store admitted wrong issuer")
			}
			again, _, err := s.reserveProStartCard(ctx, task.Email, "", true, false)
			if err != nil || again.ID != card.ID || f.opens.Load() != 1 {
				t.Fatal("issued again instead of reusing original", err)
			}
			if _, err = s.ensureProviderCard(ctx); err == nil || !strings.Contains(err.Error(), "预算") || f.opens.Load() != 1 {
				t.Fatal("daily budget bypass", err)
			}
			actual, _, _, err := s.store.ProSettings()
			if err != nil || actual.CardProvider != "ejh" || actual.EJHAutoOpen {
				t.Fatal("global defaults changed")
			}
		})
	}
}

func TestProRestockWindowEJHOverrideIsStrict(t *testing.T) {
	s, opens := ejhTestServer(t, 200)
	cfg, _, _ := s.store.EJHSettings()
	cfg.Templates = []ejh.Template{{ID: "fixture", Name: "fixture", ValidDays: 30, OpenInput: ejh.OpenInput{CardType: "fixture", CardAmount: "20", CardCurrency: "USD", BillCurrency: "USD", UseTime: 1, MCC: "ecom"}}}
	if _, err := s.store.SaveEJHSettings(cfg, ""); err != nil {
		t.Fatal(err)
	}
	p := model.DefaultProSettings()
	p.CardProvider = "nexovcc"
	p.NexovccAutoOpen = false
	p.EJHAutoOpen = true
	p.EJHTemplateID = "fixture"
	p.EJHDailyOpenLimit = 1
	p.EJHDailyAmountLimit = "20"
	if _, err := s.store.SaveProSettings(p, "", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.store.SaveGPTPayCard(gptpay.Card{ID: "other", Enabled: true}, gptpay.CardSecret{Number: "5555555555554444", CVV: "123", Month: 12, Year: 2099}); err != nil {
		t.Fatal(err)
	}
	ctx, _, _ := providerClaim(t, s, "ejh")
	card, err := s.ensureProviderCard(ctx)
	if err != nil || card.Source != "ejh" || opens.Load() != 1 {
		t.Fatal("EJH exception selected other card or wrong template", card, err)
	}
	actual, _, _, _ := s.store.ProSettings()
	if actual.CardProvider != "nexovcc" {
		t.Fatal("default overwritten")
	}
}

func TestProRestockWindowDispatchRunsAutomationOnNexovcc(t *testing.T) {
	s, st, email := gptPayFixture(t, func(http.ResponseWriter, *http.Request) { t.Error("unexpected live payment") })
	configureProAutoFixture(t, s)
	s.proAutoCancel()
	nex, f := nexFixtureServer(t)
	s.nexovcc.transport = nex.nexovcc.transport
	cfg, credentials, _ := nex.store.NexovccSettings()
	if _, err := st.SaveNexovccSettings(cfg, credentials, false); err != nil {
		t.Fatal(err)
	}
	if err := st.NexovccCache("products", []nexovcc.Product{{ID: "267", Minimum: "1.00"}}); err != nil {
		t.Fatal(err)
	}
	p, _, _, _ := st.ProSettings()
	p.CardProvider = "ejh"
	p.EJHAutoOpen = false
	p.NexovccAutoOpen = true
	p.NexovccTemplateID = "template"
	p.NexovccDailyOpenLimit = 1
	p.NexovccDailyAmountLimit = "10.00"
	if _, err := st.SaveProSettings(p, "", ""); err != nil {
		t.Fatal(err)
	}
	c := providerWindow(t, s, "nexovcc")
	pay, _, _ := st.GPTPaySettings()
	snap := restock.Snapshot{At: time.Now(), Valid: true, LoadValid: true, GroupID: 1, Binding: restockBinding(p), NewPlan: restockPlan(pay.Provider, pay.PlanCode, pay.Country, pay.CreditQuantity)}
	target, err := st.SaveDownstreamTarget(model.DownstreamTarget{Scope: "pro-restock", Provider: "sub2", Sub2: p.Sub2, Secret: "test-password"})
	if err != nil {
		t.Fatal(err)
	}
	tasks, err := st.ReserveProRestock(restock.Decision{ID: "dispatch-nex", At: time.Now(), Version: c.Version, Suggested: 1, Snapshot: snap}, target, 0)
	if err != nil || len(tasks) != 1 {
		t.Fatal(tasks, err)
	}
	// Mimic an older queued intent; actual dispatch must use the current window.
	tasks[0].CardProvider = "ejh"
	tasks[0].StrictCardProvider = false
	if err = st.SaveProRestockTask(tasks[0]); err != nil {
		t.Fatal(err)
	}
	s.proAutoHooks = &proAutomationHooks{Session: func(context.Context, string, func(string, map[string]any) (any, error)) error {
		return errors.New("fixture ends before payment")
	}}
	s.dispatchProRestock(context.Background(), c, p, snap)
	profile := waitProAutoFinished(t, s, email)
	card, _, err := st.GPTPayCard(profile.ProAuto.CardID)
	if err != nil || card.Source != "nexovcc" || f.opens.Load() != 1 || profile.ProAuto.ID == "" {
		t.Fatal("dispatch did not use allowed issuer", fmt.Sprint(profile.ProAuto), err)
	}
	tasks, err = st.ProRestockTasks()
	if err != nil || tasks[0].CardProvider != "nexovcc" || !tasks[0].StrictCardProvider {
		t.Fatal(tasks, err)
	}
	actual, _, _, _ := st.ProSettings()
	if actual.CardProvider != "ejh" {
		t.Fatal("dispatch changed global default")
	}
}
