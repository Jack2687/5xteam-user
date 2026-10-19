package httpapi

import (
	"chatgpt-space-merge/internal/ejh"
	"chatgpt-space-merge/internal/gptpay"
	"chatgpt-space-merge/internal/model"
	"chatgpt-space-merge/internal/restock"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestFallbackScheduledWindowBoundaries(t *testing.T) {
	for _, emptyAfter := range []bool{false, true} {
		t.Run(map[bool]string{false: "nonempty", true: "empty"}[emptyAfter], func(t *testing.T) {
			s, st, m, now := fallbackFixture(t)
			c, _ := st.ProRestockConfig()
			c.NoRestockWindows = []restock.DailyWindow{{Start: "17:00", End: "20:50"}}
			c.NoRestockFallback = true
			if _, err := st.SaveProRestockConfig(c); err != nil {
				t.Fatal(err)
			}
			f, id, _ := st.ProFallbackConfig()
			f.CheckSeconds = 300
			if _, err := st.SaveProFallbackConfig(f, id); err != nil {
				t.Fatal(err)
			}
			setTime := func(at string) { *now, _ = time.Parse(time.RFC3339, "2026-10-16T"+at+"+08:00") }
			m.sourceCount = 2
			setTime("16:59:59")
			if err := s.tickProFallback(context.Background(), false); err != nil {
				t.Fatal(err)
			}
			if m.on[1] || m.on[2] {
				t.Fatal("enabled early")
			}
			m.sourceFailure = true // The schedule must not wait for a failing primary sampler.
			setTime("17:00:00")
			if err := s.tickProFallback(context.Background(), false); err != nil {
				t.Fatal(err)
			}
			if !m.on[1] || !m.on[2] || m.writes[3] != 0 {
				t.Fatal("missed window start or touched unselected account", m.on)
			}
			w := httptest.NewRecorder()
			s.getProFallback(w, httptest.NewRequest("GET", "/", nil))
			if !strings.Contains(w.Body.String(), `"phase":"scheduled"`) {
				t.Fatal(w.Body.String())
			}
			setTime("20:49:59")
			if err := s.tickProFallback(context.Background(), false); err != nil {
				t.Fatal(err)
			}
			if m.writes[1] != 1 {
				t.Fatal("repeated scheduling write", m.writes)
			}
			m.sourceFailure = false
			if emptyAfter {
				m.sourceCount = 0
			}
			setTime("20:50:00")
			if err := s.tickProFallback(context.Background(), false); err != nil {
				t.Fatal(err)
			}
			if m.on[1] != emptyAfter || m.on[2] != emptyAfter {
				t.Fatal("did not restore normal empty-group rule", m.on)
			}
			// Explicitly disabling fallback wins even inside a matching pause window.
			setTime("17:30:00")
			f, id, _ = st.ProFallbackConfig()
			f.Enabled = false
			if _, err := st.SaveProFallbackConfig(f, id); err != nil {
				t.Fatal(err)
			}
			if err := s.tickProFallback(context.Background(), true); err != nil {
				t.Fatal(err)
			}
			if m.on[1] || m.on[2] || m.writes[3] != 0 {
				t.Fatal("disabled fallback was overridden")
			}
		})
	}
}

func TestFallbackNexovccExceptionUsesNormalEmptyGroupRule(t *testing.T) {
	for _, forceFallback := range []bool{false, true} {
		t.Run(fmt.Sprint(forceFallback), func(t *testing.T) {
			s, st, m, now := fallbackFixture(t)
			c, _ := st.ProRestockConfig()
			c.NoRestockWindows = []restock.DailyWindow{{Start: "02:00", End: "09:00"}, {Start: "17:00", End: "20:00", AllowedProvider: "nexovcc"}}
			c.NoRestockFallback = forceFallback
			if _, err := st.SaveProRestockConfig(c); err != nil {
				t.Fatal(err)
			}
			for _, row := range []struct {
				at    string
				count int
				want  bool
			}{
				{"01:59:59", 2, false}, {"02:00:00", 2, forceFallback}, {"08:59:59", 2, forceFallback}, {"09:00:00", 2, false},
				{"17:00:00", 2, false}, {"17:29:59", 0, true}, {"17:30:00", 2, false}, {"19:59:59", 2, false}, {"20:00:00", 0, true}, {"20:50:00", 2, false},
			} {
				*now, _ = time.Parse(time.RFC3339, "2026-10-18T"+row.at+"+08:00")
				m.sourceCount = row.count
				if err := s.tickProFallback(context.Background(), true); err != nil {
					t.Fatal(err)
				}
				if m.on[1] != row.want || m.on[2] != row.want || m.writes[3] != 0 {
					t.Fatal("wrong fallback in issuer exception", row, m.on)
				}
			}
		})
	}
}

func TestDailyEJHMergeTail(t *testing.T) {
	for _, mode := range []string{"no-timeout", "unknown-timeout", "disabled", "retry", "unknown-result", "new-lifecycle"} {
		t.Run(mode, func(t *testing.T) {
			s, st, email := gptPayFixture(t, func(http.ResponseWriter, *http.Request) { t.Error("unexpected upstream payment/login") })
			configureProAutoFixture(t, s)
			if mode == "retry" {
				cfg, _, _, _ := st.ProSettings()
				cfg.RetryCount = 1
				if _, err := st.SaveProSettings(cfg, "", ""); err != nil {
					t.Fatal(err)
				}
			}
			enableEJHPaymentFixture(t, st)
			card, err := st.ImportEJHCard(ejh.Card{OrderNo: "daily-card", Number: "4242424242424242", Validity: "1299", CVV: "012", Status: "1", CardCurrency: "USD"}, true)
			if err != nil {
				t.Fatal(err)
			}
			now := time.Now().Add(-time.Minute)
			o := gptpay.Order{ID: "daily-paid", Email: email, CardID: card.ID, Status: "success", PlanCode: "pro20", CreatedAt: now, UpdatedAt: now}
			snapshot := gptpay.Snapshot{Input: gptpay.CreateInput{CardSecret: gptpay.CardSecret{Number: "4242424242424242", Year: 2099, Month: 12, CVV: "012"}}}
			if err = st.CreateGPTPayOrder(o, snapshot); err != nil {
				t.Fatal(err)
			}
			c := restock.DefaultConfig()
			c.DailyEJHMergeEnabled = mode != "disabled"
			c.DailyEJHMergeTime = "17:30"
			if _, err = st.SaveProRestockConfig(c); err != nil {
				t.Fatal(err)
			}
			_, err = st.UpdateProAccount(email, func(p *model.MailAccountProfile) {
				disabled := false
				p.ProPushTimeoutEnabled = &disabled
				if mode == "unknown-timeout" {
					p.ProPushTimeoutEnabled = nil
				}
				p.PushStatus = "completed"
				p.ProWorkflowRunning = true
				p.ProAuto = model.ProAutoState{ID: "daily-auto", OrderID: o.ID, Status: "running", Steps: map[string]string{"push": "completed", "oauth": "completed", "recharge": "completed"}, QuotaUsedThreshold: 80, QuotaIntervalSeconds: 30}
				p.ProDailyMerge = &model.ProDailyMergeRequest{At: now, OrderID: o.ID, AutomationID: "daily-auto"}
				if mode == "new-lifecycle" {
					p.ProDailyMerge.AutomationID = "old-auto"
				}
				if mode == "disabled" {
					p.ProMergeTrigger = "daily_ejh"
					p.ProAuto.Steps["quota"] = "skipped"
				}
			})
			if err != nil {
				t.Fatal(err)
			}
			quotaCalls, mergeCalls := 0, 0
			s.proAutoHooks = &proAutomationHooks{Quota: func(context.Context, string, bool) (model.MailAccountProfile, error) {
				quotaCalls++
				return st.UpdateProAccount(email, func(p *model.MailAccountProfile) { p.Quota7D = &model.FreeQuotaWindow{UsedPercent: 1} })
			}, Merge: func(context.Context, string) (model.MailAccountProfile, error) {
				mergeCalls++
				p, _, e := st.MailAccountCredential(email)
				if mode == "unknown-result" {
					p, e = st.UpdateProAccount(email, func(p *model.MailAccountProfile) { p.TransferStatus = "unknown" })
				}
				if mode == "retry" || mode == "unknown-result" {
					e = errors.New("fixture merge failure")
				}
				return p, e
			}}
			err = s.runProAutomationTail(context.Background(), email)
			p, _, _ := st.MailAccountCredential(email)
			if mode == "unknown-result" {
				if err == nil || p.ProAuto.NextCheckAt != nil {
					t.Fatal("uncertain result retried")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if mode == "disabled" || mode == "new-lifecycle" {
				if quotaCalls != 1 || mergeCalls != 0 {
					t.Fatal("invalid request bypassed quota")
				}
				return
			}
			if quotaCalls != 0 || mergeCalls != 1 || p.ProDailyMerge.Attempts != 1 || p.ProForceMergeAttempts != 0 {
				t.Fatal("daily merge did not bypass quota independently", p.ProAuto)
			}
			if mode == "retry" {
				if p.ProAuto.Status != "waiting_quota" || p.ProDailyMerge.NextAt == nil || !p.ProDailyMerge.NextAt.After(time.Now()) {
					t.Fatal("retry did not back off")
				}
				cfg, _, _, _ := st.ProSettings()
				p.ProWorkflowRunning = false
				if s.forcedMergeReason(p, cfg, time.Now()) != "" {
					t.Fatal("backoff ignored")
				}
				p.ProDailyMerge.NextAt = nil
				p.ProAuto.StopRequested = true
				if s.forcedMergeReason(p, cfg, time.Now()) != "" {
					t.Fatal("manual stop ignored")
				}
			} else if !p.ProDailyMerge.Completed {
				t.Fatal("success not marked completed")
			}
		})
	}
}
