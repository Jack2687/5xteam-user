package store

import (
	"chatgpt-space-merge/internal/ejh"
	"encoding/json"
	"strings"
	"testing"
)

func TestActivationChargeAttribution(t *testing.T) {
	s := transferStore(t)
	card := ejhFixtureCard(t, s)
	cfg, _, err := s.EJHSettings()
	if err != nil {
		t.Fatal(err)
	}
	cfg.SingleActiveAccount = false
	if _, err = s.SaveEJHSettings(cfg, ""); err != nil {
		t.Fatal(err)
	}
	for _, email := range []string{"a@example.com", "b@example.com"} {
		transferSource(t, s, email)
		o := transferOrder(email, email, "success")
		o.Order.CardID = card.ID
		o.Order.Remote.ID = "remote-" + email
		o.Order.Remote.ChargedCredits = 120
		if err := s.CreateGPTPayOrder(o.Order, o.Snapshot); err != nil {
			t.Fatal(err)
		}
	}
	auth := ejh.Trade{OrderNo: "fixture-card", TradeNo: "auth", Number: "4242424242424242", Type: "1", Status: "1", CardAmount: "20.00", CardCurrency: "USD", TradeAmount: "18.00", TradeCurrency: "EUR", BillAmount: "20.10", BillCurrency: "USD", AuthCode: "known-code"}
	clear := auth
	clear.Type = "4"
	clear.TradeNo = "clear"
	clear.Status = "2"
	for _, v := range []ejh.Trade{auth, clear} {
		if err := s.EJHSaveTrade(card.ID, v); err != nil {
			t.Fatal(err)
		}
	}
	aid := EJHID("trade", "fixture-card|auth|1")
	cid := EJHID("trade", "fixture-card|clear|4")
	if err := s.EJHLinkCharge("a@example.com", aid, "verified receipt and provider reference"); err != nil {
		t.Fatal(err)
	}
	if err := s.EJHLinkCharge("b@example.com", aid, "same transaction"); err == nil {
		t.Fatal("duplicate attribution")
	}
	if err := s.EJHLinkCharge("b@example.com", cid, "same purchase clearing"); err == nil {
		t.Fatal("authorization and clearing double counted")
	}
	cases, err := s.EJHCasesForEmails([]string{"a@example.com"})
	if err != nil || len(cases) != 1 {
		t.Fatal(cases, err)
	}
	c := cases[0]
	if c.Charge == nil || c.Charge.CardAmount != "20.00" || c.Charge.BillAmount != "20.10" || c.Charge.Status != "1" || c.State != "opened" || c.Credits == nil || c.Credits.Charged != 120 {
		t.Fatalf("missing or conflated amounts: %+v", c)
	}
	raw, _ := json.Marshal(c)
	if strings.Contains(string(raw), auth.Number) {
		t.Fatal("PAN exposed")
	}
	if err = s.EJHLinkCharge(c.ID, cid, "verified final clearing"); err != nil {
		t.Fatal(err)
	}
	clear.CardAmount = "21.00"
	if err = s.EJHSaveTrade(card.ID, clear); err != nil {
		t.Fatal(err)
	}
	cases, err = s.EJHCasesForEmails([]string{c.Email})
	if err != nil || cases[0].Charge.CardAmount != "21.00" {
		t.Fatal("stale charge amount", err)
	}
	ret := auth
	ret.Type = "5"
	ret.TradeNo = "refund"
	ret.Status = "2"
	if err = s.EJHSaveTrade(card.ID, ret); err != nil {
		t.Fatal(err)
	}
	rid := EJHID("trade", "fixture-card|refund|5")
	if err = s.EJHMatchTrade(rid, c.ID, cid, "8.00", "USD", "verified refund receipt", true); err != nil {
		t.Fatal(err)
	}
	if err = s.EJHLinkCharge(c.ID, "", "correction after refund"); err == nil {
		t.Fatal("removed charge behind return")
	}
	if err = s.EJHUnmatch(rid, "refund correction evidence"); err != nil {
		t.Fatal(err)
	}
	cases, err = s.EJHCasesForEmails([]string{c.Email})
	if err != nil || cases[0].Charge == nil || cases[0].State != "opened" {
		t.Fatal("return correction erased independent charge", err)
	}
	if err = s.EJHLinkCharge(c.ID, "", "remove wrong attribution"); err != nil {
		t.Fatal(err)
	}
	cases, err = s.EJHCasesForEmails([]string{c.Email})
	if err != nil || cases[0].Charge != nil {
		t.Fatal("charge did not clear", err)
	}
	audit, err := EJHRecords[map[string]any](s, "match_audit")
	if err != nil || len(audit) < 5 {
		t.Fatal("missing audit", err)
	}
}
func TestActivationChargeRejectsWrongCardAndFailedOrder(t *testing.T) {
	s := transferStore(t)
	card := ejhFixtureCard(t, s)
	email := "failed@example.com"
	transferSource(t, s, email)
	o := transferOrder(email, "failed", "failed")
	o.Order.CardID = card.ID
	if err := s.CreateGPTPayOrder(o.Order, o.Snapshot); err != nil {
		t.Fatal(err)
	}
	v := ejh.Trade{OrderNo: "fixture-card", TradeNo: "x", Type: "1", Status: "2"}
	if err := s.EJHSaveTrade(card.ID, v); err != nil {
		t.Fatal(err)
	}
	id := EJHID("trade", "fixture-card|x|1")
	if err := s.EJHLinkCharge(o.Order.ID, id, "failed activation"); err == nil {
		t.Fatal("failed order accepted")
	}
	o = transferOrder(email, "success", "success")
	o.Order.CardID = "other-card"
	o.Snapshot.Input.CardSecret.Number = "5555555555554444"
	if err := s.CreateGPTPayOrder(o.Order, o.Snapshot); err != nil {
		t.Fatal(err)
	}
	if err := s.EJHLinkCharge(o.Order.ID, id, "wrong card"); err == nil {
		t.Fatal("wrong card accepted")
	}
}
