package restock

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"
)

func TestCapacityPercentDefaultAndValidation(t *testing.T) {
	c := DefaultConfig()
	if c.CapacityPercent != 60 || c.EffectiveCapacity(80) != 48 {
		t.Fatal("default should reserve 60% of reported capacity", c)
	}
	for _, percent := range []int{-1, 0, 101} {
		c.CapacityPercent = percent
		if c.Validate() == nil {
			t.Fatal("accepted invalid capacity percent", percent)
		}
	}
	for _, percent := range []int{1, 60, 100} {
		c.CapacityPercent = percent
		if err := c.Validate(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestCapacityPercentTriggerRoundsDownAndSizesWithEffectiveCapacity(t *testing.T) {
	c, s, now := fixture()
	c.CapacityPercent, c.TriggerPercent, c.TargetPercent = 60, 70, 50
	c.TimeEnabled, c.MinBatch = false, 1
	s.Accounts = []Account{{ID: 1, Ready: true, Capacity: 40, Used: 16}, {ID: 2, Ready: true, Capacity: 40, Used: 16}}
	if Pressure(c, s) {
		t.Fatal("32 must not reach floor(80 * 60% * 70%) = 33")
	}
	s.Accounts[1].Used = 17
	before, _ := json.Marshal(s)
	if !Pressure(c, s) {
		t.Fatal("33 must reach the floored threshold even though utilization is 68.75%")
	}
	if d := Evaluate(c, s, nil, Limits{}, 1, 40, 2, now); d.ConcurrencyNeed != 0 {
		t.Fatal("confirmation requirement was bypassed", d)
	}
	d := Evaluate(c, s, nil, Limits{}, 2, 40, 2, now)
	if d.ReportedCapacity != 80 || d.Capacity != 48 || d.Trace.FutureCapacity != 48 || d.Trace.ConcurrencyTrigger != 33 || d.Trace.EffectiveNewAccountCapacity != 24 || d.ConcurrencyNeed != 1 {
		t.Fatalf("expected one new 24-slot account for demand 33 / 50%% minus capacity 48: %+v", d)
	}
	// The stored task keeps the real 40-slot setting. Its effective capacity is
	// recomputed using the current ratio, not multiplied twice after saving.
	eta := now.Add(time.Minute)
	task := Task{ID: "pending", Status: "running", Binding: s.Binding, GroupID: s.GroupID, Capacity: 40, ETA: &eta}
	d = Evaluate(c, s, []Task{task}, Limits{}, 2, 40, 2, now)
	if d.ConcurrencyNeed != 0 || d.Capacity != 48 || d.Trace.NewAccountCapacity != 40 {
		t.Fatal("pending effective capacity not deducted once", d)
	}
	task.Status = "unknown"
	if d = Evaluate(c, s, []Task{task}, Limits{}, 2, 40, 2, now); d.ConcurrencyNeed != 1 {
		t.Fatal("unknown payment must not promise capacity", d)
	}
	c.CapacityPercent = 100
	if d = Evaluate(c, s, nil, Limits{}, 2, 40, 2, now); Pressure(c, s) || d.Capacity != 80 || d.ConcurrencyNeed != 0 {
		t.Fatal("ratio change must use the original snapshot", d)
	}
	after, _ := json.Marshal(s)
	if string(before) != string(after) || task.Capacity != 40 {
		t.Fatal("scaling mutated source data")
	}
}

func TestCapacityPercentFractionalAndIdle(t *testing.T) {
	c, s, now := fixture()
	c.CapacityPercent, c.TriggerPercent = 60, 70
	s.Accounts = []Account{{ID: 1, Ready: true, Capacity: 1}, {ID: 2, Ready: true, Capacity: 2}}
	d := Evaluate(c, s, nil, Limits{}, 2, 1, 2, now)
	if d.Capacity != 1.8 || d.Trace.EffectiveNewAccountCapacity != .6 || d.Trace.ConcurrencyTrigger != 1 {
		t.Fatal("fractional capacity must not be rounded per account", d)
	}
	s.Accounts = s.Accounts[:1]
	if c.ConcurrencyTrigger(1) != 0 || Pressure(c, s) {
		t.Fatal("a rounded zero trigger cannot make an idle group under pressure")
	}
}

func TestCapacityPercentExitAndMoneyUseSeparateInputs(t *testing.T) {
	c, s, now := exitFixture()
	c.CapacityPercent = 60
	baseline := Evaluate(c, s, nil, Limits{}, 2, 50, 2, now)
	if baseline.Exit.CurrentCapacity != 60 || baseline.Exit.AfterCapacity != 30 || baseline.Exit.Rows[0].Capacity != 30 || baseline.Exit.Points[0].Capacity != 30 || baseline.ExitNeed != 2 {
		t.Fatal("exit forecast used unscaled capacity", baseline.Exit)
	}
	eta := now.Add(time.Minute)
	task := Task{ID: "replacement", Status: "running", Binding: s.Binding, GroupID: s.GroupID, Capacity: 50, ETA: &eta}
	d := Evaluate(c, s, []Task{task}, Limits{}, 2, 50, 2, now)
	if d.Exit.Points[0].PendingCapacity != 30 || d.Exit.Points[0].SizingCapacity != 60 || d.ExitNeed != 1 {
		t.Fatal("exit pending/new account capacities must use the same percentage", d.Exit)
	}
	c.CapacityPercent = 100
	unscaled := Evaluate(c, s, nil, Limits{}, 2, 50, 2, now)
	if !reflect.DeepEqual(baseline.Money, unscaled.Money) || !reflect.DeepEqual(baseline.SupplyMinutes, unscaled.SupplyMinutes) {
		t.Fatal("capacity percentage changed the dollar-based supply forecast")
	}
	// The same downward rounding applies before a departing account is removed.
	c.CapacityPercent, c.TriggerPercent, c.TargetPercent = 60, 70, 50
	s.Accounts[0].Capacity, s.Accounts[1].Capacity = 80, 80
	s.Accounts[0].Used, s.Accounts[1].Used = 16, 17
	if d = Evaluate(c, s, nil, Limits{}, 2, 80, 2, now); !d.Exit.Pressure || d.Exit.AfterCapacity != 48 || d.ExitNeed != 1 {
		t.Fatal("exit threshold floor(48 * 70%) = 33 not honored", d.Exit)
	}
}
