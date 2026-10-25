package httpapi

import (
	"chatgpt-space-merge/internal/ejh"
	"chatgpt-space-merge/internal/gptpay"
	"chatgpt-space-merge/internal/model"
	"chatgpt-space-merge/internal/refundmerchant"
	"chatgpt-space-merge/internal/restock"
	"chatgpt-space-merge/internal/store"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func merchantFixture(t *testing.T) *proExecutionFixture {
	t.Helper()
	f := newProExecutionFixture(t)
	f.s.stopProAutomations()
	v, _, _, err := f.s.store.ProSettings()
	if err != nil {
		t.Fatal(err)
	}
	v.AutoMerchantEnabled, v.AutoDeleteEnabled, v.AutoMergeEnabled = true, false, false
	v.TargetAdminID = ""
	if _, _, err = f.s.store.SaveProAutomationSettings(v, gptpay.Settings{PlanCode: "pro5"}, "", "fixture-merchant-key"); err != nil {
		t.Fatal(err)
	}
	p, c, err := f.s.store.MailAccountCredential(f.email)
	if err != nil {
		t.Fatal(err)
	}
	password, totp := "fixture-password", "JBSWY3DPEHPK3PXP"
	if _, err = f.s.store.PatchMailCredentials(f.email, store.MailCredentialsPatch{Revision: f.s.store.MailCredentialsRevision(p, c), GPTPassword: &password, TOTPSecret: &totp}); err != nil {
		t.Fatal(err)
	}
	if _, err = f.s.store.UpdateProAccount(f.email, func(p *model.MailAccountProfile) {
		p.ProAuto = model.ProAutoState{ID: "merchant-auto", Status: "waiting_quota", Stage: "quota", Steps: map[string]string{"push": "completed", "quota": "completed"}, StartedAt: time.Now().Add(-time.Hour)}
	}); err != nil {
		t.Fatal(err)
	}
	return f
}

func TestProMerchantAutoTailAndTerminalStatuses(t *testing.T) {
	f := merchantFixture(t)
	status := "queued"
	posts, gets := 0, 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer fixture-merchant-key" {
			t.Error("incorrect API key")
		}
		if r.Method == "POST" {
			posts++
			var in refundmerchant.Submission
			if json.NewDecoder(r.Body).Decode(&in) != nil || !in.Authorized || len(in.Accounts) != 1 || in.Accounts[0].Email != f.email || in.Accounts[0].Password != "fixture-password" || in.Accounts[0].TOTPSecret != "JBSWY3DPEHPK3PXP" {
				t.Error("incorrect credentials")
			}
			w.WriteHeader(202)
			fmt.Fprintf(w, `{"items":[{"id":"job","email":%q,"status":"succeeded"}]}`, f.email)
			return
		}
		gets++
		fmt.Fprintf(w, `{"id":"job","email":%q,"status":%q}`, f.email, status)
	}))
	defer srv.Close()
	f.s.proMerchantClient = &refundmerchant.Client{BaseURL: srv.URL}
	if err := f.s.runProAutomationTail(t.Context(), f.email); err != nil {
		t.Fatal(err)
	}
	p, _, _ := f.s.store.MailAccountCredential(f.email)
	if !p.MerchantAccepted() || p.MerchantCompleted() || p.ProAuto.Status != "awaiting_merchant" || p.ProStages().Steps["merchant"] != "waiting" || gets != 0 {
		t.Fatal("202 was treated as final success", p.ProMerchant)
	}
	if p.ProMerchant.NextCheckAt == nil || time.Until(*p.ProMerchant.NextCheckAt) < 14*time.Second {
		t.Fatal("missing 15 second interval")
	}
	// A mode change cannot send this account through local OpenAI deletion.
	v, _, _, _ := f.s.store.ProSettings()
	v.AutoMerchantEnabled = false
	v.AutoDeleteEnabled = true
	if _, err := f.s.store.SaveProSettings(v, "", ""); err != nil {
		t.Fatal(err)
	}
	for _, next := range []string{"queued", "processing", "verifying", "attention", "failed", "succeeded"} {
		status = next
		p, err := f.s.performProDisposition(t.Context(), f.email)
		if err != nil {
			t.Fatal(err)
		}
		if p.MerchantCompleted() != (next == "succeeded") || p.ProDeactivation != nil || p.SpaceMergedOnce {
			t.Fatalf("wrong completion at %s", next)
		}
		if next == "failed" && p.ProMerchant.NextCheckAt != nil {
			t.Fatal("terminal failure was scheduled again")
		}
	}
	p, _, _ = f.s.store.MailAccountCredential(f.email)
	if p.ProAuto.Status != "completed" || p.ProMerchant.NextCheckAt != nil || p.ProStages().Steps["merchant"] != "completed" {
		t.Fatal("did not complete merchant tail")
	}
	if _, err := f.s.performProMerchant(t.Context(), f.email); err != nil {
		t.Fatal(err)
	}
	if posts != 1 || gets != 6 || len(f.calls) != 0 {
		t.Fatal("replayed request or called OpenAI", posts, gets, f.calls)
	}
	_, total, counts, err := f.s.store.ProAccountsPage("", "merchant", 100, 0)
	if err != nil || total != 1 || counts.Merchant != 1 || counts.Deleted != 0 {
		t.Fatal(total, counts, err)
	}
}

