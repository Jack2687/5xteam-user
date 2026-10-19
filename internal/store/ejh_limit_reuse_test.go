package store

import (
	"chatgpt-space-merge/internal/ejh"
	"chatgpt-space-merge/internal/model"
	"testing"
	"time"
)

func TestEJHIncreaseUsedCardLimitAndReuseAfterReturn(t *testing.T) {
	s := transferStore(t)
	card := ejhFixtureCard(t, s)
	limit := 1
	card.MaxAccounts = &limit
	_, secret, err := s.GPTPayCard(card.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SaveGPTPayCard(card, secret); err != nil {
		t.Fatal(err)
	}
	email := "used-limit@example.com"
	transferSource(t, s, email)
	order := transferOrder(email, "limit-order", "success")
	order.Order.CardID = card.ID
	if err = s.CreateGPTPayOrder(order.Order, order.Snapshot); err != nil {
		t.Fatal(err)
	}
	if err = s.EJHMarkRequested(email, "manual", time.Now()); err != nil {
		t.Fatal(err)
	}
	limit = 2
	if _, err = s.SaveGPTPayCard(card, secret); err != nil {
		t.Fatal(err)
	}
	if err = s.ReserveProCard("next@example.com", card.ID, false); err == nil {
		t.Fatal("raising limit released active account before removal")
	}
	if _, err = s.UpdateProAccount(email, func(p *model.MailAccountProfile) { p.RemoveStatus = "completed" }); err != nil {
		t.Fatal(err)
	}
	cases, err := s.EJHCases()
	if err != nil || len(cases) != 1 || cases[0].State != "waiting" {
		t.Fatal(cases, err)
	}
	got, _, err := s.GPTPayCard(card.ID)
	if err != nil || got.AccountLimit != 2 || got.OpenedAccounts != 1 || got.RemainingAccounts != 1 || got.Available {
		t.Fatal(got, err)
	}
	if err = s.EJHBeginOperation(ejh.Operation{ID: "cannot-cancel-waiting", CardID: card.ID, OrderNo: "fixture-card", Method: "cancel", Status: "submitting"}); err == nil {
		t.Fatal("waiting return allowed cancellation")
	}
	if err = s.ReserveProCard("next@example.com", card.ID, false); err == nil {
		t.Fatal("removed but unreturned account freed card")
	}
	ejhReceiveForOrder(t, s, card.ID, order.Order.ID)
	if err = s.ReserveProCard("next@example.com", card.ID, false); err != nil {
		t.Fatal("returned account blocked reuse", err)
	}
	if err = s.ReleaseProCard("next@example.com"); err != nil {
		t.Fatal(err)
	}
	limit = 1
	if _, err = s.SaveGPTPayCard(card, secret); err != nil {
		t.Fatal(err)
	}
	got, _, err = s.GPTPayCard(card.ID)
	if err != nil || got.OpenedAccounts != 1 || got.RemainingAccounts != 0 || got.Available {
		t.Fatal("lowering erased lifetime count", got, err)
	}
	limit = 0
	if _, err = s.SaveGPTPayCard(card, secret); err != nil {
		t.Fatal(err)
	}
	got, _, err = s.GPTPayCard(card.ID)
	if err != nil || got.MaxAccounts != nil || got.OpenedAccounts != 1 || !got.Available {
		t.Fatal("reset did not restore default", got, err)
	}
}
