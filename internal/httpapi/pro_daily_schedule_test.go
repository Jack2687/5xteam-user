package httpapi

import (
	"chatgpt-space-merge/internal/ejh"
	"chatgpt-space-merge/internal/gptpay"
	"chatgpt-space-merge/internal/model"
	"chatgpt-space-merge/internal/restock"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestFallbackNoRestockWindowsUseEmptyGroupRule(t *testing.T) {
	s, st, m, now := fallbackFixture(t)
	c, _ := st.ProRestockConfig()
	c.NoRestockWindows = []restock.DailyWindow{{Start: "02:00", End: "09:00"}, {Start: "17:00", End: "20:00", AllowedProvider: "nexovcc"}}
	if _, err := st.SaveProRestockConfig(c); err != nil {
		t.Fatal(err)
	}
	for _, row := range []struct {
		at    string
		count int
	}{
		{"01:59:59", 2}, {"02:00:00", 2}, {"02:30:00", 0}, {"03:00:00", 2}, {"08:59:59", 0}, {"09:00:00", 0}, {"09:01:00", 2},
		{"17:00:00", 2}, {"17:29:59", 0}, {"17:30:00", 2}, {"19:59:59", 2}, {"20:00:00", 0}, {"20:50:00", 2},
	} {
		*now, _ = time.Parse(time.RFC3339, "2026-10-18T"+row.at+"+08:00")
		m.sourceCount = row.count
		if err := s.tickProFallback(context.Background(), true); err != nil {
			t.Fatal(err)
		}
		want := row.count == 0
		if m.on[1] != want || m.on[2] != want || m.writes[3] != 0 {
			t.Fatal("schedule changed empty-group rule", row, m.on)
		}
		w := httptest.NewRecorder()
		s.getProFallback(w, httptest.NewRequest("GET", "/", nil))
		phase := `"phase":"off"`
		if want {
			phase = `"phase":"active"`
		}
		if !strings.Contains(w.Body.String(), phase) {
			t.Fatal(w.Body.String())
		}
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
