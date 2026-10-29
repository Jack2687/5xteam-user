package store

import (
	"fmt"
	"reflect"
	"testing"
	"time"

	"chatgpt-space-merge/internal/ejh"
	"chatgpt-space-merge/internal/gptpay"
)

func TestEJHDashboardReaderIndependentOfWriter(t *testing.T) {
	s := transferStore(t)
	card := ejhFixtureCard(t, s)
	reader, err := s.ProListReader(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	s.mu.Lock()
	done := make(chan error, 1)
	go func() {
		if _, _, err := reader.EJHSettings(); err != nil {
			done <- err
			return
		}
		cards, err := reader.GPTPayCardsByType("ejh")
		if err == nil && (len(cards) != 1 || cards[0].ID != card.ID) {
			err = fmt.Errorf("saved card missing")
		}
		if err == nil {
			_, err = reader.EJHCases()
		}
		if err == nil {
			_, err = EJHRecords[ejh.TradeRecord](reader, "trade")
		}
		done <- err
	}()
	select {
	case err := <-done:
		s.mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		s.mu.Unlock()
		t.Fatal("saved dashboard waited for background writer")
	}
}

func TestGPTPayCardsByTypePreservesAliasesAndOccupancy(t *testing.T) {
	s := transferStore(t)
	c := ejhFixtureCard(t, s)
	_, secret, err := s.GPTPayCard(c.ID)
	if err != nil {
		t.Fatal(err)
	}
	alias := gptpay.Card{ID: "manual-alias", Enabled: true}
	if _, err = s.SaveGPTPayCard(alias, secret); err != nil {
		t.Fatal(err)
	}
	transferSource(t, s, "occupied@example.com")
	o := transferOrder("occupied@example.com", "occupied-order", "success")
	o.Order.CardID = c.ID
	if err = s.CreateGPTPayOrder(o.Order, o.Snapshot); err != nil {
		t.Fatal(err)
	}
	other := gptpay.Card{ID: "other-card", Enabled: true}
	otherSecret := secret
	otherSecret.Number = "5555555555554444"
	if _, err = s.SaveGPTPayCard(other, otherSecret); err != nil {
		t.Fatal(err)
	}
	all, _, err := s.GPTPayCards(100, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	expected := []gptpay.Card{}
	for _, c := range all {
		if c.CardType == "ejh" {
			expected = append(expected, c)
			if c.ActiveAccounts != 1 || c.Available {
				t.Fatal("fixture did not hold the physical card")
			}
		}
	}
	if len(expected) != 2 {
		t.Fatal("fixture missing aliases")
	}
	// An unrelated card's private payload must never be read by this view.
	if _, err = s.db.Exec("UPDATE gptpay_cards SET encrypted_secret='invalid-unrelated-secret' WHERE id=?", other.ID); err != nil {
		t.Fatal(err)
	}
	got, err := s.GPTPayCardsByType("ejh")
	if err != nil || !reflect.DeepEqual(got, expected) {
		t.Fatalf("typed projection differs: %v", err)
	}
	if _, err = s.GPTPayCardsByType("bogus"); err == nil {
		t.Fatal("unknown type accepted")
	}
}
