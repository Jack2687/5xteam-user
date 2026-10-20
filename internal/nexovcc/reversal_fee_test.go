package nexovcc

import (
	"testing"
	"time"
)

func TestZeroAuthorizationFeeBelongsToUniqueSimultaneousReturn(t *testing.T) {
	at := time.Date(2026, 10, 20, 15, 46, 47, 0, time.UTC)
	for _, mode := range []string{"matched", "different card", "different time", "different merchant", "nonzero", "ambiguous"} {
		t.Run(mode, func(t *testing.T) {
			trade := func(id, auth, status, amount string, when time.Time) Trade {
				return Trade{ID: id, Serial: "card1", Authorization: auth, Currency: "USD", MerchantCurrency: "PHP", Merchant: "OPENAI", MerchantAmount: "8919.64", Amount: amount, Status: status, At: when}
			}
			zero := trade("zero", "auxiliary", "PENDING", "0.00", at)
			zero.MerchantAmount = "1.43"
			switch mode {
			case "different card":
				zero.Serial = "card2"
			case "different time":
				zero.At = at.Add(time.Second)
			case "different merchant":
				zero.Merchant = "OTHER"
			case "nonzero":
				zero.Amount = "1.00"
			}
			f := Finance{Trades: []Trade{trade("paid", "original", "PENDING", "141.82", at.Add(-10*time.Minute)), trade("returned", "original", "REVERSED", "141.82", at), zero, trade("later", "next", "PENDING", "141.82", at.Add(time.Hour))}, Funds: []Fund{
				{ID: "payment-fee", Serial: "card1", Link: "paid", Kind: 2007, Currency: "USD", Amount: "-2.42"},
				{ID: "reversal-fee", Serial: "card1", Link: "returned", Kind: 2003, Currency: "USD", Amount: "-3.00"},
				{ID: "auxiliary-fee", Serial: zero.Serial, Link: "zero", Kind: 2007, Currency: "USD", Amount: "-1.00"},
				{ID: "later-fee", Serial: "card1", Link: "later", Kind: 2007, Currency: "USD", Amount: "-2.42"},
			}}
			if mode == "ambiguous" {
				f.Trades = append(f.Trades, trade("other-paid", "other", "PENDING", "141.82", at.Add(-time.Minute)), trade("other-return", "other", "REVERSED", "141.82", at))
			}
			f.Trades[0].Merchant = "OPENAI  *CHATGPT SUBSCR  +14158799686 CAUS"
			f.Trades[1].Merchant = "Auth PARTIAL Reversal-OPENAI *CHATGPT SUBSCR +1"
			if mode != "different merchant" {
				f.Trades[2].Merchant = "OPENAI *CHATGPT SUBSCR +14158799686 CAUS"
			}
			if mode == "ambiguous" {
				f.Trades[len(f.Trades)-2].Merchant = f.Trades[0].Merchant
			}
			var sum int64
			for _, g := range Groups(f) {
				n, _ := SignedCents(g.Fees)
				sum += n
				if g.Authorization == "original" {
					want := "5.42"
					if mode == "matched" {
						want = "6.42"
						if len(g.FeeTradeIDs) != 1 || g.FeeTradeIDs[0] != "zero" {
							t.Fatal(g)
						}
					}
					if !g.Valid || !g.FullyReturned || g.Cost != want || g.Fees != want || g.Payment != "141.82" || g.Returned != "141.82" {
						t.Fatal(g)
					}
				}
				if g.Authorization == "next" && g.Fees != "2.42" {
					t.Fatal("next account fee was reassigned", g)
				}
			}
			if sum != 884 {
				t.Fatal("fee counted twice or lost", sum)
			}
		})
	}
}
