package httpapi

import (
	"chatgpt-space-merge/internal/ejh"
	"chatgpt-space-merge/internal/gptpay"
	"chatgpt-space-merge/internal/model"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestEJHOccupancyManualAndAutomaticEntrypoints(t *testing.T) {
	for _, status := range []string{"success", "unknown"} {
		t.Run(status, func(t *testing.T) {
			var calls atomic.Int32
			s, st, email := gptPayFixture(t, func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if status == "unknown" {
					w.WriteHeader(503)
					return
				}
				fmt.Fprint(w, `{"code":0,"data":{"id":"local-payment","status":"success"}}`)
			})
			s.proAutoCancel()
			s.proAutoWG.Wait()
			configureProAutoFixture(t, s)
			if w := payCall(t, s.saveGPTPayCard, "id", "test-card", map[string]any{"card_type": "ejh"}); w.Code != 200 {
				t.Fatal(w.Body.String())
			}
			other := "other@example.com"
			if _, err := st.SaveMailAccount(model.MailAccountProfile{Email: other, ManagementScope: "pro"}, model.MailAccountCredentials{Email: other, AccessToken: proAutoToken(other, "initial")}); err != nil {
				t.Fatal(err)
			}
			if err := st.ReserveProCard(other, "test-card", false); err != nil {
				t.Fatal(err)
			}
			w := payCall(t, s.createGPTPayOrder, "email", email, payInput("reserved-manual-01"))
			if w.Code != 409 || calls.Load() != 0 || !strings.Contains(w.Body.String(), "占用") {
				t.Fatal("manual bypassed auto reservation", w.Code, w.Body.String(), calls.Load())
			}
			if err := st.ReleaseProCard(other); err != nil {
				t.Fatal(err)
			}
			order := payOrder(t, payCall(t, s.createGPTPayOrder, "email", email, payInput("actual-manual-01")))
			if calls.Load() != 1 || (status == "success" && order.Status != "success") || (status == "unknown" && order.Status != "submission_unknown") {
				t.Fatal(order, calls.Load())
			}
			w = payCall(t, s.startProAutomation, "email", other, map[string]any{"card_id": "test-card", "plan_code": "pro20"})
			if w.Code != 409 || !strings.Contains(w.Body.String(), "占用") || calls.Load() != 1 {
				t.Fatal("auto bypassed manual order", w.Code, w.Body.String())
			}
			if _, err := st.UpdateProAccount(email, func(p *model.MailAccountProfile) { p.RemoveStatus = "completed" }); err != nil {
				t.Fatal(err)
			}
			c, _, err := st.GPTPayCard("test-card")
			if err != nil || c.Available {
				t.Fatal("removal released occupancy", c, err)
			}
			if status == "success" {
				ejhReceiveOrderAPI(t, s, "test-card", order.ID)
				c, _, err = st.GPTPayCard("test-card")
				if err != nil || !c.Available || c.ActiveAccounts != 0 {
					t.Fatal("return did not release card", c, err)
				}
			}
		})
	}
}

func ejhReceiveOrderAPI(t *testing.T, s *Server, cardID, orderID string) {
	t.Helper()
	o, _, err := s.store.GPTPayOrder(orderID)
	if err != nil {
		t.Fatal(err)
	}
	pay := ejh.Trade{OrderNo: "fixture-" + cardID, TradeNo: orderID + "-pay", Type: "1", Status: "1", AuthCode: orderID, CardAmount: "5794.64", CardCurrency: "PHP", TradeTime: o.CreatedAt.Format(time.RFC3339Nano)}
	ret := pay
	ret.Type, ret.TradeNo, ret.CardAmount = "2", orderID+"-return", "5780.87"
	ret.TradeTime = time.Now().Add(time.Hour).Format(time.RFC3339Nano)
	if err = s.store.EJHSaveTrades(cardID, []ejh.Trade{pay, ret}); err != nil {
		t.Fatal(err)
	}
	if err = s.store.EJHReconcile(); err != nil {
		t.Fatal(err)
	}
	cases, err := s.store.EJHCases()
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		if c.ID == orderID && c.State == "returned" {
			return
		}
	}
	t.Fatal("return was not automatically matched", orderID)
}

