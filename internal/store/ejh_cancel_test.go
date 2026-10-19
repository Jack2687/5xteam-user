package store

import (
	"chatgpt-space-merge/internal/ejh"
	"chatgpt-space-merge/internal/model"
	"testing"
	"time"
)

func TestEJHCancelReturnStatesAndAdmission(t *testing.T) {
	s := transferStore(t)
	card := ejhFixtureCard(t, s)
	email := "cancel-state@example.com"
	transferSource(t, s, email)
	order := transferOrder(email, "cancel-payment", "success")
	order.Order.CardID = card.ID
	if err := s.CreateGPTPayOrder(order.Order, order.Snapshot); err != nil {
		t.Fatal(err)
	}
	op := ejh.Operation{ID: "cancel-return-state", CardID: card.ID, OrderNo: "fixture-card", Method: "cancel", Status: "submitting"}
	if err := s.EJHBeginOperation(op); err == nil {
		t.Fatal("opened account allowed cancellation")
	}
	if err := s.EJHMarkRequested(email, "manual", time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateProAccount(email, func(p *model.MailAccountProfile) { p.RemoveStatus = "completed" }); err != nil {
		t.Fatal(err)
	}
	if err := s.EJHBeginOperation(op); err == nil {
		t.Fatal("waiting-for-return account allowed cancellation after removal")
	}
	original := ejh.Trade{OrderNo: "fixture-card", TradeNo: "cancel-original", Type: "1", Status: "2"}
	returned := ejh.Trade{OrderNo: "fixture-card", TradeNo: "cancel-return", Type: "5", Status: "2"}
	for _, tr := range []ejh.Trade{original, returned} {
		if err := s.EJHSaveTrade(card.ID, tr); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.EJHMatchTrade(EJHID("trade", "fixture-card|cancel-return|5"), order.Order.ID, EJHID("trade", "fixture-card|cancel-original|1"), "10", "USD", "verified fixture return", true); err != nil {
		t.Fatal(err)
	}
	// Stale occupancy must not override the requested return-state rule.
	if _, err := s.db.Exec(`INSERT INTO pro_card_reservations VALUES(?,?)`, email, proCardKey("4242424242424242")); err != nil {
		t.Fatal(err)
	}
	if err := s.EJHBeginOperation(op); err != nil {
		t.Fatal("returned account blocked", err)
	}
	decorated, _, err := s.GPTPayCard(card.ID)
	if err != nil || decorated.Available {
		t.Fatal("pending cancellation must block new payment", decorated, err)
	}
	other := "new-after-cancel@example.com"
	transferSource(t, s, other)
	if err := s.ReserveProCard(other, card.ID, false); err == nil {
		t.Fatal("reservation admitted during cancellation")
	}
	op.Status = "failed"
	if err := s.EJHPut("operation", op.ID, op); err != nil {
		t.Fatal(err)
	}
	next := ejh.Case{ID: "unopened-case", Email: other, CardID: card.ID, PaymentStatus: "failed"}
	if _, err := s.db.Exec(`INSERT INTO ejh_cases VALUES(?,?,?,?)`, next.ID, next.Email, next.CardID, mustJSON(next)); err != nil {
		t.Fatal(err)
	}
	op.ID = "cancel-with-unopened"
	op.Status = "submitting"
	if err := s.EJHBeginOperation(op); err != nil {
		t.Fatal("unopened case blocked", err)
	}
	op.Status = "failed"
	if err := s.EJHPut("operation", op.ID, op); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE ejh_cases SET profile=json_set(profile,'$.payment_status','success') WHERE id=?`, next.ID); err != nil {
		t.Fatal(err)
	}
	op.ID = "cancel-mixed"
	op.Status = "submitting"
	if err := s.EJHBeginOperation(op); err == nil {
		t.Fatal("one opened account did not block returned/unopened mixture")
	}
}

func TestEJHCardCreationTimePreserved(t *testing.T) {
	s := transferStore(t)
	card := ejhFixtureCard(t, s)
	var before ejh.CardRecord
	if err := s.EJHGet("card", card.ID, &before); err != nil {
		t.Fatal(err)
	}
	if before.CreatedAt.IsZero() {
		t.Fatal("missing creation time")
	}
	if _, err := s.ImportEJHCard(before.Remote, false); err != nil {
		t.Fatal(err)
	}
	var after ejh.CardRecord
	if err := s.EJHGet("card", card.ID, &after); err != nil {
		t.Fatal(err)
	}
	if !before.CreatedAt.Equal(after.CreatedAt) {
		t.Fatal("refresh changed creation order")
	}
	for _, status := range []string{"2", "8", "unexpected"} {
		after.Remote.Status = status
		if err := s.EJHPut("card", card.ID, after); err != nil {
			t.Fatal(err)
		}
		if err := s.EJHBeginOperation(ejh.Operation{ID: "reject-" + status, CardID: card.ID, Method: "cancel", Status: "submitting"}); err == nil {
			t.Fatal("invalid state allowed", status)
		}
	}
}
