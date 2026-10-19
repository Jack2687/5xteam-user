package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"chatgpt-space-merge/internal/gptpay"
	"chatgpt-space-merge/internal/model"
	"chatgpt-space-merge/internal/restock"
	"chatgpt-space-merge/internal/store"
)

func timetableFixture() (model.ProSettings, gptpay.Settings, restock.Config) {
	p := model.DefaultProSettings()
	p.PurchaseScheduleEnabled = true
	p.PurchaseWindows = []model.ProPurchaseWindow{
		{Start: "20:50", End: "09:00", CardProvider: "ejh", PlanCode: "pro5"},
		{Start: "09:00", End: "20:50", CardProvider: "ejh", PlanCode: "pro20"},
	}
	p.EJHPlanTemplates = map[string]string{"pro5": "ejh5", "pro20": "ejh20"}
	p.NexovccPlanTemplates = map[string]string{"pro5": "global5", "pro20": "global20"}
	c := restock.DefaultConfig()
	c.NoRestockEnabled = true
	c.NoRestockWindows = []restock.DailyWindow{{Start: "02:00", End: "09:00"}, {Start: "17:00", End: "20:50"}}
	return p, gptpay.Settings{PlanCode: "pro5"}, c
}

func TestProRestockTimetableBoundaries(t *testing.T) {
	p, pay, c := timetableFixture()
	for _, tc := range []struct {
		at, plan string
		blocked  bool
	}{
		{"01:59", "pro5", false}, {"02:00", "", true}, {"08:59", "", true},
		{"09:00", "pro20", false}, {"16:59", "pro20", false}, {"17:00", "", true},
		{"20:49", "", true}, {"20:50", "pro5", false}, {"23:59", "pro5", false}, {"00:00", "pro5", false},
	} {
		t.Run(tc.at, func(t *testing.T) {
			at, _ := time.Parse(time.RFC3339, "2026-10-19T"+tc.at+":00+08:00")
			// The server timezone must not shift business-time boundaries.
			v := proRestockDailyTimetable(p, pay, c, at.UTC())
			if len(v.Periods) != 4 || v.Current.Blocked != tc.blocked || v.Current.Plan != tc.plan {
				t.Fatalf("unexpected timetable: %+v", v)
			}
			last := v.Periods[3]
			if last.StartMinute != 1250 || last.EndMinute != 120 || last.Plan != "pro5" {
				t.Fatalf("midnight interval not joined: %+v", last)
			}
		})
	}
}

func TestProRestockTimetableMatchesDispatch(t *testing.T) {
	p, pay, c := timetableFixture()
	// Provider exceptions override the issuer, keeping the scheduled plan.
	c.NoRestockWindows[1].AllowedProvider = "nexovcc"
	// Complete prohibition must still win over an overlapping exception.
	c.NoRestockWindows = append(c.NoRestockWindows, restock.DailyWindow{Start: "18:00", End: "18:30"})
	start := time.Date(2026, 10, 19, 0, 0, 0, 0, time.FixedZone("CST", 8*3600))
	for m := 0; m < 1440; m++ {
		at := start.Add(time.Duration(m) * time.Minute)
		v := proRestockDailyTimetable(p, pay, c, at).Current
		if blocked := c.NoRestockReason(at) != ""; v.Blocked != blocked {
			t.Fatalf("minute %d: prohibition differs from dispatch", m)
		} else if blocked {
			continue
		}
		selected, payment, err := store.ResolveProPurchase(p, pay, c, at)
		if err != nil || v.Provider != selected.AutoCardProvider() || v.Plan != payment.PlanCode {
			t.Fatalf("minute %d: timetable differs from dispatch: %+v, %v", m, v, err)
		}
	}
}

func TestProRestockTimetableDefaultsAndInvalidConfig(t *testing.T) {
	p, pay, c := timetableFixture()
	now := time.Date(2026, 10, 19, 12, 0, 0, 0, time.FixedZone("CST", 8*3600))
	p.PurchaseWindows = p.PurchaseWindows[:1]
	v := proRestockDailyTimetable(p, pay, c, now)
	if v.Current.Source != "default" || v.Current.Plan != "pro5" {
		t.Fatal("uncovered time must use default purchase settings", v)
	}
	p.PurchaseScheduleEnabled, c.NoRestockEnabled = false, false
	p.CardProvider, pay.PlanCode = "nexovcc", "pro20"
	v = proRestockDailyTimetable(p, pay, c, now)
	if len(v.Periods) != 1 || v.Current.StartMinute != 0 || v.Current.EndMinute != 1440 || v.Current.Provider != "nexovcc" || v.Current.Plan != "pro20" {
		t.Fatal("disabled windows should produce all-day defaults", v)
	}
	c.NoRestockEnabled, c.NoRestockWindows = true, nil
	c.NoRestockStart, c.NoRestockEnd = "23:00", "13:00"
	v = proRestockDailyTimetable(p, pay, c, now)
	if !v.Current.Blocked || v.Current.StartMinute != 1380 || v.Current.EndMinute != 780 {
		t.Fatal("legacy overnight prohibition missing", v)
	}
	c.NoRestockEnd = "bad"
	v = proRestockDailyTimetable(p, pay, c, now)
	if len(v.Periods) != 1 || !v.Current.Blocked || v.Current.Error == "" {
		t.Fatal("invalid prohibition must not look available", v)
	}
	p, pay, c = timetableFixture()
	delete(p.EJHPlanTemplates, "pro20")
	v = proRestockDailyTimetable(p, pay, c, now)
	if v.Current.Error == "" || v.Current.Plan != "pro20" {
		t.Fatal("missing template must retain intended plan and report error", v)
	}
}

func TestProRestockAPIScheduledProvider(t *testing.T) {
	s, st, _ := gptPayFixture(t, func(http.ResponseWriter, *http.Request) { t.Error("dashboard requested supplier") })
	s.proAutoCancel()
	p, _, _, _ := st.ProSettings()
	p.CardProvider, p.PurchaseScheduleEnabled = "ejh", true
	p.PurchaseWindows = []model.ProPurchaseWindow{activePurchaseWindow("nexovcc", "pro20")}
	p.NexovccPlanTemplates = map[string]string{"pro20": "global20"}
	if _, err := st.SaveProSettings(p, "", ""); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	s.getProRestock(rec, httptest.NewRequest(http.MethodGet, "/api/pro-restock", nil))
	var result struct {
		Data struct {
			Provider  string              `json:"card_provider"`
			Timetable proRestockTimetable `json:"purchase_timetable"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil || rec.Code != 200 {
		t.Fatal(rec.Code, rec.Body.String(), err)
	}
	if result.Data.Provider != "nexovcc" || result.Data.Timetable.Current.Plan != "pro20" {
		t.Fatal("API displayed default issuer instead of scheduled purchase", result)
	}
}
