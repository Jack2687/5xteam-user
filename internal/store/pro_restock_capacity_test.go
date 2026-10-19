package store

import (
	"chatgpt-space-merge/internal/restock"
	"testing"
)

func TestProRestockCapacityPercentPersistenceAndLegacyDefault(t *testing.T) {
	s := transferStore(t)
	c, err := s.ProRestockConfig()
	if err != nil || c.CapacityPercent != 60 {
		t.Fatal("new installation default", c, err)
	}
	c.CapacityPercent = 80
	c, err = s.SaveProRestockConfig(c)
	if err != nil {
		t.Fatal(err)
	}
	read, err := s.ProRestockConfig()
	if err != nil || read.CapacityPercent != 80 || read.Version != c.Version {
		t.Fatal("saved percentage was lost", read, err)
	}
	if _, err = s.db.Exec(`UPDATE pro_restock_config SET payload=json_remove(payload,'$.capacity_percent') WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	read, err = s.ProRestockConfig()
	if err != nil || read.CapacityPercent != restock.DefaultCapacityPercent || read.Version != c.Version {
		t.Fatal("legacy config should inherit 60 without resetting other settings", read, err)
	}
	read.CapacityPercent = 0
	if _, err = s.SaveProRestockConfig(read); err == nil {
		t.Fatal("invalid percentage accepted")
	}
}
