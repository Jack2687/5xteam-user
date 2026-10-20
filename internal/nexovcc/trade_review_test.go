package nexovcc

import (
	"reflect"
	"testing"
	"time"
)

func TestFinanceResidualRoundingProductionCases(t *testing.T) {
	for _, tc := range []struct{ paid, back, residual, want string }{{"142.12", "141.83", "0.28", "0.29"}, {"142.12", "141.87", "0.24", "0.25"}} {
		f := fixtureFinance(t)
		g := Groups(f)[0]
		for i := range f.Trades {
			if f.Trades[i].Status == "PENDING" {
				f.Trades[i].Amount = tc.paid
			} else {
				f.Trades[i].Amount = tc.back
			}
		}
		f.Trades = append(f.Trades, Trade{ID: "residual", Serial: g.Serial, Authorization: g.Authorization, Currency: "USD", Amount: tc.residual, MerchantAmount: tc.residual, MerchantCurrency: "USD", Status: "COMPLETE", At: g.ReturnedAt.Add(time.Hour)})
		got := Groups(f)[0]
		if !got.Valid || !got.FullyReturned || got.Payment != tc.paid || got.Returned != tc.back || got.Difference != tc.want {
			t.Fatal(got)
		}
	}
}

func TestReviewedGroupsRefundLinkPreservesPrincipalAndFees(t *testing.T) {
	now := time.Now()
	f := Finance{Trades: []Trade{
		{ID: "paid", Serial: "card", Authorization: "old", Currency: "USD", Amount: "142.12", MerchantAmount: "142.12", MerchantCurrency: "USD", Status: "COMPLETE", At: now},
		{ID: "refund", Serial: "card", Authorization: "new", Currency: "USD", Amount: "141.57", MerchantAmount: "141.57", MerchantCurrency: "USD", Status: "REFUNDED", At: now.Add(time.Hour)},
	}, Funds: []Fund{{ID: "fee", Serial: "card", Link: "refund", Currency: "USD", Kind: 8888, Amount: "-4.40"}}}
	var source, target TradeGroup
	for _, g := range Groups(f) {
		if g.Payment == "0.00" {
			source = g
		} else {
			target = g
		}
	}
	original := append([]Trade{}, f.Trades...)
	review := TradeReview{GroupID: source.ID, TargetID: target.ID, Kind: "refund_link", Fingerprint: ReviewFingerprint(f, source, target)}
	groups, rows := ReviewedGroups(f, []TradeReview{review})
	if len(groups) != 1 || !rows[0].Applied || !groups[0].Valid || groups[0].Payment != "142.12" || groups[0].Returned != "141.57" || groups[0].Cost != "4.95" || groups[0].Fees != "4.40" {
		t.Fatal(groups, rows)
	}
	if !reflect.DeepEqual(original, f.Trades) {
		t.Fatal("raw ledger changed")
	}
	if _, e := OtherExpenseLimit(f, source); e == nil {
		t.Fatal("refund principal allowed as expense")
	}
	f.Trades[1].Amount = "141.58"
	groups, rows = ReviewedGroups(f, []TradeReview{review})
	if len(groups) != 2 || rows[0].Applied || rows[0].Problem == "" {
		t.Fatal("changed source was silently accepted", groups, rows)
	}
	wrong := target
	wrong.Serial = "different"
	if RefundLinkError(f, source, wrong) == nil {
		t.Fatal("cross card link allowed")
	}
}
