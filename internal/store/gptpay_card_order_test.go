package store

import (
	"chatgpt-space-merge/internal/gptpay"
	"fmt"
	"testing"
	"time"
)

func TestGPTPayAvailableCardsOldestFirst(t *testing.T) {
	s := transferStore(t)
	now := time.Now().UTC()
	one := 1
	for i, id := range []string{"new", "old", "middle", "disabled"} {
		created := now.Add(-time.Duration([]int{1, 3, 2, 4}[i]) * time.Hour)
		secret := gptpay.CardSecret{Number: fmt.Sprintf("424242424242%04d", i), Year: 2099, Month: 12, CVV: "123"}
		if _, err := s.SaveGPTPayCard(gptpay.Card{ID: id, Enabled: id != "disabled", CreatedAt: created, MaxAccounts: &one}, secret); err != nil {
			t.Fatal(err)
		}
	}
	first, n, err := s.GPTPayCards(1, 0, true)
	if err != nil || n != 3 || len(first) != 1 || first[0].ID != "old" {
		t.Fatal("oldest card not first before pagination", first, n, err)
	}
	next, _, _ := s.GPTPayCards(1, 1, true)
	if next[0].ID != "middle" {
		t.Fatal("wrong page order")
	}
	managed, _, _ := s.GPTPayCards(1, 0, false)
	if managed[0].ID != "new" {
		t.Fatal("management must stay newest first")
	}
	card, secret, _ := s.GPTPayCard("old")
	card.Name = "renamed"
	if _, err = s.SaveGPTPayCard(card, secret); err != nil {
		t.Fatal(err)
	}
	first, _, _ = s.GPTPayCards(1, 0, true)
	if first[0].ID != "old" {
		t.Fatal("refresh changed opening priority")
	}
	if err = s.ReserveProCard("busy@example.com", "old", false); err != nil {
		t.Fatal(err)
	}
	first, _, _ = s.GPTPayCards(1, 0, true)
	if first[0].ID != "middle" {
		t.Fatal("occupied card selected")
	}
	if err = s.ReleaseProCard("busy@example.com"); err != nil {
		t.Fatal(err)
	}
	first, _, _ = s.GPTPayCards(1, 0, true)
	if first[0].ID != "old" {
		t.Fatal("released oldest card not preferred")
	}
}
