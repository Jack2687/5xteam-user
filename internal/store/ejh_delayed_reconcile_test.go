package store

import (
	"chatgpt-space-merge/internal/ejh"
	"testing"
	"time"
)

func TestEJHDelayedReversalBackfillsAtStartup(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { s.Close() }()
	card := ejhFixtureCard(t, s)
	email := "delayed@example.com"
	transferSource(t, s, email)
	o := transferOrder(email, "delayed-order", "success")
	o.Order.CardID = card.ID
	at, _ := time.Parse(time.RFC3339, "2026-10-27T23:26:30+08:00")
	o.Order.CreatedAt, o.Order.UpdatedAt = at.Add(-time.Minute), at
	if err = s.CreateGPTPayOrder(o.Order, o.Snapshot); err != nil {
		t.Fatal(err)
	}
	if err = s.EJHMarkRequested(email, "remote", at.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	pay := ejh.Trade{OrderNo: "fixture-card", TradeNo: "delayed-pay", Type: "1", Status: "1", AuthCode: "B", CardCurrency: "PHP", CardAmount: "8919.64", TradeTime: "2026-10-27 15:26:24", CreatedAt: "2026-10-27 23:39:56", Merchant: "OPENAI"}
	ret := pay
	ret.TradeNo, ret.Type, ret.CardAmount = "delayed-return", "2", "8898.18"
	ret.TradeTime, ret.CreatedAt = "2026-10-27 17:15:21", "2026-10-28 02:12:49"
	if err = s.EJHSaveTrades(card.ID, []ejh.Trade{ret, pay}); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	cases, err := s.EJHCases()
	if err != nil || len(cases) != 1 || cases[0].State != "returned" || cases[0].Amounts["PHP"] != "8898.18" {
		t.Fatalf("startup did not reconcile return: %+v %v", cases, err)
	}
	c, _, err := s.GPTPayCard(card.ID)
	if err != nil || c.ActiveAccounts != 0 || c.OpenedAccounts != 1 {
		t.Fatalf("wrong occupancy: %+v %v", c, err)
	}
	if err = s.EJHReconcile(); err != nil {
		t.Fatal(err)
	}
	cases, _ = s.EJHCases()
	if cases[0].Amounts["PHP"] != "8898.18" {
		t.Fatal("reconciliation duplicated return")
	}
	rows, err := EJHRecords[ejh.TradeRecord](s, "trade")
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if r.Trade.TradeNo == ret.TradeNo && r.Trade.TradeTime != ret.TradeTime {
			t.Fatal("raw timestamp rewritten")
		}
	}
}
