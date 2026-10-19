package store

import (
	"reflect"
	"testing"

	"chatgpt-space-merge/internal/restock"
)

func TestProRestockMoneyWindowDefaultAndPersistence(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { s.Close() }()
	if _, err = s.db.Exec(`INSERT INTO pro_restock_config VALUES(1,'{"version":1,"mode":"observe"}')`); err != nil {
		t.Fatal(err)
	}
	c, err := s.ProRestockConfig()
	if err != nil || c.MoneyWindowMinutes != 3 {
		t.Fatal("legacy config must default to three minutes", c, err)
	}
	for _, minutes := range []int{0, -1, 61} {
		bad := c
		bad.MoneyWindowMinutes = minutes
		if _, err := s.SaveProRestockConfig(bad); err == nil {
			t.Fatal("invalid window saved", minutes)
		}
	}
	for _, minutes := range []int{1, 5, restock.MaxMoneyWindowMinutes} {
		c.MoneyWindowMinutes = minutes
		c, err = s.SaveProRestockConfig(c)
		if err != nil {
			t.Fatal(err)
		}
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.ProRestockConfig()
	if err != nil || !reflect.DeepEqual(got, c) || got.MoneyWindowMinutes != 60 {
		t.Fatal("window did not survive restart", got, err)
	}
}
