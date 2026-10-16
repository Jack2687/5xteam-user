package store

import (
	"reflect"
	"testing"
)

func TestEJHRechargeLedgerPersistenceAndValidation(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { s.Close() }()
	initial, err := s.EJHRechargeLedger()
	if err != nil || initial.Amount != "" || initial.UpdatedAt != nil {
		t.Fatal(initial, err)
	}
	settings, secret, err := s.EJHSettings()
	if err != nil {
		t.Fatal(err)
	}
	saved, err := s.SaveEJHRechargeLedger(" 1234.5 ")
	if err != nil || saved.Amount != "1234.50" || saved.UpdatedAt == nil {
		t.Fatal(saved, err)
	}
	for _, bad := range []string{"-1", "1.001", "1e3", "NaN", "1,000", "1000000000000", "1 USD"} {
		if _, err = s.SaveEJHRechargeLedger(bad); err == nil {
			t.Fatal("invalid amount accepted", bad)
		}
		got, err := s.EJHRechargeLedger()
		if err != nil || saved.Amount != got.Amount || got.UpdatedAt == nil || !saved.UpdatedAt.Equal(*got.UpdatedAt) {
			t.Fatal("invalid save changed amount", got, err)
		}
	}
	after, afterSecret, err := s.EJHSettings()
	if err != nil || !reflect.DeepEqual(settings, after) || secret != afterSecret {
		t.Fatal("ledger changed supplier settings", after, err)
	}
	s.Close()
	s, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.EJHRechargeLedger()
	if err != nil || saved.Amount != got.Amount || got.UpdatedAt == nil || !saved.UpdatedAt.Equal(*got.UpdatedAt) {
		t.Fatal("ledger lost after restart", got, err)
	}
	if got, err = s.SaveEJHRechargeLedger("0"); err != nil || got.Amount != "0.00" {
		t.Fatal(got, err)
	}
	if got, err = s.SaveEJHRechargeLedger(""); err != nil || got.Amount != "" {
		t.Fatal(got, err)
	}
}
