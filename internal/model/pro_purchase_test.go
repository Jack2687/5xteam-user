package model

import (
	"testing"
	"time"
)

func TestProPurchaseWindowsBoundaries(t *testing.T) {
	p := DefaultProSettings()
	p.PurchaseScheduleEnabled = true
	p.PurchaseWindows = []ProPurchaseWindow{{Start: "17:00", End: "20:50", CardProvider: "nexovcc", PlanCode: "pro20"}, {Start: "23:00", End: "06:00", CardProvider: "ejh", PlanCode: "pro5"}}
	if err := p.ValidatePurchaseWindows(); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ clock, plan string }{{"16:59", ""}, {"17:00", "pro20"}, {"20:49", "pro20"}, {"20:50", ""}, {"23:00", "pro5"}, {"00:00", "pro5"}, {"05:59", "pro5"}, {"06:00", ""}} {
		now, _ := time.ParseInLocation("2006-01-02 15:04", "2026-10-19 "+tc.clock, time.FixedZone("CST", 8*3600))
		w, ok := p.PurchaseWindow(now.UTC())
		if ok != (tc.plan != "") || w.PlanCode != tc.plan {
			t.Fatal(tc, w, ok)
		}
	}
	p.PurchaseScheduleEnabled = false
	if _, ok := p.PurchaseWindow(time.Date(2026, 10, 19, 10, 0, 0, 0, time.UTC)); ok {
		t.Fatal("disabled rule matched")
	}
}

func TestProPurchaseWindowsRejectOverlapAndInvalid(t *testing.T) {
	for _, windows := range [][]ProPurchaseWindow{
		{{Start: "23:00", End: "06:00", CardProvider: "ejh", PlanCode: "pro5"}, {Start: "05:00", End: "07:00", CardProvider: "nexovcc", PlanCode: "pro20"}},
		{{Start: "17:00", End: "17:00", CardProvider: "ejh", PlanCode: "pro5"}},
		{{Start: "24:00", End: "06:00", CardProvider: "ejh", PlanCode: "pro5"}},
		{{Start: "17:00", End: "20:50", CardProvider: "bad", PlanCode: "pro5"}},
		{{Start: "17:00", End: "20:50", CardProvider: "ejh", PlanCode: "pro50"}},
	} {
		p := DefaultProSettings()
		p.PurchaseWindows = windows
		if p.ValidatePurchaseWindows() == nil {
			t.Fatal("invalid rules accepted", windows)
		}
	}
}
