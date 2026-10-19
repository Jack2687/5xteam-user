package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"chatgpt-space-merge/internal/model"
	"chatgpt-space-merge/internal/sub2"
)

func TestProCostQuotaIndependentResults(t *testing.T) {
	for _, tc := range []struct {
		name, stats                string
		quotaFailure, statsFailure bool
		wantStandard, wantUser     float64
	}{
		{name: "both costs", stats: `{"total_standard_cost":12.3456,"total_user_cost":3.25}`, wantStandard: 12.3456, wantUser: 3.25},
		{name: "legacy fallback", stats: `{"total_cost":8}`, wantStandard: 8, wantUser: 0.4},
		{name: "cost failure preserves snapshot", statsFailure: true, wantStandard: 1.25, wantUser: 0.4},
		{name: "malformed stats preserve snapshot", stats: `{}`, wantStandard: 1.25, wantUser: 0.4},
		{name: "quota failure still saves cost", quotaFailure: true, stats: `{"total_standard_cost":6,"total_user_cost":2}`, wantStandard: 6, wantUser: 2},
		{name: "lower counters remain monotonic like Team", stats: `{"total_standard_cost":0,"total_user_cost":0}`, wantStandard: 1.25, wantUser: 0.4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newProExecutionFixture(t)
			var statCalls atomic.Int32
			downstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/v1/auth/login":
					fmt.Fprint(w, `{"code":0,"data":{"access_token":"fixture"}}`)
				case "/api/v1/admin/accounts/42/stats":
					statCalls.Add(1)
					if r.URL.Query().Get("days") != "90" {
						t.Error("wrong statistics window")
					}
					if tc.statsFailure {
						http.Error(w, "fixture stats failure", 400)
						return
					}
					fmt.Fprintf(w, `{"code":0,"data":{"summary":%s}}`, tc.stats)
				case "/api/v1/admin/openai/accounts/42/quota":
					if tc.quotaFailure {
						http.Error(w, "fixture quota failure", 400)
						return
					}
					fmt.Fprint(w, `{"code":0,"data":{"rate_limit":{"primary_window":{"used_percent":10,"limit_window_seconds":18000},"secondary_window":{"used_percent":20,"limit_window_seconds":604800}}}}`)
				default:
					t.Errorf("unexpected request: %s", r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			defer downstream.Close()
			v, _, _, _ := f.s.store.ProSettings()
			v.Provider, v.Sub2.URL, v.Sub2.Email = "sub2", downstream.URL, "admin@example.com"
			if _, err := f.s.store.SaveProSettings(v, "fixture-password", ""); err != nil {
				t.Fatal(err)
			}
			if _, err := f.s.store.UpdateProAccount(f.email, func(p *model.MailAccountProfile) { p.PushProvider = "sub2" }); err != nil {
				t.Fatal(err)
			}
			old := time.Now().Add(-time.Hour)
			_, err := f.s.store.UpdateProAccount(f.email, func(p *model.MailAccountProfile) {
				p.CostProvider, p.CostConnectionIdentity, p.CostDownstreamIdentity = "sub2", proCostConnectionIdentity(v.Sub2), "42"
				p.CostDownstreamSnapshot, p.UserCostDownstreamSnapshot = 1.25, 0.4
				p.UserCostDownstreamIdentity, p.CostCheckedAt = "42", &old
			})
			if err != nil {
				t.Fatal(err)
			}
			_, err = f.s.performProQuota(t.Context(), f.email, false)
			if (err != nil) != tc.quotaFailure {
				t.Fatalf("quota error=%v", err)
			}
			p, _, err := f.s.store.MailAccountCredential(f.email)
			if err != nil {
				t.Fatal(err)
			}
			if p.CostDownstreamSnapshot != tc.wantStandard || p.UserCostDownstreamSnapshot != tc.wantUser || p.CostCheckedAt == nil {
				t.Fatalf("incorrect costs: %+v", p)
			}
			if (tc.statsFailure || tc.stats == `{}`) && !p.CostCheckedAt.Equal(old) {
				t.Fatal("failed statistics changed freshness")
			}
			if !tc.quotaFailure && (p.QuotaStatus != "completed" || p.Quota7D.UsedPercent != 20) {
				t.Fatal("cost failure changed quota result")
			}
			if statCalls.Load() != 1 {
				t.Fatal("expected one statistics call")
			}
			w := f.request(f.s.listProAccounts, "GET", "/api/pro-accounts?page=1&page_size=10", "")
			var body struct {
				Data struct {
					Items []model.MailAccountProfile `json:"items"`
				} `json:"data"`
			}
			if err = json.Unmarshal(w.Body.Bytes(), &body); err != nil || len(body.Data.Items) != 1 || body.Data.Items[0].CostDownstreamSnapshot != tc.wantStandard {
				t.Fatal("list did not expose cost", err)
			}
			if statCalls.Load() != 1 {
				t.Fatal("list must not poll downstream statistics")
			}
			if tc.name == "both costs" {
				w = f.request(f.s.checkProAccountQuota, "POST", "/quota", "")
				if w.Code != 200 || statCalls.Load() != 2 {
					t.Fatal("single-account query did not refresh consumption", w.Code)
				}
				body, _ := json.Marshal(map[string]any{"emails": []string{f.email}})
				w = f.request(f.s.checkProAccountQuotas, "POST", "/quota-batch", string(body))
				if w.Code != 200 || statCalls.Load() != 3 {
					t.Fatal("batch query did not refresh consumption", w.Code)
				}
				if _, err = f.s.performProQuota(t.Context(), f.email, true); err != nil || statCalls.Load() != 4 {
					t.Fatal("automatic quota query did not refresh consumption", err)
				}
			}
		})
	}
}

