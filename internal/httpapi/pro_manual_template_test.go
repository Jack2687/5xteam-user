package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"chatgpt-space-merge/internal/gptpay"
	"chatgpt-space-merge/internal/model"
	"chatgpt-space-merge/internal/nexovcc"
)

// Real handlers and local issuer/payment servers reproduce a usable older card
// of the opposite plan, with the legacy default still pointing at that card.
func manualTemplateFixture(t *testing.T, provider, plan string) (*Server, string, gptpay.Card, string) {
	t.Helper()
	var s *Server
	var email string
	if provider == "ejh" {
		s, _, _, email = allocationFixture(t)
	} else {
		s, _, email, _ = restockWindowIntegration(t, func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprint(w, `{"code":0,"data":{"id":"manual-paid","status":"success"}}`)
		})
	}
	other := "pro20"
	if plan == other {
		other = "pro5"
	}
	p, _, _, _ := s.store.ProSettings()
	p.CardProvider = provider
	p.PurchaseScheduleEnabled = true
	p.PurchaseWindows = []model.ProPurchaseWindow{activePurchaseWindow(provider, other)}
	var oldID, newID string
	if provider == "ejh" {
		cfg, _, _ := s.store.EJHSettings()
		old := cfg.Templates[0]
		old.PlanCode = other
		newTemplate := old
		newTemplate.ID, newTemplate.Name, newTemplate.PlanCode = "manual-target", "target", plan
		cfg.Templates = append(cfg.Templates[:0], old, newTemplate)
		if _, err := s.store.SaveEJHSettings(cfg, ""); err != nil {
			t.Fatal(err)
		}
		oldID, newID = old.ID, newTemplate.ID
		p.EJHPlanTemplates = map[string]string{plan: newID, other: oldID}
	} else {
		cfg, credentials, _ := s.store.NexovccSettings()
		old := cfg.Templates[0]
		old.PlanCode = other
		newTemplate := old
		newTemplate.ID, newTemplate.Name, newTemplate.PlanCode = "manual-target", "target", plan
		cfg.Templates = append(cfg.Templates[:0], old, newTemplate)
		if _, err := s.store.SaveNexovccSettings(cfg, credentials, false); err != nil {
			t.Fatal(err)
		}
		oldID, newID = old.ID, newTemplate.ID
		p.NexovccPlanTemplates = map[string]string{plan: newID, other: oldID}
		p.NexovccDailyOpenLimit, p.NexovccDailyAmountLimit = 5, "100"
	}
	if _, err := s.store.SaveProSettings(p, "", ""); err != nil {
		t.Fatal(err)
	}
	pay, _, _ := s.store.GPTPaySettings()
	pay.PlanCode = plan
	if _, err := s.store.SaveGPTPaySettings(pay, ""); err != nil {
		t.Fatal(err)
	}
	if provider == "nexovcc" {
		c, err := s.store.SaveNexovccCard(nexovcc.Card{Serial: "old-manual-template", Status: "1", Balance: "10.00", CreatedAt: time.Now().Add(-time.Hour)}, &nexovcc.PrivateCard{Number: "5555555555554444", CVV: "123", Month: 12, Year: 2099})
		if err != nil {
			t.Fatal(err)
		}
		op := nexovcc.Operation{ID: "old-manual-template", Action: "open", TemplateID: oldID, PlanCode: other, CardID: c.ID, Status: "submitted", CreatedAt: time.Now(), UpdatedAt: time.Now()}
		if err = s.store.BeginNexovccOperation(op); err != nil {
			t.Fatal(err)
		}
		op.Status = "succeeded"
		if err = s.store.SaveNexovccOperation(op); err != nil {
			t.Fatal(err)
		}
		old, _, err := s.store.GPTPayCard(c.ID)
		if err != nil {
			t.Fatal(err)
		}
		return s, email, old, newID
	}
	ctx := context.WithValue(context.Background(), proPurchaseContextKey{}, &model.ProPurchaseRoute{CardProvider: provider, TemplateID: oldID, PlanCode: other})
	old, err := s.ensureProviderCard(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return s, email, old, newID
}

