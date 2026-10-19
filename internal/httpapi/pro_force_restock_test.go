package httpapi

import (
	"chatgpt-space-merge/internal/ejh"
	"chatgpt-space-merge/internal/gptpay"
	"chatgpt-space-merge/internal/model"
	"chatgpt-space-merge/internal/restock"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestForceRestockCollectorUsesSavedPolicyAndUpstreamExpiry(t *testing.T) {
	s, st, email := gptPayFixture(t, func(http.ResponseWriter, *http.Request) { t.Error("unexpected supplier request") })
	s.proAutoCancel()
	expiry := time.Now().Add(24 * time.Hour).Unix()
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var data any
		switch r.URL.Path {
		case "/api/v1/auth/login":
			data = map[string]any{"access_token": "fixture-token"}
		case "/api/v1/admin/groups/all":
			data = []any{map[string]any{"id": 1, "name": "Pro", "platform": "openai"}}
		case "/api/v1/admin/accounts":
			data = map[string]any{"items": []any{map[string]any{"id": 1, "platform": "openai", "status": "active", "schedulable": true, "concurrency": 10, "group_ids": []int{1}, "expires_at": expiry, "credentials": map[string]any{"plan_type": "upstream-pro"}}}, "total": 1}
		case "/api/v1/admin/ops/concurrency":
			data = map[string]any{"enabled": true, "timestamp": time.Now(), "account": map[string]any{"1": map[string]any{"account_id": 1, "current_in_use": 0, "waiting_in_queue": 0, "max_capacity": 10}}}
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": data})
	}))
	defer up.Close()
	p := model.DefaultProSettings()
	p.Sub2.URL = up.URL
	p.Sub2.Email = "admin@test.com"
	p.Sub2.GroupIDs = []int64{1}
	p.Sub2.AccountConcurrency = 10
	p.ForceMergeEnabled = true
	p.ForceMergeMinutes = 1
	if _, err := st.SaveProSettings(p, "fixture", ""); err != nil {
		t.Fatal(err)
	}
	enableEJHPaymentFixture(t, st)
	card, err := st.ImportEJHCard(ejh.Card{OrderNo: "force-fixture", Number: "5555555555554444", Validity: "1299", CVV: "123", Status: "1", CardCurrency: "USD"}, true)
	if err != nil {
		t.Fatal(err)
	}
	if err = st.CreateGPTPayOrder(gptpay.Order{ID: "force-paid", Email: email, CardID: card.ID, Status: "success", CreatedAt: time.Now()}, gptpay.Snapshot{Input: gptpay.CreateInput{CardSecret: gptpay.CardSecret{Number: "5555555555554444"}}}); err != nil {
		t.Fatal(err)
	}
	first := time.Now().Add(-50 * time.Minute)
	enabled := true
	ref := model.ProDownstream(p)
	if _, err = st.UpdateProAccount(email, func(v *model.MailAccountProfile) {
		v.PushStatus = "completed"
		v.PushProvider = "sub2"
		v.Sub2AccountID = 1
		v.ProPushStartedAt = &first
		v.ProPushTimeoutEnabled = &enabled
		v.ProPushTimeoutMinutes = 60
		v.ProPushDownstream = &ref
		v.ProAuto.QuotaPlan = "purchase-plan"
	}); err != nil {
		t.Fatal(err)
	}
	c := restock.DefaultConfig()
	c.GroupID = 1
	c.ForceMergeEnabled = true
	s.proRestock.quotaBinding = restockBinding(p)
	s.proRestock.quotaNext = time.Now().Add(time.Hour)
	s.proRestock.moneyBusy = true // Money collection has separate integration coverage.
	collect := func() restock.Account {
		t.Helper()
		s.proRestock.quotaBinding = restockBinding(p)
		s.proRestock.quotaNext = time.Now().Add(time.Hour)
		snap, e := s.collectProRestock(context.Background(), c, p, "fixture")
		if e != nil || !snap.Valid || !snap.LoadValid || len(snap.Accounts) != 1 {
			t.Fatalf("collect: %+v %v", snap, e)
		}
		if snap.LifetimeMinutes != 0 {
			t.Fatal("global policy leaked into forecast")
		}
		return snap.Accounts[0]
	}
	a := collect()
	if !a.EJH || a.ForceMergeDeadline == nil || !a.ForceMergeDeadline.Equal(first.Add(time.Hour)) || a.ExpiresAt.Unix() != expiry || a.Plan != "upstream-pro" || a.PurchasePlan != "purchase-plan" {
		t.Fatalf("local timeout changed upstream fields: %+v", a)
	}
	key := a.ForceMergeKey
	p.Sub2.AccountConcurrency = 20
	if collect().ForceMergeKey != key {
		t.Fatal("changing concurrency reset source lifecycle")
	}
	off := false
	st.UpdateProAccount(email, func(v *model.MailAccountProfile) { v.ProPushTimeoutEnabled = &off })
	a = collect()
	if a.ForceMergeDeadline != nil || a.PushTimeoutEnabled == nil || *a.PushTimeoutEnabled {
		t.Fatal("disabled account acquired deadline")
	}
	st.UpdateProAccount(email, func(v *model.MailAccountProfile) { v.ProPushTimeoutEnabled = nil })
	if collect().ForceMergeDeadline != nil {
		t.Fatal("unknown historical account acquired deadline")
	}
	st.UpdateProAccount(email, func(v *model.MailAccountProfile) {
		v.ProPushDownstream = &model.ProDownstreamRef{Provider: "sub2", URL: "https://different.invalid"}
	})
	if collect().EJH {
		t.Fatal("same numeric ID in another downstream matched")
	}
	st.UpdateProAccount(email, func(v *model.MailAccountProfile) { v.ProPushDownstream = &ref; v.SpaceMergedOnce = true })
	if collect().EJH {
		t.Fatal("merged account participated")
	}
}

