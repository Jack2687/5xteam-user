package nexovcc

import (
	"reflect"
	"testing"
	"time"
)

// Redacted issuer sequence: a previous reversed purchase, then a PHP
// authorization whose settlement and separate refund both report USD.
func settlementCurrencyRefundFinance() Finance {
	at := time.Date(2026, 10, 26, 8, 0, 0, 0, time.UTC)
	trade := func(id, auth, status, amount, merchantAmount, currency string, hours int) Trade {
		return Trade{ID: id, Serial: "currency-refund-card", Authorization: auth, Status: status, Currency: "USD", Amount: amount, MerchantAmount: merchantAmount, MerchantCurrency: currency, Merchant: "OPENAI*CHATGPTSUBSCR", At: at.Add(time.Duration(hours) * time.Hour)}
	}
	return Finance{Trades: []Trade{
		trade("refund", "refund-auth", "REFUNDED", "140.60", "140.6", "USD", 19),
		trade("prior-residual", "prior-auth", "COMPLETE", "0.35", "0.35", "USD", 17),
		trade("settlement", "payment-auth", "COMPLETE", "141.18", "141.18", "USD", 6),
		trade("payment", "payment-auth", "PENDING", "141.18", "8919.64", "PHP", 3),
		trade("prior-reversal", "prior-auth", "REVERSED", "140.82", "8919.64", "PHP", 2),
		trade("prior-payment", "prior-auth", "PENDING", "141.18", "8919.64", "PHP", 0),
	}, Funds: []Fund{
		{ID: "refund-fee", Serial: "currency-refund-card", Link: "refund", Currency: "USD", Kind: 8888, Amount: "-4.22"},
		{ID: "payment-fee", Serial: "currency-refund-card", Link: "payment", Currency: "USD", Kind: 2007, Amount: "-0.80"},
		{ID: "prior-refund-fee", Serial: "currency-refund-card", Link: "prior-reversal", Currency: "USD", Kind: 2003, Amount: "-3.00"},
		{ID: "prior-payment-fee", Serial: "currency-refund-card", Link: "prior-payment", Currency: "USD", Kind: 2007, Amount: "-1.00"},
	}}
}

func TestFinanceRefundAfterSettlementCurrencyChange(t *testing.T) {
	for _, tc := range []struct{ refund, fee, difference, cost string }{
		{"140.60", "-4.22", "0.58", "5.60"},
		{"140.82", "-4.23", "0.36", "5.39"},
	} {
		for _, reverse := range []bool{false, true} {
			f := settlementCurrencyRefundFinance()
			f.Trades[0].Amount, f.Trades[0].MerchantAmount = tc.refund, tc.refund
			f.Funds[0].Amount = tc.fee
			if reverse {
				for i, j := 0, len(f.Trades)-1; i < j; i, j = i+1, j-1 {
					f.Trades[i], f.Trades[j] = f.Trades[j], f.Trades[i]
				}
			}
			original := append([]Trade(nil), f.Trades...)
			groups := Groups(f)
			if len(groups) != 2 {
				t.Fatalf("refund remains orphaned: %+v", groups)
			}
			for _, g := range groups {
				if !g.Valid || !g.FullyReturned {
					t.Fatalf("completed return not recognized: %+v", g)
				}
				if g.Authorization == "payment-auth" && (g.Payment != "141.18" || g.Returned != tc.refund || g.Difference != tc.difference || g.Cost != tc.cost || len(g.TradeIDs) != 3) {
					t.Fatalf("refund principal or fees incorrect: %+v", g)
				}
				if g.Authorization == "prior-auth" && (g.Returned != "140.82" || g.Cost != "4.36" || len(g.TradeIDs) != 3) {
					t.Fatalf("refund attached to a previously reversed payment: %+v", g)
				}
			}
			if !reflect.DeepEqual(original, f.Trades) {
				t.Fatal("issuer evidence changed")
			}
		}
	}
}

func TestFinanceSettlementCurrencyRefundGuards(t *testing.T) {
	for _, mode := range []string{"other merchant", "unknown currency", "missing original currency", "invalid original amount", "inconsistent settlement", "different card", "refund before settlement", "excess refund", "two payments", "two refunds", "unknown status"} {
		t.Run(mode, func(t *testing.T) {
			f := settlementCurrencyRefundFinance()
			switch mode {
			case "other merchant":
				f.Trades[3].Merchant = "OTHER MERCHANT"
			case "unknown currency":
				f.Trades[2].MerchantCurrency = "EUR"
			case "missing original currency":
				f.Trades[3].MerchantCurrency = ""
			case "invalid original amount":
				f.Trades[3].MerchantAmount = "bad"
			case "inconsistent settlement":
				f.Trades[2].MerchantAmount = "141.19"
			case "different card":
				f.Trades[0].Serial = "another-card"
			case "refund before settlement":
				f.Trades[0].At = f.Trades[2].At.Add(-time.Minute)
			case "excess refund":
				f.Trades[0].Amount = "141.19"
			case "two payments":
				other := f.Trades[2]
				other.ID, other.Authorization = "other-payment", "other-auth"
				f.Trades = append(f.Trades, other)
			case "two refunds":
				other := f.Trades[0]
				other.ID, other.Authorization = "other-refund", "other-auth"
				f.Trades = append(f.Trades, other)
			case "unknown status":
				f.Trades[2].Status = "UNKNOWN"
			}
			for _, g := range Groups(f) {
				if g.Authorization == "payment-auth" && (g.FullyReturned || g.Returned != "0.00") {
					t.Fatalf("uncertain refund released payment: %+v", g)
				}
			}
		})
	}
}