func TestProMerchantLostResponseUsesOriginalRequest(t *testing.T) {
	f := merchantFixture(t)
	var originalBody, originalKey string
	posts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		posts++
		if posts == 1 {
			originalBody, originalKey = string(body), r.Header.Get("Idempotency-Key")
			w.WriteHeader(502)
			return
		}
		if originalBody != string(body) || originalKey != r.Header.Get("Idempotency-Key") {
			t.Error("retry changed request")
		}
		w.WriteHeader(202)
		fmt.Fprintf(w, `{"jobs":[{"id":"job","email":%q}]}`, f.email)
	}))
	defer srv.Close()
	f.s.proMerchantClient = &refundmerchant.Client{BaseURL: srv.URL}
	if _, err := f.s.performProMerchant(t.Context(), f.email); err == nil {
		t.Fatal("ignored response loss")
	}
	p, c, _ := f.s.store.MailAccountCredential(f.email)
	password := "new-password"
	if _, err := f.s.store.PatchMailCredentials(f.email, store.MailCredentialsPatch{Revision: f.s.store.MailCredentialsRevision(p, c), GPTPassword: &password}); err != nil {
		t.Fatal(err)
	}
	if err := f.s.store.RecoverProWorkflows(); err != nil {
		t.Fatal(err)
	}
	p, err := f.s.performProMerchant(t.Context(), f.email)
	if err != nil || !p.MerchantAccepted() || p.MerchantCompleted() || posts != 2 {
		t.Fatal(p.ProMerchant, err)
	}
	encoded, _ := json.Marshal(p)
	if strings.Contains(string(encoded), "fixture-password") || strings.Contains(string(encoded), "fixture-merchant-key") {
		t.Fatal("credentials leaked")
	}
}

func TestProMerchantStopCancelsRequestAndTimers(t *testing.T) {
	f := merchantFixture(t)
	entered, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		close(entered)
		select {
		case <-r.Context().Done():
		case <-release:
		}
		w.WriteHeader(202)
		fmt.Fprintf(w, `{"items":[{"id":"job","email":%q}]}`, f.email)
	}))
	defer srv.Close()
	defer close(release)
	f.s.proMerchantClient = &refundmerchant.Client{BaseURL: srv.URL}
	if response := f.request(f.s.merchantProAccount, "POST", "/merchant", ""); response.Code != 202 {
		t.Fatal(response.Code, response.Body.String())
	}
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("request not started")
	}
	if r := f.request(f.s.stopProAutomation, "POST", "/stop", ""); r.Code != 202 {
		t.Fatal(r.Code)
	}
	f.s.proMergeWG.Wait()
	p, _, _ := f.s.store.MailAccountCredential(f.email)
	if !p.ProAuto.StopRequested || p.ProAuto.Status != "stopped" || p.ProMerchant.NextCheckAt != nil {
		t.Fatal("stop did not persist")
	}
	f.s.tickProMerchants(context.Background())
	if calls.Load() != 1 {
		t.Fatal("stopped request restarted")
	}
	if _, err := f.s.performProMerchant(t.Context(), f.email); err != context.Canceled {
		t.Fatal(err)
	}
}