func TestProManualTemplateEntrypoints(t *testing.T) {
	for _, provider := range []string{"ejh", "nexovcc"} {
		for _, plan := range []string{"pro5", "pro20"} {
			for _, mode := range []string{"manual", "single", "replacement"} {
				t.Run(provider+"/"+plan+"/"+mode, func(t *testing.T) {
					s, email, old, target := manualTemplateFixture(t, provider, plan)
					st := s.store
					list := httptest.NewRecorder()
					s.listGPTPayCards(list, httptest.NewRequest("GET", "/api/gptpay/cards?enabled=true&card_provider="+provider+"&page_size=10", nil))
					var result struct {
						Data struct {
							Items []gptpay.Card `json:"items"`
							Total int           `json:"total"`
						} `json:"data"`
					}
					if err := json.Unmarshal(list.Body.Bytes(), &result); err != nil || list.Code != 200 || result.Data.Total != 0 {
						t.Fatal("wrong-plan candidate offered", list.Body.String(), err)
					}
					body := map[string]any{"provider": "tokenseek", "plan_code": plan, "card_provider": provider, "card_id": old.ID, "request_id": "manual-template-request"}
					handler := s.startProAutomation
					if mode == "single" {
						handler = s.createGPTPayOrder
						delete(body, "card_provider")
					} else {
						delete(body, "request_id")
					}
					blocked := payCall(t, handler, "email", email, body)
					if blocked.Code != 409 || !strings.Contains(blocked.Body.String(), "模板") {
						t.Fatal("explicit old card accepted", blocked.Code, blocked.Body.String())
					}
					orders, _, _ := st.GPTPayOrders(20, 0, email)
					if len(orders) != 0 {
						t.Fatal("rejection submitted payment")
					}
					if mode == "replacement" {
						cfg, _, _, _ := st.ProSettings()
						cfg.RechargeReplacementCount = 1
						if _, err := st.SaveProSettings(cfg, "", ""); err != nil {
							t.Fatal(err)
						}
						allocationAccount(t, s, "manual-successor@example.com")
					}
					rootEmail := email
					s.proAutoHooks = &proAutomationHooks{Session: func(ctx context.Context, current string, rpc func(string, map[string]any) (any, error)) error {
						if mode == "replacement" && current == rootEmail {
							_ = s.autoProUpdate(current, "login", "completed", "")
							_ = s.autoProUpdate(current, "recharge", "running", "")
							return errors.New("local pre-payment failure")
						}
						if err := windowRecharge(s, current, rpc); err != nil {
							return err
						}
						_, err := rpc("pro_tokens", map[string]any{"tokens": map[string]any{"access_token": proAutoToken(current, "paid"), "refresh_token": "fixture-rt"}})
						return err
					}, Push: func(_ context.Context, current string) (model.MailAccountProfile, error) {
						return st.UpdateProAccount(current, func(p *model.MailAccountProfile) { p.PushStatus = "completed" })
					}, Quota: func(context.Context, string, bool) (model.MailAccountProfile, error) {
						return model.MailAccountProfile{}, errors.New("stop at quota")
					}}
					body["card_id"] = ""
					started := payCall(t, handler, "email", email, body)
					if mode == "single" {
						order := payOrder(t, started)
						if order.Status != "success" {
							t.Fatal(order)
						}
					} else {
						if started.Code != 202 {
							t.Fatal(started.Code, started.Body.String())
						}
						p := waitProAutoFinished(t, s, email)
						if p.ProAuto.Replacement.PurchaseRoute == nil || p.ProAuto.Replacement.PurchaseRoute.TemplateID != target {
							t.Fatal("template intent not persisted", p.ProAuto)
						}
						if mode == "replacement" {
							s.dispatchProRechargeReplacements(context.Background())
							p, _, _ = st.MailAccountCredential(email)
							if p.ProAuto.Replacement.NextEmail == "" {
								t.Fatal("successor not claimed", p.ProAuto)
							}
							email = p.ProAuto.Replacement.NextEmail
							p = waitProAutoFinished(t, s, email)
						}
						if p.ProAuto.Status != "waiting_quota" {
							t.Fatal("flow failed", p.ProAuto)
						}
					}
					orders, _, _ = st.GPTPayOrders(20, 0, email)
					if len(orders) != 1 || orders[0].Status != "success" || orders[0].PlanCode != plan || orders[0].CardID == old.ID {
						t.Fatal("wrong payment", orders)
					}
					matches, err := st.ProTemplateCards(provider, target, plan)
					if err != nil || !matches[orders[0].CardID] {
						t.Fatal("payment used wrong template", err, orders)
					}
					before, _, _ := st.GPTPayCard(old.ID)
					if before.ActiveAccounts != 0 || before.PendingAccounts != 0 {
						t.Fatal("old card occupied", before.ID)
					}
				})
			}
		}
	}
}

func TestProManualTemplatePinnedReplacementAndConfigChanges(t *testing.T) {
	s, _, _, target := manualTemplateFixture(t, "ejh", "pro5")
	p, _, _, _ := s.store.ProSettings()
	pay, _, _ := s.store.GPTPaySettings()
	_, selected, err := s.manualProPurchase(context.Background(), p, pay, "ejh", nil)
	if err != nil || selected.PurchaseRoute == nil {
		t.Fatal(err)
	}
	pinned := selected.PurchaseRoute
	// The pending chain must retain its original template even when the map
	// is edited. A different plan or reclassified template stops before I/O.
	p.EJHPlanTemplates["pro5"] = p.EJHPlanTemplates["pro20"]
	_, selected, err = s.manualProPurchase(context.Background(), p, pay, "ejh", pinned)
	if err != nil || selected.PurchaseRoute.TemplateID != target {
		t.Fatal("lost pinned template", err)
	}
	changed := pay
	changed.PlanCode = "pro20"
	if _, _, err = s.manualProPurchase(context.Background(), p, changed, "ejh", pinned); err == nil {
		t.Fatal("queued replacement changed plan")
	}
	cfg, _, _ := s.store.EJHSettings()
	for i := range cfg.Templates {
		if cfg.Templates[i].ID == target {
			cfg.Templates[i].PlanCode = "pro20"
		}
	}
	if _, err = s.store.SaveEJHSettings(cfg, ""); err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.manualProPurchase(context.Background(), p, pay, "ejh", pinned); err == nil {
		t.Fatal("reclassified template accepted")
	}
}
