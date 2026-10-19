package nexovcc

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"
)

func fixtureFinance(t *testing.T) Finance {
	t.Helper()
	raw, err := os.ReadFile("testdata/finance.json")
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err = json.Unmarshal(raw, &payload); err != nil {
		t.Fatal(err)
	}
	c := New(Credentials{Login: "fixture", Password: "fixture"}, func(_ context.Context, r Request) (Response, error) {
		if r.Path == "/api/login" {
			v := reply(map[string]any{"user_id": 1})
			v.Cookie = "fixture"
			return v, nil
		}
		if r.Body["user_id"] != "1" || r.Body["page"] != 1 {
			t.Fatal("invalid authenticated page request")
		}
		d, ok := payload[r.Path]
		if !ok {
			t.Fatal(r.Path)
		}
		return reply(d), nil
	})
	f, err := c.ReadFinance(context.Background(), func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func TestFinanceHARReversalAndFees(t *testing.T) {
	f := fixtureFinance(t)
	groups := Groups(f)
	if len(groups) != 1 {
		t.Fatal(groups)
	}
	g := groups[0]
	if !g.Valid || !g.FullyReturned || g.Payment != "92.33" || g.Returned != "92.28" || g.Difference != "0.05" || g.Fees != "0.80" || g.Cost != "0.85" {
		t.Fatal(g)
	}
	var fees int64
	for _, r := range f.Funds {
		if IsFee(r.Kind) {
			n, _ := SignedCents(r.Amount)
			fees -= n
		}
	}
	if fees != 130 {
		t.Fatal("actual account fees", fees)
	}
	// Fee rebates reduce the cost. Day un_auth_day and cancel_fee are the same
	// expense and neither may be added again.
	f.Funds = append(f.Funds, Fund{ID: "rebate", Link: g.TradeIDs[0], Serial: g.Serial, Kind: 5004, Amount: "0.20", Currency: "USD"})
	if got := Groups(f)[0].Cost; got != "0.65" {
		t.Fatal(got)
	}
}

func TestFinancePartialAmbiguousAndSettlement(t *testing.T) {
	original := fixtureFinance(t)
	for _, tc := range []struct {
		name            string
		change          func(*Finance)
		valid, returned bool
		cost            string
	}{
		{"partial", func(f *Finance) { f.Trades[0].Amount = "45.00"; f.Trades[0].MerchantAmount = "2800.00" }, true, false, "0.80"},
		{"wrong currency", func(f *Finance) { f.Trades[0].MerchantCurrency = "EUR" }, true, false, "0.80"},
		{"unknown status", func(f *Finance) { f.Trades[0].Status = "NEW_STATUS" }, false, false, "0.80"},
		{"duplicate authorization", func(f *Finance) { r := f.Trades[1]; r.ID = "second"; f.Trades = append(f.Trades, r) }, false, false, "0.80"},
		{"return before payment", func(f *Finance) { f.Trades[0].At = f.Trades[1].At.Add(-time.Hour) }, false, false, "0.80"},
		{"settlement replaces authorization", func(f *Finance) {
			f.Trades = f.Trades[1:]
			r := f.Trades[0]
			r.ID = "settled"
			r.Status = "COMPLETE"
			r.Amount = "92.35"
			r.At = r.At.Add(time.Minute)
			f.Trades = append(f.Trades, r)
		}, true, false, "92.65"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := original
			f.Trades = append([]Trade{}, original.Trades...)
			tc.change(&f)
			g := Groups(f)[0]
			if g.Valid != tc.valid || g.FullyReturned != tc.returned || g.Cost != tc.cost {
				t.Fatal(g)
			}
		})
	}
}

func TestFinancePaginationDoesNotSilentlyTruncate(t *testing.T) {
	for _, mode := range []string{"capped", "repeated", "missing", "changed", "short", "empty"} {
		t.Run(mode, func(t *testing.T) {
			c := New(Credentials{Login: "fixture", Password: "fixture"}, func(_ context.Context, r Request) (Response, error) {
				if r.Path == "/api/login" {
					v := reply(map[string]any{"user_id": 1})
					v.Cookie = "fixture"
					return v, nil
				}
				page := r.Body["page"].(int)
				total := 3
				rows := []any{map[string]any{"id": fmt.Sprint(page)}}
				if mode == "repeated" {
					rows = []any{map[string]any{"id": "same"}}
				}
				if mode == "changed" && page > 1 {
					total = 4
				}
				if mode == "short" && page > 1 {
					rows = nil
				}
				if mode == "empty" {
					total = 0
					rows = nil
				}
				data := map[string]any{"list": rows, "total": total, "page": page, "pageSize": 1}
				if mode == "missing" {
					delete(data, "total")
				}
				return reply(data), nil
			})
			rows, err := c.FinancePages(context.Background(), "/api/findLogCardTrades", nil)
			if mode == "capped" {
				if err != nil || len(rows) != 3 {
					t.Fatal(rows, err)
				}
			} else if mode == "empty" {
				if err != nil || len(rows) != 0 {
					t.Fatal(rows, err)
				}
			} else if err == nil {
				t.Fatal("accepted incomplete snapshot", rows)
			}
		})
	}
}

func TestFinanceResidualSettlementAfterReversal(t *testing.T) {
	for _, order := range [][]int{{0, 1, 2}, {2, 0, 1}, {1, 2, 0}, {2, 1, 0}, {0, 2, 1}, {1, 0, 2}} {
		f := fixtureFinance(t)
		g := Groups(f)[0]
		settlement := Trade{ID: "residual", Serial: g.Serial, Authorization: g.Authorization, Currency: "USD", Amount: "0.05", MerchantAmount: "0.05", MerchantCurrency: "USD", Status: "COMPLETE", At: g.ReturnedAt.Add(time.Hour)}
		rows := append(f.Trades, settlement)
		f.Trades = []Trade{rows[order[0]], rows[order[1]], rows[order[2]]}
		got := Groups(f)[0]
		if !got.Valid || !got.FullyReturned || !got.Settled || got.ID != g.ID || got.Payment != "92.33" || got.Returned != "92.28" || got.Difference != "0.05" || got.Cost != "0.85" || got.MerchantCurrency != "PHP" {
			t.Fatalf("row order %v changed accounting: %+v", order, got)
		}
	}
}

func TestFinanceResidualSettlementRequiresEvidence(t *testing.T) {
	for _, mode := range []string{"amount mismatch", "early settlement", "partial merchant return"} {
		t.Run(mode, func(t *testing.T) {
			f := fixtureFinance(t)
			g := Groups(f)[0]
			r := Trade{ID: "unverified", Serial: g.Serial, Authorization: g.Authorization, Currency: "USD", Amount: "0.05", MerchantAmount: "0.05", MerchantCurrency: "USD", Status: "COMPLETE", At: g.ReturnedAt.Add(time.Hour)}
			switch mode {
			case "amount mismatch":
				r.Amount = "0.04"
			case "early settlement":
				r.At = g.At
			case "partial merchant return":
				f.Trades[0].MerchantAmount = "100.00"
			}
			f.Trades = append(f.Trades, r)
			got := Groups(f)[0]
			if got.Valid || got.FullyReturned || got.Cost != "0.80" {
				t.Fatalf("invalid settlement fabricated a return or negative cost: %+v", got)
			}
		})
	}
}

func TestFinanceRegularSettlementThenRefund(t *testing.T) {
	f := fixtureFinance(t)
	g := Groups(f)[0]
	f.Trades[0].Amount, f.Trades[0].MerchantAmount = "40.00", "2500.00"
	f.Trades = append(f.Trades, Trade{ID: "normal-settlement", Serial: g.Serial, Authorization: g.Authorization, Currency: "USD", Amount: "92.35", MerchantAmount: "5794.64", MerchantCurrency: "PHP", Status: "COMPLETE", At: g.At.Add(time.Minute)})
	got := Groups(f)[0]
	if !got.Valid || got.FullyReturned || !got.Settled || got.Payment != "92.35" || got.Difference != "52.35" || got.Cost != "53.15" {
		t.Fatalf("ordinary settlement/refund changed: %+v", got)
	}
}
