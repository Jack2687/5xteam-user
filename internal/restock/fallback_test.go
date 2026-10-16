package restock

import (
	"testing"
	"time"
)

func TestFallbackAccountCountPlan(t *testing.T) {
	f := DefaultFallbackConfig()
	f.Enabled = true
	f.URL = "https://example.test"
	f.Email = "admin@example.test"
	f.GroupID = 7
	f.AccountIDs = []int64{1}
	now := time.Now()
	for _, count := range []int{0, 1, 20} {
		for _, hour := range []int{0, 5, 9, 12, 23} {
			at := time.Date(2026, 10, 16, hour, 0, 0, 0, time.UTC)
			p := f.Plan(at, FallbackObservation{GroupID: 2, Count: &count, At: at})
			if p.Hold || p.Active != (count == 0) {
				t.Fatal(count, hour, p)
			}
		}
	}
	zero := 0
	for _, o := range []FallbackObservation{
		{}, {GroupID: 2, Count: &zero, At: now, Error: "offline"},
		{GroupID: 2, Count: &zero, At: now.Add(-11 * time.Second)},
		{GroupID: 2, Count: &zero, At: now.Add(2 * time.Second)},
		{GroupID: 2, Count: &zero},
	} {
		if p := f.Plan(now, o); !p.Hold || p.Active {
			t.Fatal(p)
		}
	}
	f.Enabled = false
	if p := f.Plan(now, FallbackObservation{}); p.Hold || p.Active || p.Phase != "disabled" {
		t.Fatal(p)
	}
}
func TestFallbackConfigValidation(t *testing.T) {
	c := DefaultFallbackConfig()
	if c.Enabled || c.CheckSeconds != 3 || c.Validate() != nil {
		t.Fatal(c)
	}
	for _, mutate := range []func(*FallbackConfig){
		func(c *FallbackConfig) { c.CheckSeconds = 1 }, func(c *FallbackConfig) { c.CheckSeconds = 301 },
		func(c *FallbackConfig) { c.Enabled = true }, func(c *FallbackConfig) { c.URL = "https://user:secret@host" },
		func(c *FallbackConfig) { c.AccountIDs = []int64{1, 1} }, func(c *FallbackConfig) { c.AccountIDs = []int64{0} },
	} {
		v := c
		mutate(&v)
		if v.Validate() == nil {
			t.Fatal("accepted invalid config", v)
		}
	}
}
