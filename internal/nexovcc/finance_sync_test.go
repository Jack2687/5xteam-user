package nexovcc

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"testing"
	"time"
)

func TestScopedFinanceFiltersAndRejectsUnfilteredResponses(t *testing.T) {
	raw, err := os.ReadFile("testdata/finance.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixtures map[string]any
	if err = json.Unmarshal(raw, &fixtures); err != nil {
		t.Fatal(err)
	}
	f := fixtureFinance(t)
	serial := f.Trades[0].Serial
	filtered := []any{}
	for _, row := range fixtures["/api/findLogWebFunds"].(map[string]any)["list"].([]any) {
		if row.(map[string]any)["serial_no"] == serial {
			filtered = append(filtered, row)
		}
	}
	fixtures["/api/findLogWebFunds"].(map[string]any)["list"] = filtered
	fixtures["/api/findLogWebFunds"].(map[string]any)["total"] = len(filtered)
	expected := []Fund{}
	for _, fund := range f.Funds {
		if fund.Serial == serial {
			expected = append(expected, fund)
		}
	}
	for _, wrong := range []bool{false, true} {
		calls := []string{}
		c := New(Credentials{Login: "test", Password: "test"}, func(_ context.Context, r Request) (Response, error) {
			if r.Path == "/api/login" {
				v := reply(map[string]any{"user_id": 1})
				v.Cookie = "test"
				return v, nil
			}
			calls = append(calls, r.Path)
			if r.Body["card_no"] != "4242424242424242" {
				t.Fatal("missing exact card filter")
			}
			if r.Path != "/api/findLogCardTrades" && r.Path != "/api/findLogWebFunds" {
				t.Fatal("unrelated endpoint", r.Path)
			}
			return reply(fixtures[r.Path]), nil
		})
		target := serial
		if wrong {
			target = "another-card"
		}
		fresh, err := c.ReadCardFinance(context.Background(), "4242424242424242", target)
		if (err != nil) != wrong {
			t.Fatal(wrong, err)
		}
		if !wrong && (len(calls) != 2 || len(fresh.Trades) != len(f.Trades) || !reflect.DeepEqual(fresh.Funds, expected)) {
			t.Fatal("scoped parser changed financial IDs or values")
		}
		if _, err = c.ReadCardFinance(context.Background(), "4242", serial); err == nil {
			t.Fatal("last four digits cannot replace the full card filter")
		}
	}
}

func TestScopedFinanceMergePreservesHistoryAndDuplicates(t *testing.T) {
	now := time.Now()
	old := Finance{At: now.Add(-time.Hour), Trades: []Trade{{ID: "old-a", Serial: "a"}, {ID: "old-b", Serial: "b"}}, Funds: []Fund{{ID: "keep", Serial: "b", At: now.Add(-7 * 24 * time.Hour)}, {ID: "replace", Serial: "a", At: now}}}
	fresh := Finance{At: now, Trades: []Trade{{ID: "new-a", Serial: "a"}}, Funds: []Fund{{ID: "identical-1", Serial: "a", Amount: "-0.50", At: now}, {ID: "identical-2", Serial: "a", Amount: "-0.50", At: now}}}
	merged := MergeCardFinance(old, fresh, "a")
	if len(merged.Trades) != 2 || merged.Trades[0].ID != "old-b" || len(merged.Funds) != 3 || len(old.Funds) != 2 {
		t.Fatal("card refresh erased unrelated history", merged)
	}
	if len(MergeCardFinance(merged, fresh, "a").Funds) != 3 {
		t.Fatal("repeated refresh duplicated fees")
	}
	account := Finance{At: now, Funds: fresh.Funds}
	merged = MergeAccountFinance(merged, account, now.Add(-72*time.Hour))
	if len(merged.Funds) != 3 || merged.Funds[0].ID != "keep" || merged.Trades[0].ID != "old-b" {
		t.Fatal("account window lost old card history", merged)
	}
	if !merged.CardSyncedAt("b").IsZero() {
		t.Fatal("another card's refresh falsely made b fresh")
	}
}

func TestClosedFinanceRequiresRefundsAndReturnedPrincipal(t *testing.T) {
	f := fixtureFinance(t)
	serial := f.Trades[0].Serial
	card := Card{Serial: serial, Status: "4"}
	// Explicit actual principal movements, independent of account-paid fees.
	f.Funds = append(f.Funds, Fund{ID: "close", Serial: serial, Kind: 3002, Currency: "USD", Amount: "119.95", At: time.Now()})
	if !ClosedFinanceComplete(card, f, Groups(f), false) {
		t.Fatal("fully refunded and closed card should stop polling")
	}
	for _, scenario := range []string{"still live", "missing principal", "pending authorization", "unknown fee", "missing time"} {
		t.Run(scenario, func(t *testing.T) {
			c, changed := card, f
			changed.Funds = append([]Fund{}, f.Funds...)
			switch scenario {
			case "still live":
				c.Status = "1"
			case "missing principal":
				changed.Funds = changed.Funds[:len(changed.Funds)-1]
			case "pending authorization":
				changed.Trades = changed.Trades[1:]
			case "unknown fee":
				changed.Funds = append(changed.Funds, Fund{Serial: serial, Kind: 99999, Currency: "USD", Amount: "-0.30", At: time.Now()})
			case "missing time":
				changed.At = time.Time{}
			}
			if ClosedFinanceComplete(c, changed, Groups(changed), false) {
				t.Fatal("unresolved card stopped polling")
			}
		})
	}
}

func TestReceiptReaderOnlyQueriesOneCardsFunds(t *testing.T) {
	calls := 0
	c := New(Credentials{Login: "test", Password: "test"}, func(_ context.Context, r Request) (Response, error) {
		if r.Path == "/api/login" {
			v := reply(map[string]any{"user_id": 1})
			v.Cookie = "test"
			return v, nil
		}
		calls++
		if r.Path != "/api/findLogWebFunds" || r.Body["card_no"] != "4242424242424242" {
			t.Fatal("unrelated receipt request", r.Path)
		}
		return reply(map[string]any{"list": []any{}, "total": 0, "page": r.Body["page"]}), nil
	})
	fresh, err := c.ReadCardFunds(context.Background(), "4242424242424242", "a")
	if err != nil || calls != 1 {
		t.Fatal(calls, err)
	}
	old := Finance{At: time.Now().Add(-time.Hour), Trades: []Trade{{ID: "payment", Serial: "a"}}, Funds: []Fund{{ID: "other", Serial: "b"}}}
	merged := MergeCardFunds(old, fresh, "a")
	if len(merged.Trades) != 1 || merged.Trades[0].ID != "payment" || len(merged.Funds) != 1 || !merged.CardSyncedAt("a").Equal(old.At) || !merged.FundsSyncedAt("a").Equal(fresh.At) {
		t.Fatal("receipt refresh changed payment history/freshness", merged)
	}
}

func TestAccountSummaryPreservesRecentAndClosedCardEvidence(t *testing.T) {
	now := time.Now()
	old := Finance{Funds: []Fund{{ID: "recent", Serial: "deleted", At: now}, {ID: "old", At: now.Add(-7 * 24 * time.Hour)}}, Trades: []Trade{{ID: "payment", Serial: "deleted"}}, Recharges: []Recharge{{ID: "old-recharge"}}}
	fresh := Finance{At: now, Recharges: []Recharge{{ID: "new-recharge"}}}
	out := MergeAccountSummary(old, fresh, now.Add(-72*time.Hour))
	if !reflect.DeepEqual(out.Funds, old.Funds) || !reflect.DeepEqual(out.Trades, old.Trades) || len(out.Recharges) != 2 || !out.AccountAt.Equal(now) {
		t.Fatal("summary erased ledger evidence", out)
	}
}
