package restock

import "testing"

func TestCapacityTiersBoundariesAndEffectiveCapacity(t *testing.T) {
	c := DefaultConfig()
	c.CapacityPercent = 100
	c.CapacityTiersEnabled = true
	c.CapacityTiers = []CapacityTier{{120, 85, 80}, {0, 80, 75}}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ capacity, trigger, target, count int }{{0, 80, 75, 0}, {60, 80, 75, 48}, {119, 80, 75, 95}, {120, 85, 80, 102}, {240, 85, 80, 204}} {
		trigger, target := c.CapacityThresholds(tc.capacity)
		if trigger != tc.trigger || target != tc.target || c.ConcurrencyTrigger(tc.capacity) != tc.count {
			t.Fatal(tc, trigger, target, c.ConcurrencyTrigger(tc.capacity))
		}
	}
	c.CapacityPercent = 50
	if trigger, _ := c.CapacityThresholds(120); trigger != 80 {
		t.Fatal("raw capacity selected tier")
	}
	c.CapacityTiersEnabled = false
	if trigger, target := c.CapacityThresholds(60); trigger != c.TriggerPercent || target != c.TargetPercent {
		t.Fatal("disabled tiers changed fixed policy")
	}
	c.CapacityTiersEnabled = true
	c.CapacityTiers = []CapacityTier{{60, 80, 75}}
	if c.Validate() == nil {
		t.Fatal("missing zero bound accepted")
	}
	c.CapacityTiers = []CapacityTier{{0, 80, 80}}
	if c.Validate() == nil {
		t.Fatal("target at trigger accepted")
	}
}
