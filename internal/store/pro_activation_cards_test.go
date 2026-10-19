package store

import (
	"fmt"
	"testing"
	"time"

	"chatgpt-space-merge/internal/gptpay"
	"chatgpt-space-merge/internal/nexovcc"
)

func TestProActivationCardsSuccessfulSnapshot(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	now := time.Now()
	snapshot := gptpay.Snapshot{}
	orders := []gptpay.Order{
		{ID: "old", Email: "pro@example.com", Status: "success", CardName: "旧卡", CardLast4: "1111", CreatedAt: now.Add(-3 * time.Hour)},
		{ID: "paid", Email: "pro@example.com", Status: "success", CardName: "开通时卡名", CardLast4: "4242", CreatedAt: now.Add(-2 * time.Hour)},
		{ID: "failed", Email: "pro@example.com", Status: "failed", CardName: "失败卡", CardLast4: "3333", CreatedAt: now.Add(-time.Hour)},
		{ID: "pending", Email: "pro@example.com", Status: "processing", CardName: "待处理卡", CardLast4: "5555", CreatedAt: now},
		{ID: "unrelated", Email: "other@example.com", Status: "success", CardName: "其他账号卡", CardLast4: "6666", CreatedAt: now},
	}
	for _, order := range orders {
		if err = s.CreateGPTPayOrder(order, snapshot); err != nil {
			t.Fatal(err)
		}
	}
	emails := []string{"PRO@example.com", "unknown@example.com"}
	for i := 0; i < 405; i++ {
		emails = append(emails, fmt.Sprintf("empty-%d@example.com", i))
	}
	cards, err := s.ProActivationCards(emails)
	if err != nil {
		t.Fatal(err)
	}
	if len(cards) != 1 || cards["pro@example.com"].CardName != "开通时卡名" || cards["pro@example.com"].CardLast4 != "4242" {
		t.Fatalf("wrong card snapshot: %+v", cards)
	}
	// No card rows exist: deleting/editing the card cannot erase the order snapshot.
	orders[3].Status = "success"
	if err = s.UpdateGPTPayOrder(orders[3]); err != nil {
		t.Fatal(err)
	}
	cards, err = s.ProActivationCards([]string{"pro@example.com"})
	if err != nil || cards["pro@example.com"].CardLast4 != "5555" {
		t.Fatal("new successful order not reflected")
	}
	cards, err = s.ProActivationCards(nil)
	if err != nil || len(cards) != 0 {
		t.Fatal("empty page lookup failed")
	}
}

func TestProActivationCardEJHCanonicalNavigation(t *testing.T) {
	s := transferStore(t)
	canonical := ejhFixtureCard(t, s)
	cfg, _, err := s.EJHSettings()
	if err != nil {
		t.Fatal(err)
	}
	cfg.SingleActiveAccount = false
	if _, err = s.SaveEJHSettings(cfg, ""); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	for i, item := range []struct{ id, email, card, number string }{
		{"direct", "direct@example.com", canonical.ID, "4242424242424242"},
		{"alias", "alias@example.com", "historical-alias", "4242424242424242"},
		{"ordinary", "ordinary@example.com", "ordinary-card", "5555555555554242"},
		{"old-ejh", "latest@example.com", canonical.ID, "4242424242424242"},
		{"new-ordinary", "latest@example.com", "ordinary-card", "5555555555554242"},
	} {
		snapshot := gptpay.Snapshot{}
		snapshot.Input.Number = item.number
		order := gptpay.Order{ID: item.id, Email: item.email, CardID: item.card, CardName: "same card label", CardLast4: "4242", Status: "success", CreatedAt: now.Add(time.Duration(i) * time.Minute)}
		if err := s.CreateGPTPayOrder(order, snapshot); err != nil {
			t.Fatal(err)
		}
	}
	cards, err := s.ProActivationCards([]string{"direct@example.com", "alias@example.com", "ordinary@example.com", "latest@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	for _, email := range []string{"direct@example.com", "alias@example.com"} {
		if cards[email].EJHCardID != canonical.ID {
			t.Fatalf("lost canonical identity: %s %+v", email, cards[email])
		}
	}
	for _, email := range []string{"ordinary@example.com", "latest@example.com"} {
		if cards[email].EJHCardID != "" {
			t.Fatalf("ordinary card falsely navigable: %s %+v", email, cards[email])
		}
	}
	cases, err := s.EJHCasesForEmails([]string{"alias@example.com"})
	if err != nil || len(cases) != 1 || cases[0].CardID != "historical-alias" || cases[0].EJHCardID != canonical.ID {
		t.Fatalf("case association: %+v %v", cases, err)
	}
}

func TestProActivationCardNexovccCanonicalNavigation(t *testing.T) {
	s := transferStore(t)
	canonical := nexStoreCard(t, s)
	cfg, _, err := s.NexovccSettings()
	if err != nil {
		t.Fatal(err)
	}
	cfg.SingleActiveAccount = false
	cfg.DefaultMaxAccounts = 10
	if _, err = s.SaveNexovccSettings(cfg, nexovcc.Credentials{}, false); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	for i, item := range []struct{ id, email, card, number string }{
		{"direct", "direct@example.com", canonical.ID, "4242424242424242"},
		{"alias", "alias@example.com", "historical-alias", "4242424242424242"},
		{"ordinary", "ordinary@example.com", "ordinary-card", "5555555555554242"},
		{"old-ejh", "latest@example.com", canonical.ID, "4242424242424242"},
		{"new-ordinary", "latest@example.com", "ordinary-card", "5555555555554242"},
	} {
		snapshot := gptpay.Snapshot{}
		snapshot.Input.Number = item.number
		order := gptpay.Order{ID: item.id, Email: item.email, CardID: item.card, CardName: "same card label", CardLast4: "4242", Status: "success", CreatedAt: now.Add(time.Duration(i) * time.Minute)}
		if err := s.CreateGPTPayOrder(order, snapshot); err != nil {
			t.Fatal(err)
		}
	}
	cards, err := s.ProActivationCards([]string{"direct@example.com", "alias@example.com", "ordinary@example.com", "latest@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	for _, email := range []string{"direct@example.com", "alias@example.com"} {
		if cards[email].NexovccCardID != canonical.ID {
			t.Fatalf("lost canonical identity: %s %+v", email, cards[email])
		}
	}
	for _, email := range []string{"ordinary@example.com", "latest@example.com"} {
		if cards[email].NexovccCardID != "" {
			t.Fatalf("ordinary card falsely navigable: %s %+v", email, cards[email])
		}
	}
}
