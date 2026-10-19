package httpapi

import (
	"context"
	"testing"
	"time"

	"chatgpt-space-merge/internal/nexovcc"
)

func TestNexovccIncompleteFinancePreservesSnapshot(t *testing.T) {
	s, _ := nexFixtureServer(t)
	previous := nexovcc.Finance{At: time.Now().Add(-time.Hour), Funds: []nexovcc.Fund{{ID: "prior-record", Currency: "USD", Kind: 3003, Amount: "-0.50"}}}
	if err := s.store.SaveNexovccFinance(previous); err != nil {
		t.Fatal(err)
	}
	base := s.nexovcc.transport
	s.nexovcc.transport = func(ctx context.Context, r nexovcc.Request) (nexovcc.Response, error) {
		if r.Path == "/api/findLogWebFunds" {
			return nexovcc.Response{Status: 502}, nil
		}
		return base(ctx, r)
	}
	if err := s.syncNexovccFinanceLocked(context.Background()); err == nil {
		t.Fatal("partial sync accepted")
	}
	var f nexovcc.Finance
	var warning string
	if err := s.store.NexovccCached("finance", &f); err != nil {
		t.Fatal(err)
	}
	if err := s.store.NexovccCached("finance_error", &warning); err != nil {
		t.Fatal(err)
	}
	if !f.At.Equal(previous.At) || len(f.Funds) != 1 || warning == "" {
		t.Fatal(f, warning)
	}
}

func TestNexovccTransferRequiresUniqueSameCardCredit(t *testing.T) {
	s, _ := nexFixtureServer(t)
	ctx := context.Background()
	op := nexovcc.Operation{ID: "test-transfer", Serial: "serial-1", Action: "withdraw", Amount: "9.00", CreatedAt: time.Now().Add(-time.Minute)}
	fund := nexovcc.Fund{ID: "credit-1", Serial: op.Serial, Kind: 4002, Currency: "USD", Amount: "9.00", At: op.CreatedAt.Add(time.Second)}
	for _, scenario := range []string{"correct", "wrong card", "old", "wrong amount", "duplicate"} {
		t.Run(scenario, func(t *testing.T) {
			r := fund
			switch scenario {
			case "wrong card":
				r.Serial = "serial-2"
			case "old":
				r.At = op.CreatedAt.Add(-time.Hour)
			case "wrong amount":
				r.Amount = "8.00"
			}
			f := nexovcc.Finance{At: time.Now(), Funds: []nexovcc.Fund{r}}
			if scenario == "duplicate" {
				r.ID = "credit-2"
				f.Funds = append(f.Funds, r)
			}
			if err := s.store.SaveNexovccFinance(f); err != nil {
				t.Fatal(err)
			}
			current := op
			if got := s.nexovccTransferConfirmedLocked(ctx, &current, 4002, "9.00"); got != (scenario == "correct") {
				t.Fatal(scenario, got)
			}
		})
	}
}

func TestNexovccSyncRefreshesImportedCardBalance(t *testing.T) {
	for _, mode := range []string{"active", "frozen", "deleted", "balance failure"} {
		t.Run(mode, func(t *testing.T) {
			s, _ := nexFixtureServer(t)
			ctx := context.Background()
			opened, err := s.openNexovccLocked(ctx, "nex-balance-open1", "template", false)
			if err != nil {
				t.Fatal(err)
			}
			old, _ := s.store.NexovccCard(opened.CardID)
			old.Balance, old.BalanceAt = "120.00", time.Now().Add(-time.Hour)
			if _, err = s.store.SaveNexovccCard(old, nil); err != nil {
				t.Fatal(err)
			}
			base := s.nexovcc.transport
			calls := 0
			s.nexovcc.transport = func(ctx context.Context, r nexovcc.Request) (nexovcc.Response, error) {
				switch r.Path {
				case "/api/card/findUserCardList":
					status := "1"
					if mode == "frozen" {
						status = "2"
					}
					if mode == "deleted" {
						status = "4"
					}
					return nexReply(map[string]any{"list": []any{map[string]any{"serial_no": old.Serial, "status": status}}}), nil
				case "/api/card/getUserCardDetail":
					t.Fatal("already imported card should not fetch card secrets during balance sync")
				case "/api/card/getUserCardBalance":
					calls++
					if mode == "balance failure" {
						return nexovcc.Response{Status: 502}, nil
					}
					return nexReply(map[string]any{"balance": "27.67"}), nil
				}
				return base(ctx, r)
			}
			s.nexovcc.client = nil
			err = s.syncNexovccLocked(ctx)
			if (err != nil) != (mode == "balance failure") {
				t.Fatal(err)
			}
			got, err := s.store.NexovccCard(old.ID)
			if err != nil || !got.Imported {
				t.Fatal(got, err)
			}
			if mode == "deleted" {
				if calls != 0 || got.Status != "4" {
					t.Fatal("queried deleted card", got, calls)
				}
			} else if mode == "balance failure" {
				if calls != 1 || got.Error == "" || got.Balance != old.Balance || !got.BalanceAt.Equal(old.BalanceAt) {
					t.Fatal("failed refresh hid stale data", got, calls)
				}
			} else if calls != 1 || got.Balance != "27.67" || !got.BalanceAt.After(old.BalanceAt) || got.Error != "" {
				t.Fatal("balance not refreshed", got, calls)
			}
		})
	}
}