func TestForceRestockCommittedReplacementContinues(t *testing.T) {
	for _, departed := range []bool{false, true} {
		t.Run(map[bool]string{false: "average recovered", true: "source departed"}[departed], func(t *testing.T) {
			s, st, _ := gptPayFixture(t, func(http.ResponseWriter, *http.Request) { t.Error("unexpected supplier request") })
			configureProAutoFixture(t, s)
			s.proAutoCancel()
			p, password, _, _ := st.ProSettings()
			p.Concurrency = 1
			if _, err := st.SaveProSettings(p, password, ""); err != nil {
				t.Fatal(err)
			}
			c := restock.DefaultConfig()
			c.Enabled = true
			c.Mode = "auto"
			c.GroupID = 1
			c.ForceMergeEnabled = true
			c.TimeEnabled = false
			c.ConcurrencyEnabled = false
			if _, err := st.SaveProRestockConfig(c); err != nil {
				t.Fatal(err)
			}
			enabled := true
			deadline := time.Now().Add(5 * time.Minute)
			accounts := []restock.Account{{ID: 99, Ready: true, Capacity: 20, EJH: true, PushTimeoutEnabled: &enabled, ForceMergeDeadline: &deadline, ForceMergeKey: "old-cycle"}}
			pay, _, _ := st.GPTPaySettings()
			s.proRestock.monitor = func(context.Context, restock.Config, model.ProSettings, string) (restock.Snapshot, error) {
				return restock.Snapshot{At: time.Now(), Valid: true, LoadValid: true, GroupID: 1, Binding: restockBinding(p), NewPlan: restockPlan(pay.Provider, pay.PlanCode, pay.Country, pay.CreditQuantity), Accounts: accounts}, nil
			}
			s.proMergeMu.Lock()
			s.proAutoRunning = 1
			s.proMergeMu.Unlock()
			if err := s.tickProRestock(context.Background(), true); err != nil {
				t.Fatal(err)
			}
			tasks, err := st.ProRestockTasks()
			if err != nil || len(tasks) != 1 || tasks[0].ForceMergeSource == "" || tasks[0].Status != "reserved" {
				t.Fatal(tasks, err)
			}
			deadline = time.Now().Add(time.Hour)
			if departed {
				accounts = nil
			}
			launches := make(chan string, 1)
			s.proAutoHooks = &proAutomationHooks{Session: func(_ context.Context, email string, _ func(string, map[string]any) (any, error)) error {
				launches <- email
				return errors.New("stop fixture before payment")
			}}
			s.proMergeMu.Lock()
			s.proAutoRunning = 0
			s.proMergeMu.Unlock()
			if err = s.tickProRestock(context.Background(), true); err != nil {
				t.Fatal(err)
			}
			select {
			case email := <-launches:
				waitProAutoFinished(t, s, email)
			case <-time.After(5 * time.Second):
				t.Fatal("committed replacement was cancelled or stalled")
			}
			tasks, err = st.ProRestockTasks()
			if err != nil || len(tasks) != 1 {
				t.Fatal("duplicate purchase", tasks, err)
			}
		})
	}
}