func TestProCostBindingAndMissingUserCost(t *testing.T) {
	f := newProExecutionFixture(t)
	v, _, _, _ := f.s.store.ProSettings()
	v.Provider, v.Sub2.URL, v.Sub2.Email = "sub2", "https://sub2.example.test", "admin@example.test"
	if _, err := f.s.store.SaveProSettings(v, "fixture", ""); err != nil {
		t.Fatal(err)
	}
	p, err := f.s.store.UpdateProAccount(f.email, func(p *model.MailAccountProfile) { p.PushProvider = "sub2" })
	if err != nil {
		t.Fatal(err)
	}
	p, err = f.s.saveProCostSnapshot(p, v, sub2.AccountCosts{StandardCostUSD: 2})
	if err != nil || p.UserCostDownstreamIdentity != "" || p.CostCheckedAt == nil {
		t.Fatal("missing user cost became known", err)
	}
	zero := 0.0
	p, err = f.s.saveProCostSnapshot(p, v, sub2.AccountCosts{StandardCostUSD: 2, UserCostUSD: &zero})
	if err != nil || p.UserCostDownstreamIdentity != "42" || p.UserCostDownstreamSnapshot != 0 {
		t.Fatal("zero user cost not preserved", err)
	}
	old := p
	p, err = f.s.store.UpdateProAccount(f.email, func(p *model.MailAccountProfile) { p.Sub2AccountID = 43 })
	if err != nil || p.CostCheckedAt != nil || p.UserCostDownstreamIdentity != "" {
		t.Fatal("rebind retained cost", err)
	}
	p, err = f.s.saveProCostSnapshot(old, v, sub2.AccountCosts{StandardCostUSD: 99})
	if err != nil || p.CostCheckedAt != nil {
		t.Fatal("stale response overwritten new binding", err)
	}
	p, err = f.s.saveProCostSnapshot(p, v, sub2.AccountCosts{StandardCostUSD: 1})
	if err != nil || p.CostDownstreamSnapshot != 1 || p.UserCostDownstreamIdentity != "" {
		t.Fatal("new identity reused old user cost", err)
	}
	v.Sub2.URL = "https://different.example.test"
	if _, err = f.s.store.SaveProSettings(v, "", ""); err != nil {
		t.Fatal(err)
	}
	rows, err := f.s.proAccountListWithCards([]model.MailAccountProfile{p})
	if err != nil || rows[0].CostCheckedAt != nil {
		t.Fatal("different server exposed previous cost", err)
	}
	p, err = f.s.saveProCostSnapshot(p, v, sub2.AccountCosts{StandardCostUSD: 0.5})
	if err != nil || p.CostDownstreamSnapshot != 0.5 {
		t.Fatal("new server inherited previous maximum", err)
	}
	p, err = f.s.store.UpdateProAccount(f.email, func(p *model.MailAccountProfile) { p.PushProvider = "cpa" })
	if err != nil || p.CostCheckedAt != nil {
		t.Fatal("provider change retained cost", err)
	}
}

func TestProCostUnsupportedProviderDoesNotQueryStats(t *testing.T) {
	f := newProExecutionFixture(t)
	var stats atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "stats") {
			stats.Add(1)
		}
		http.Error(w, "fixture CPA unavailable", 400)
	}))
	defer ts.Close()
	v, _, _, _ := f.s.store.ProSettings()
	v.Provider, v.CPA.URL, v.Sub2.URL = "cpa", ts.URL, ts.URL
	if _, err := f.s.store.SaveProSettings(v, "fixture", "fixture"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.store.UpdateProAccount(f.email, func(p *model.MailAccountProfile) { p.PushProvider = "cpa" }); err != nil {
		t.Fatal(err)
	}
	_, _ = f.s.performProQuota(t.Context(), f.email, false)
	if stats.Load() != 0 {
		t.Fatal("unsupported provider queried stats")
	}
}
