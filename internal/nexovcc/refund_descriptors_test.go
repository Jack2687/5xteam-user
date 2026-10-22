package nexovcc

import (
	"encoding/json"
	"os"
	"reflect"
	"sort"
	"testing"
)

func refundDescriptorFinance(t *testing.T) Finance {
	t.Helper()
	raw, err := os.ReadFile("testdata/refund-descriptors.json")
	if err != nil {
		t.Fatal(err)
	}
	var f Finance
	if err = json.Unmarshal(raw, &f); err != nil {
		t.Fatal(err)
	}
	return f
}

func TestFinanceRefundWithChangingIssuerDescriptors(t *testing.T) {
	raw, err := os.ReadFile("testdata/refund-descriptors.json")
	if err != nil {
		t.Fatal(err)
	}
	var expected struct {
		Groups []TradeGroup `json:"expected_groups"`
	}
	if err = json.Unmarshal(raw, &expected); err != nil {
		t.Fatal(err)
	}
	for i := range expected.Groups {
		sort.Strings(expected.Groups[i].TradeIDs)
	}
	for _, reverse := range []bool{false, true} {
		f := refundDescriptorFinance(t)
		if reverse {
			for i, j := 0, len(f.Trades)-1; i < j; i, j = i+1, j-1 {
				f.Trades[i], f.Trades[j] = f.Trades[j], f.Trades[i]
			}
		}
		original := append([]Trade{}, f.Trades...)
		groups := Groups(f)
		for i := range groups {
			sort.Strings(groups[i].TradeIDs)
		}
		if !reflect.DeepEqual(groups, expected.Groups) {
			t.Fatalf("derived data used by frontend differs: %+v", groups)
		}
		if len(groups) != 3 {
			t.Fatalf("refund should join the outstanding payment only: %+v", groups)
		}
		var found bool
		var totalCost int64
		for _, g := range groups {
			if !g.Valid {
				t.Fatalf("orphaned refund after matching: %+v", g)
			}
			if g.FullyReturned || g.Settled {
				n, _ := SignedCents(g.Difference)
				totalCost += n
			}
			if g.Authorization == "payment" {
				found = true
				if !g.FullyReturned || g.Payment != "141.82" || g.Returned != "141.23" || g.Difference != "0.59" || g.Fees != "7.16" || g.Cost != "7.75" || len(g.TradeIDs) != 3 || g.ReturnedAt.IsZero() {
					t.Fatalf("refund or fee accounting incorrect: %+v", g)
				}
			}
		}
		for _, f := range f.Funds {
			if IsFee(f.Kind) {
				n, _ := SignedCents(f.Amount)
				totalCost -= n
			}
		}
		if !found || totalCost != 1489 {
			t.Fatalf("card cost must be 14.89 USD, got %d", totalCost)
		}
		if !reflect.DeepEqual(f.Trades, original) {
			t.Fatal("matching rewrote the supplier ledger")
		}
	}
}

func TestFinanceRefundDescriptorMatchingGuards(t *testing.T) {
	for _, mode := range []string{"different merchant", "different product", "unknown suffix", "different card", "different currency", "early refund", "over original", "two unpaid candidates", "two refunds"} {
		t.Run(mode, func(t *testing.T) {
			f := refundDescriptorFinance(t)
			// Source order in the redacted real fixture is refund, tiny settlement,
			// payment settlement, payment authorization, prior reversal, ...
			switch mode {
			case "different merchant":
				f.Trades[0].Merchant = "OTHER*CHATGPT SUBSCR SAN FRANCISCOCAUS"
			case "different product":
				f.Trades[0].Merchant = "OPENAI *API SAN FRANCISCOCAUS"
			case "unknown suffix":
				f.Trades[0].Merchant = "OPENAI *CHATGPT SUBSCR OTHER PRODUCT"
			case "different card":
				f.Trades[0].Serial = "another-card"
			case "different currency":
				f.Trades[0].MerchantCurrency = "EUR"
			case "early refund":
				f.Trades[0].At = f.Trades[3].At
			case "over original":
				f.Trades[0].Amount = "141.83"
			case "two unpaid candidates":
				other := f.Trades[2]
				other.ID, other.Authorization = "second-purchase", "another-auth"
				f.Trades = append(f.Trades, other)
			case "two refunds":
				other := f.Trades[0]
				other.ID, other.Authorization = "second-refund", "another-refund-auth"
				f.Trades = append(f.Trades, other)
			}
			for _, g := range Groups(f) {
				if g.Authorization == "payment" && (g.FullyReturned || g.Returned != "0.00") {
					t.Fatalf("uncertain refund released payment: %+v", g)
				}
			}
		})
	}
}

func TestRefundMerchantDescriptorAliases(t *testing.T) {
	for _, name := range []string{"OPENAI*CHATGPTSUBSCR", "openai *chatgpt subscr +14158799686 caus", "OPENAI *CHATGPT SUBSCR OPENAI.COM   CAUS", "OPENAI *CHATGPT SUBSCR SAN FRANCISCOCAUS"} {
		if got := normalizedMerchant(name); got != "OPENAI*CHATGPTSUBSCR" {
			t.Fatalf("descriptor not normalized: %q -> %q", name, got)
		}
	}
	for _, name := range []string{"", "OPENAI*API", "OPENAI*CHATGPTSUBSCR OTHER", "FAKE OPENAI*CHATGPTSUBSCR", "OTHER MERCHANT"} {
		if normalizedMerchant(name) == "OPENAI*CHATGPTSUBSCR" {
			t.Fatalf("unrelated merchant normalized: %q", name)
		}
	}
}