func TestEJHOccupancySchedulerPolicy(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		t.Run(fmt.Sprint(enabled), func(t *testing.T) {
			var calls atomic.Int32
			s, st, _ := gptPayFixture(t, func(w http.ResponseWriter, r *http.Request) {
				var in gptpay.CreateInput
				if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
					t.Error(err)
				}
				calls.Add(1)
				fmt.Fprintf(w, `{"code":0,"data":{"id":%q,"status":"success"}}`, in.Session.User.Email)
			})
			s.proAutoCancel()
			s.proAutoWG.Wait()
			configureProAutoFixture(t, s)
			cfg := ejh.DefaultSettings()
			cfg.SingleActiveAccount = enabled
			if _, err := st.SaveEJHSettings(cfg, ""); err != nil {
				t.Fatal(err)
			}
			if w := payCall(t, s.saveGPTPayCard, "id", "test-card", map[string]any{"card_type": "ejh"}); w.Code != 200 {
				t.Fatal(w.Body.String())
			}
			pro, _, _, err := st.ProSettings()
			if err != nil {
				t.Fatal(err)
			}
			pro.ScheduledEnabled = true
			pro.MaxUnmerged = 10
			pro.Concurrency = 5
			if _, err = st.SaveProSettings(pro, "", ""); err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 4; i++ {
				email := fmt.Sprintf("scheduled-%d@example.com", i)
				if _, err = st.SaveMailAccount(model.MailAccountProfile{Email: email, ManagementScope: "pro"}, model.MailAccountCredentials{Email: email}); err != nil {
					t.Fatal(err)
				}
			}
			s.proAutoHooks = &proAutomationHooks{Session: func(ctx context.Context, email string, rpc func(string, map[string]any) (any, error)) error {
				if _, e := rpc("pro_recharge", map[string]any{"session": map[string]any{"access_token": proAutoToken(email, "before"), "user": map[string]any{"email": email}, "account": map[string]any{"id": "personal-id"}}}); e != nil {
					return e
				}
				_, e := rpc("pro_tokens", map[string]any{"tokens": map[string]any{"access_token": proAutoToken(email, "after"), "refresh_token": "fixture"}})
				return e
			}, Push: func(ctx context.Context, email string) (model.MailAccountProfile, error) {
				return st.UpdateProAccount(email, func(p *model.MailAccountProfile) {
					p.PushStatus = "completed"
					p.PushProvider = "sub2"
					p.Sub2AccountID = 1
				})
			}, Quota: func(ctx context.Context, email string, _ bool) (model.MailAccountProfile, error) {
				return st.UpdateProAccount(email, func(p *model.MailAccountProfile) {
					p.QuotaStatus = "completed"
					p.Quota7D = &model.FreeQuotaWindow{UsedPercent: 1}
				})
			}, Merge: func(context.Context, string) (model.MailAccountProfile, error) {
				t.Error("unexpected merge")
				return model.MailAccountProfile{}, nil
			}}
			want := 3
			if enabled {
				want = 1
			}
			before, err := s.proScheduleStatus()
			if err != nil || before.CardCapacity != want {
				t.Fatal(before, err)
			}
			s.tickProSchedule(false)
			runs, _, err := st.ProScheduleRuns(10, 0)
			if err != nil || len(runs) != 1 || len(runs[0].Tasks) != want {
				t.Fatal(runs, err)
			}
			for _, task := range runs[0].Tasks {
				p := waitProAutoFinished(t, s, task.Email)
				if p.ProAuto.Status != "waiting_quota" {
					t.Fatal(p.ProAuto)
				}
			}
			c, _, err := st.GPTPayCard("test-card")
			if err != nil || c.Available || c.ActiveAccounts != want || int(calls.Load()) != want {
				t.Fatal(c, err, calls.Load())
			}
			if err = st.SetProScheduleNext(time.Now().Add(-time.Second)); err != nil {
				t.Fatal(err)
			}
			s.tickProSchedule(false)
			if int(calls.Load()) != want {
				t.Fatal("worker cleanup freed occupancy")
			}
		})
	}
}

func TestEJHSettingsSwitchCompatibilityAndCardTypeDashboard(t *testing.T) {
	s, st, email := gptPayFixture(t, func(http.ResponseWriter, *http.Request) { t.Error("no external calls") })
	s.proAutoCancel()
	s.proAutoWG.Wait()
	cfg := ejh.DefaultSettings()
	cfg.SingleActiveAccount = false
	legacyRaw, _ := json.Marshal(cfg)
	var legacy map[string]any
	json.Unmarshal(legacyRaw, &legacy)
	delete(legacy, "single_active_account")
	for _, body := range []any{cfg, legacy} {
		w := payCall(t, s.saveEJHSettings, "", "", body)
		if w.Code != 200 {
			t.Fatal(w.Body.String())
		}
		got, _, err := st.EJHSettings()
		if err != nil || got.SingleActiveAccount {
			t.Fatal("explicit false or omitted field lost", got, err)
		}
	}
	_, secret, err := st.GPTPayCard("test-card")
	if err != nil {
		t.Fatal(err)
	}
	o := gptpay.Order{ID: "old-card-order", Email: email, CardID: "test-card", Status: "success", CreatedAt: time.Now()}
	if err = st.CreateGPTPayOrder(o, gptpay.Snapshot{Input: gptpay.CreateInput{CardSecret: secret}}); err != nil {
		t.Fatal(err)
	}
	read := func() struct {
		Cards []struct {
			Local  gptpay.Card `json:"local"`
			Linked bool        `json:"issuer_linked"`
		} `json:"cards"`
		Cases []ejh.Case `json:"cases"`
	} {
		w := payCall(t, s.ejhDashboard, "", "", nil)
		if w.Code != 200 {
			t.Fatal(w.Body.String())
		}
		var v struct {
			Data struct {
				Cards []struct {
					Local  gptpay.Card `json:"local"`
					Linked bool        `json:"issuer_linked"`
				} `json:"cards"`
				Cases []ejh.Case `json:"cases"`
			} `json:"data"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
			t.Fatal(err)
		}
		return v.Data
	}
	if d := read(); len(d.Cards) != 0 || len(d.Cases) != 0 {
		t.Fatal("other data leaked into EJH", d)
	}
	if w := payCall(t, s.saveGPTPayCard, "id", "test-card", map[string]any{"card_type": "ejh"}); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if d := read(); len(d.Cards) != 1 || len(d.Cases) != 1 || d.Cards[0].Linked || d.Cards[0].Local.CardType != "ejh" {
		t.Fatal("manual EJH missing", d)
	}
	if w := payCall(t, s.saveGPTPayCard, "id", "test-card", map[string]any{"enabled": false}); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if d := read(); len(d.Cards) != 1 {
		t.Fatal("partial update reset type")
	}
	if w := payCall(t, s.saveGPTPayCard, "id", "test-card", map[string]any{"card_type": "invalid"}); w.Code != 400 {
		t.Fatal(w.Code)
	}
	if w := payCall(t, s.listGPTPayCardCases, "", "", nil); w.Code != 200 || !strings.Contains(w.Body.String(), o.ID) {
		t.Fatal("general association lost", w.Body.String())
	}
}
