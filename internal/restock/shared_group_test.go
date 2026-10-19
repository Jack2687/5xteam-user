package restock

import (
	"reflect"
	"testing"
)

func TestMonitoredSharedAccountRetainsAllForecasts(t *testing.T) {
	c, s, now := fixture()
	want := Evaluate(c, s, nil, Limits{}, 2, 20, 2, now)
	s.Accounts[0].Shared = true
	got := Evaluate(c, s, nil, Limits{}, 2, 20, 2, now)
	want.Snapshot.Accounts[0].Shared = true
	if !Pressure(c, s) || !reflect.DeepEqual(got, want) {
		t.Fatalf("other group membership changed monitoring: %+v", got)
	}
	c, s, now = exitFixture()
	s.Accounts[0].Shared, s.Accounts[1].Shared = true, true
	got = Evaluate(c, s, nil, Limits{}, 0, 50, 2, now)
	if !got.DemandKnown || got.Exit == nil || !got.Exit.Valid || got.ExitNeed != 1 || got.Exit.AfterCapacity != 50 {
		t.Fatalf("shared accounts lost exit protection: %+v", got)
	}
}

func TestFallbackMultipleGroupsAndLegacySelection(t *testing.T) {
	c := DefaultFallbackConfig()
	c.GroupID = 7
	c.NormalizeGroups()
	if !reflect.DeepEqual(c.GroupIDs, []int64{7}) {
		t.Fatal(c)
	}
	c.GroupIDs = []int64{7, 8}
	if c.Validate() != nil {
		t.Fatal(c)
	}
	for _, ids := range [][]int64{{7, 7}, {0}, {-1}} {
		c.GroupIDs = ids
		if c.Validate() == nil {
			t.Fatal("invalid groups accepted", ids)
		}
	}
	c.GroupIDs = []int64{}
	c.NormalizeGroups()
	if c.GroupID != 0 || len(c.SelectedGroupIDs()) != 0 {
		t.Fatal("cleared selection restored legacy group", c)
	}
}
