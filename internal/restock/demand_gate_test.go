package restock

import (
	"testing"
	"time"
)

func TestDemandGateContinuousNightCycle(t *testing.T) {
	c := DefaultConfig()
	c.NoRestockEnabled = true
	c.NoRestockWindows = []DailyWindow{{Start: "23:00", End: "09:00", HighDemandEnabled: true, HighDemandThreshold: 20, HighDemandConfirmations: 3}}
	now := time.Date(2026, 10, 21, 23, 59, 50, 0, scheduleLocation)
	feed := func(at time.Time, main, fallback int, valid bool) DemandGate {
		g := c.AdvanceDemand(DemandGate{Scope: "scope", Valid: valid, MainAt: at, FallbackAt: at, ValidUntil: at.Add(20 * time.Second), MonitorUsed: main, FallbackUsed: fallback, TotalUsed: main + fallback}, at)
		c.DemandGate = &g
		return g
	}
	g := feed(now, 0, 25, true)
	if g.Allowed || g.Windows[0].Consecutive != 1 {
		t.Fatal(g)
	}
	g = feed(now, 0, 25, true)
	if g.Windows[0].Consecutive != 1 {
		t.Fatal("recounted same sample", g)
	}
	g = feed(now.Add(5*time.Second), 0, 25, true)
	if g.Allowed || g.Windows[0].Consecutive != 2 {
		t.Fatal(g)
	}
	g = feed(now.Add(10*time.Second), 25, 0, true)
	if !g.Allowed || c.NoRestockReason(now.Add(10*time.Second)) != "" {
		t.Fatal("midnight/traffic transfer reset count", g)
	}
	g = feed(now.Add(15*time.Second), 19, 0, true)
	if g.Allowed || g.Windows[0].Consecutive != 0 {
		t.Fatal("low traffic did not reblock", g)
	}
	g = feed(now.Add(20*time.Second), 0, 25, true)
	if g.Allowed || g.Windows[0].Consecutive != 1 {
		t.Fatal(g)
	}
	g = feed(now.Add(25*time.Second), 0, 25, false)
	if g.Valid || g.Allowed || g.Windows[0].Consecutive != 0 {
		t.Fatal("failure counted", g)
	}
	for i := 6; i < 9; i++ {
		g = feed(now.Add(time.Duration(i)*5*time.Second), 0, 25, true)
	}
	if !g.Allowed {
		t.Fatal(g)
	}
	if v := c.DemandStatus(now.Add(61 * time.Second)); v.Allowed || v.Valid {
		t.Fatal("expired authorization", v)
	}
	end := time.Date(2026, 10, 22, 9, 0, 0, 0, scheduleLocation)
	if c.DemandStatus(end).Active || c.NoRestockReason(end) != "" {
		t.Fatal("end boundary")
	}
	if c.DemandStatus(now.AddDate(0, 0, 1)).Allowed {
		t.Fatal("previous night reused")
	}
}

func TestDemandGateScopeOverlapAndValidation(t *testing.T) {
	c := DefaultConfig()
	c.NoRestockEnabled = true
	c.NoRestockWindows = []DailyWindow{{Start: "02:00", End: "09:00", HighDemandEnabled: true, HighDemandThreshold: 20, HighDemandConfirmations: 1}}
	now := time.Date(2026, 10, 21, 3, 0, 0, 0, scheduleLocation)
	sample := DemandGate{Scope: "old", Valid: true, MainAt: now, ValidUntil: now.Add(time.Minute), TotalUsed: 25}
	g := c.AdvanceDemand(sample, now)
	c.DemandGate = &g
	c.NoRestockWindows = append(c.NoRestockWindows, DailyWindow{Start: "02:30", End: "04:00"})
	if c.DemandStatus(now).Allowed || c.NoRestockReason(now) == "" {
		t.Fatal("strict overlapping interval bypassed")
	}
	c.NoRestockWindows = c.NoRestockWindows[:1]
	c.Version++
	if c.DemandStatus(now).Allowed {
		t.Fatal("old config allowed")
	}
	c.NoRestockWindows[0].HighDemandConfirmations = 2
	sample.Scope = "new"
	g = c.AdvanceDemand(sample, now)
	if g.Allowed || g.Windows[0].Consecutive != 1 {
		t.Fatal(g)
	}
	c.NoRestockWindows[0].AllowedProvider = "nexovcc"
	if c.Validate() == nil {
		t.Fatal("issuer exception accepted high demand")
	}
	c.NoRestockWindows[0].AllowedProvider = ""
	c.NoRestockWindows[0].HighDemandThreshold = 0
	if c.Validate() == nil {
		t.Fatal("zero threshold accepted")
	}
}
