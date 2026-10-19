package store

import (
	"chatgpt-space-merge/internal/ejh"
	"testing"
	"time"
)

func TestEJHTradeCardAmountChangeInvalidatesConfirmation(t *testing.T) {
	for _, field := range []string{"unchanged", "amount", "currency"} {
		t.Run(field, func(t *testing.T) {
			s := transferStore(t)
			trade := ejh.Trade{OrderNo: "card", TradeNo: "refund", Type: "5", Status: "2", CardAmount: "12.30", CardCurrency: "USD"}
			id := EJHID("trade", "card|refund|5")
			now := time.Now()
			old := ejh.TradeRecord{ID: id, CardID: "card", Trade: trade, OrderID: "account-order", Confirmed: true, ConfirmedAt: &now, Amount: "12.30", Currency: "USD", Evidence: "verified receipt"}
			if err := s.EJHPut("trade", id, old); err != nil {
				t.Fatal(err)
			}
			if field == "amount" {
				trade.CardAmount = "12.50"
			}
			if field == "currency" {
				trade.CardCurrency = "EUR"
			}
			if err := s.EJHSaveTrade("card", trade); err != nil {
				t.Fatal(err)
			}
			var got ejh.TradeRecord
			if err := s.EJHGet("trade", id, &got); err != nil {
				t.Fatal(err)
			}
			if got.OrderID != old.OrderID || got.Confirmed != (field == "unchanged") || (got.ConfirmedAt != nil) != (field == "unchanged") {
				t.Fatalf("bad confirmation: %+v", got)
			}
			audit, err := EJHRecords[map[string]any](s, "match_audit")
			if err != nil {
				t.Fatal(err)
			}
			want := 1
			if field == "unchanged" {
				want = 0
			}
			if len(audit) != want {
				t.Fatalf("audit count: %d", len(audit))
			}
		})
	}
}
