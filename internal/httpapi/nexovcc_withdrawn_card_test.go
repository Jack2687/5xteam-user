package httpapi

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"chatgpt-space-merge/internal/nexovcc"
)

func TestNexovccWithdrawnToReserveEvidence(t *testing.T) {
	for _, mode := range []string{"confirmed", "manual amount", "below reserve", "no operation", "low spent balance", "failed", "unknown", "processing", "zero withdrawal", "wrong card", "wrong serial", "stale balance", "missing balance time", "pending operation", "refunded again", "funded again"} {
		t.Run(mode, func(t *testing.T) {
			at := time.Now().Add(-time.Hour)
			card := nexovcc.Card{ID: "card", Serial: "serial", Balance: "1.00", BalanceAt: at.Add(time.Minute)}
			op := nexovcc.Operation{ID: "withdrawn", CardID: card.ID, Serial: card.Serial, Action: "withdraw_all", Status: "succeeded", BeforeBalance: "10.00", Amount: "9.00", CreatedAt: at}
			var extra []nexovcc.Operation
			switch mode {
			case "manual amount":
				op.Action = "withdraw"
			case "below reserve":
				card.Balance = "0.50"
			case "no operation":
				op.Action = "open"
			case "low spent balance":
				op.Amount = "8.00"
			case "failed", "unknown", "processing":
				op.Status = mode
			case "zero withdrawal":
				op.BeforeBalance, op.Amount = "1.00", "0.00"
			case "wrong card":
				op.CardID = "other"
			case "wrong serial":
				op.Serial = "other"
			case "stale balance":
				card.BalanceAt = at.Add(-time.Minute)
			case "missing balance time":
				card.BalanceAt = time.Time{}
			case "pending operation":
				extra = append(extra, nexovcc.Operation{CardID: card.ID, Action: "cancel", Status: "processing"})
			case "refunded again":
				card.Balance = "10.00"
			case "funded again":
				extra = append(extra, nexovcc.Operation{CardID: card.ID, Serial: card.Serial, Action: "deposit", Status: "succeeded", CreatedAt: at.Add(time.Second)})
			}
			want := mode == "confirmed" || mode == "manual amount" || mode == "below reserve"
			if got := nexovccWithdrawnToReserve(card, append(extra, op)); got != want {
				t.Fatalf("skip=%v, wanted %v", got, want)
			}
		})
	}
}

func TestNexovccWithdrawnCardSkipsScheduledButAllowsManualSync(t *testing.T) {
	s, _ := nexFixtureServer(t)
	old := time.Now().Add(-2 * time.Hour)
	card, err := s.store.SaveNexovccCard(nexovcc.Card{Serial: "reserve-card", Status: "1", Balance: "1.00", BalanceAt: old.Add(time.Minute)}, &nexovcc.PrivateCard{Number: "4242424242424242", CVV: "123", Month: 12, Year: 2099})
	if err != nil {
		t.Fatal(err)
	}
	op := nexovcc.Operation{ID: "nex-reserve-skip1", CardID: card.ID, Serial: card.Serial, Action: "withdraw_all", BeforeBalance: "10.00", Amount: "9.00", Status: "succeeded", CreatedAt: old}
	if err = s.store.BeginNexovccOperation(op); err != nil {
		t.Fatal(err)
	}
	finance := nexovcc.Finance{At: old, CardAt: map[string]time.Time{card.Serial: old}, Funds: []nexovcc.Fund{{ID: "original", Serial: card.Serial, Currency: "USD", Kind: 3001, Amount: "-10.00", At: old}}}
	if err = s.store.SaveNexovccFinance(finance); err != nil {
		t.Fatal(err)
	}
	// Force a possible details repair too: automatic list sync must retain the
	// stored card without issuing even its details request.
	if err = s.store.SetNexovccCardSyncError(card.ID, "旧资料查询失败"); err != nil {
		t.Fatal(err)
	}
	card, _ = s.store.NexovccCard(card.ID)
	base := s.nexovcc.transport
	reads := map[string]int{}
	s.nexovcc.transport = func(ctx context.Context, r nexovcc.Request) (nexovcc.Response, error) {
		switch r.Path {
		case "/api/card/findUserCardList":
			return nexReply(map[string]any{"list": []any{map[string]any{"serial_no": card.Serial, "status": "1", "card_no": "4242424242424242"}}}), nil
		case "/api/card/getUserCardDetail", "/api/card/getUserCardBalance", "/api/findLogCardTrades", "/api/findLogWebFunds":
			reads[r.Path]++
		}
		return base(ctx, r)
	}
	// A fresh server with only the persisted store must skip the same card.
	restarted := &Server{store: s.store}
	restarted.nexovcc.transport = s.nexovcc.transport
	if err = restarted.syncNexovccLocked(nexovccScheduledSync(context.Background())); err != nil {
		t.Fatal(err)
	}
	if len(reads) != 0 {
		t.Fatal("scheduled sync contacted completed card", reads)
	}
	after, _ := s.store.NexovccCard(card.ID)
	var cached nexovcc.Finance
	if err = s.store.NexovccCached("finance", &cached); err != nil {
		t.Fatal(err)
	}
	beforeJSON, _ := json.Marshal(card)
	afterJSON, _ := json.Marshal(after)
	if string(beforeJSON) != string(afterJSON) || !cached.CardSyncedAt(card.Serial).Equal(old) || len(cached.Funds) != 1 || cached.Funds[0].ID != "original" || cached.Funds[0].Amount != "-10.00" || !cached.Funds[0].At.Equal(old) {
		t.Fatal("automatic sync overwrote retired card data", after, cached)
	}
	// This is the same explicit full-sync entry used by the unchanged button.
	if err = restarted.syncNexovccLocked(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	for _, endpoint := range []string{"/api/card/getUserCardDetail", "/api/card/getUserCardBalance", "/api/findLogCardTrades", "/api/findLogWebFunds"} {
		if reads[endpoint] == 0 {
			t.Fatal("manual full sync skipped card endpoint", endpoint, reads)
		}
	}
	after, _ = s.store.NexovccCard(card.ID)
	if after.Balance != "10.00" {
		t.Fatal("manual refresh did not discover changed balance", after)
	}
	ops, _ := s.store.NexovccOperations()
	if nexovccWithdrawnToReserve(after, ops) {
		t.Fatal("changed balance remains excluded")
	}
}