func TestProMerchantQuotaTimeoutAndDailyTriggers(t *testing.T) {
	for _, trigger := range []string{"quota", "timeout", "daily"} {
		t.Run(trigger, func(t *testing.T) {
			f := merchantFixture(t)
			enableEJHPaymentFixture(t, f.s.store)
			card, err := f.s.store.ImportEJHCard(ejh.Card{OrderNo: "merchant-card", Number: "4242424242424242", Validity: "1299", CVV: "012", Status: "1", CardCurrency: "USD"}, true)
			if err != nil {
				t.Fatal(err)
			}
			at := time.Now().Add(-2 * time.Hour)
			order := gptpay.Order{ID: "merchant-paid", Email: f.email, CardID: card.ID, Status: "success", PlanCode: "pro20", CreatedAt: at, UpdatedAt: at}
			if err = f.s.store.CreateGPTPayOrder(order, gptpay.Snapshot{Input: gptpay.CreateInput{CardSecret: gptpay.CardSecret{Number: "4242424242424242", Year: 2099, Month: 12, CVV: "012"}}}); err != nil {
				t.Fatal(err)
			}
			cfg := restock.DefaultConfig()
			cfg.DailyEJHMergeEnabled = trigger == "daily"
			cfg.DailyEJHMergeTime = "17:30"
			if _, err = f.s.store.SaveProRestockConfig(cfg); err != nil {
				t.Fatal(err)
			}
			if _, err = f.s.store.UpdateProAccount(f.email, func(p *model.MailAccountProfile) {
				p.ProAuto.OrderID = order.ID
				p.ProAuto.QuotaUsedThreshold = 80
				p.ProAuto.Steps["quota"] = "waiting"
				p.ProAuto.Steps["recharge"] = "completed"
				p.ProAuto.Steps["oauth"] = "completed"
				if trigger == "daily" {
					p.ProDailyMerge = &model.ProDailyMergeRequest{At: at, OrderID: order.ID, AutomationID: p.ProAuto.ID}
				}
				if trigger == "timeout" {
					enabled := true
					p.ProPushTimeoutEnabled = &enabled
					p.ProPushTimeoutMinutes = 60
					p.ProPushStartedAt = &at
				}
			}); err != nil {
				t.Fatal(err)
			}
			quotaCalls, posts := 0, 0
			f.s.proAutoHooks = &proAutomationHooks{Quota: func(context.Context, string, bool) (model.MailAccountProfile, error) {
				quotaCalls++
				return f.s.store.UpdateProAccount(f.email, func(p *model.MailAccountProfile) {
					p.Quota7D = &model.FreeQuotaWindow{UsedPercent: 85, LimitWindowSeconds: 604800}
				})
			}, Merge: func(context.Context, string) (model.MailAccountProfile, error) {
				t.Error("merchant trigger executed space merge")
				return model.MailAccountProfile{}, nil
			}}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == "POST" {
					posts++
					w.WriteHeader(202)
					fmt.Fprintf(w, `{"id":"job","email":%q}`, f.email)
				} else {
					fmt.Fprintf(w, `{"id":"job","email":%q,"status":"succeeded"}`, f.email)
				}
			}))
			defer srv.Close()
			f.s.proMerchantClient = &refundmerchant.Client{BaseURL: srv.URL}
			if err = f.s.runProAutomationTail(t.Context(), f.email); err != nil {
				t.Fatal(err)
			}
			p, _, _ := f.s.store.MailAccountCredential(f.email)
			if posts != 1 || !p.MerchantAccepted() || p.MerchantCompleted() || p.ProAuto.Status != "awaiting_merchant" {
				t.Fatal("wrong pending tail", p.ProAuto)
			}
			if trigger == "quota" && quotaCalls != 1 || trigger != "quota" && quotaCalls != 0 {
				t.Fatal("wrong quota calls", quotaCalls)
			}
			cases, err := f.s.store.EJHCasesForEmails([]string{f.email})
			if err != nil || len(cases) != 1 || cases[0].RequestedAt == nil || cases[0].RequestSource != "api_merchant" || cases[0].Refunds != 0 || len(cases[0].Returns) != 0 {
				t.Fatal("merchant acceptance must record request but never invent refund", err)
			}
			if trigger == "daily" && p.ProDailyMerge.Completed {
				t.Fatal("daily task completed before merchant final success")
			}
			p, err = f.s.performProMerchant(t.Context(), f.email)
			if err != nil || !p.MerchantCompleted() || trigger == "daily" && !p.ProDailyMerge.Completed {
				t.Fatal("final merchant state not propagated", err)
			}
		})
	}
}
