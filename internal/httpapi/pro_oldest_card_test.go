package httpapi

import (
	"chatgpt-space-merge/internal/gptpay"
	"chatgpt-space-merge/internal/model"
	"context"
	"testing"
	"time"
)

func TestEJHAutomaticSelectionPrefersOldestUsableCard(t *testing.T) {
	s, opens := ejhTestServer(t, 200)
	for _, email := range []string{"one@example.com", "two@example.com", "manual@example.com"} {
		if _, err := s.store.SaveMailAccount(model.MailAccountProfile{Email: email}, model.MailAccountCredentials{Email: email}); err != nil {
			t.Fatal(err)
		}
		if _, err := s.store.UpdateMailAccountManagementScope(email, "pro"); err != nil {
			t.Fatal(err)
		}
	}
	one := 1
	for i, id := range []string{"old", "new"} {
		secret := gptpay.CardSecret{Number: []string{"4242424242424242", "4000000000000002"}[i], Year: 2099, Month: 12, CVV: "123"}
		if _, err := s.store.SaveGPTPayCard(gptpay.Card{ID: id, Enabled: true, CreatedAt: time.Now().Add(time.Duration(i-3) * time.Hour), MaxAccounts: &one}, secret); err != nil {
			t.Fatal(err)
		}
	}
	for i, want := range []string{"old", "new"} {
		card, _, err := s.reserveProStartCard(context.Background(), []string{"one@example.com", "two@example.com"}[i], "", true, false)
		if err != nil || card.ID != want {
			t.Fatal("wrong automatic priority", card.ID, want, err)
		}
	}
	if opens.Load() != 0 {
		t.Fatal("issued new card despite available old cards")
	}
	if err := s.store.ReleaseProCard("one@example.com"); err != nil {
		t.Fatal(err)
	}
	card, err := s.ensureEJHCard(context.Background())
	if err != nil || card.ID != "old" {
		t.Fatal("released old card not reused", err)
	}
	if err := s.store.ReleaseProCard("two@example.com"); err != nil {
		t.Fatal(err)
	}
	chosen, _, err := s.reserveProStartCard(context.Background(), "manual@example.com", "new", true, false)
	if err != nil || chosen.ID != "new" {
		t.Fatal("explicit manual card choice was replaced", err)
	}
}
