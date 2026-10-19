package store

import (
	"chatgpt-space-merge/internal/ejh"
	"testing"
)

func TestEJHImportCannotRebindOrderToDifferentPhysicalCard(t *testing.T) {
	s := transferStore(t)
	card := ejhFixtureCard(t, s)
	var record ejh.CardRecord
	if err := s.EJHGet("card", card.ID, &record); err != nil {
		t.Fatal(err)
	}
	original := record.Remote
	record.Remote.Number = "5555555555554444"
	if _, err := s.ImportEJHCard(record.Remote, false); err == nil {
		t.Error("supplier changed physical card identity without rejection")
	}
	_, secret, err := s.GPTPayCard(card.ID)
	if err != nil || secret.Number != original.Number {
		t.Fatal("saved card credentials were replaced")
	}
	var count int
	if err := s.db.QueryRow(`SELECT count(*) FROM ejh_card_links WHERE card_id=?`, card.ID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("order now maps to %d physical cards", count)
	}
	if err := s.ReserveProCard("blocked@example.com", card.ID, false); err == nil {
		t.Fatal("unverified issuer card still admitted")
	}
	if _, err := s.ImportEJHCard(original, false); err != nil {
		t.Fatal(err)
	}
	if err := s.ReserveProCard("recovered@example.com", card.ID, false); err != nil {
		t.Fatal("valid refresh did not recover", err)
	}
}
