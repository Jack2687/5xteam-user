package httpapi

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"chatgpt-space-merge/internal/nexovcc"
)

func TestNexovccAutoWithdrawRefundsOnlyAndNoDuplicate(t *testing.T) {
	s, _ := nexFixtureServer(t)
	pans := []string{"4242424242424242", "5555555555554444", "4000000000000002", "4000000000000010", "4000000000000028"}
	cards := []nexovcc.Card{}
	f := nexovcc.Finance{At: time.Now()}
	balances := map[string]string{}
	for i, pan := range pans {
		balance := "10.00"
		if i == 3 {
			balance = "1.00"
		}
		serial := fmt.Sprintf("autowd-%d", i)
		card, err := s.store.SaveNexovccCard(nexovcc.Card{Serial: serial, Status: "1", Balance: balance, CreatedAt: time.Now().Add(time.Duration(i-5) * time.Hour)}, &nexovcc.PrivateCard{Number: pan, CVV: "123", Month: 12, Year: 2099})
		if err != nil {
			t.Fatal(err)
		}
		cards = append(cards, card)
		balances[serial] = balance
		if i == 2 { // Fresh unused card.
			continue
		}
		for j, status := range []string{"PENDING", "REVERSED"} {
			if i == 4 && j == 1 { // Payment still awaiting refund.
				continue
			}
			f.Trades = append(f.Trades, nexovcc.Trade{ID: fmt.Sprintf("t-%d-%d", i, j), Serial: serial, Authorization: "auth", Currency: "USD", Amount: "9.00", MerchantAmount: "9.00", MerchantCurrency: "USD", Status: status, At: time.Now().Add(time.Duration(j-2) * time.Minute)})
		}
	}
	if err := s.store.SaveNexovccFinance(f); err != nil {
		t.Fatal(err)
	}
	base := s.nexovcc.transport
	var writes []time.Time
	s.nexovcc.transport = func(ctx context.Context, r nexovcc.Request) (nexovcc.Response, error) {
		serial, _ := r.Body["serial_no"].(string)
		switch r.Path {
		case "/api/card/getUserCardDetail":
			for i, c := range cards {
				if c.Serial == serial {
					return nexReply(map[string]any{"serial_no": serial, "status": "1", "card_no": pans[i], "cvv": "123", "expire_at_str": "12/99"}), nil
				}
			}
		case "/api/card/getUserCardBalance":
			return nexReply(map[string]any{"balance": balances[serial]}), nil
		case "/api/card/userCardWithdraw":
			if (serial != cards[0].Serial && serial != cards[1].Serial) || r.Body["amount"] != "9.00" {
				t.Fatal("withdrew protected card or wrong amount", r.Body)
			}
			writes = append(writes, time.Now())
			balances[serial] = "1.00"
			return nexReply(nil), nil
		case "/api/card/lockUserCard", "/api/card/deleteUserCard":
			t.Fatal("withdraw-only froze or closed card", r.Path)
		}
		return base(ctx, r)
	}
	if err := s.autoWithdrawNexovccLocked(context.Background()); err != nil || len(writes) != 0 {
		t.Fatal("default-off auto-withdraw executed", err, writes)
	}
	cfg, _, _ := s.store.NexovccSettings()
	cfg.AutoWithdrawEnabled, cfg.BatchIntervalSeconds = true, 1
	if _, err := s.store.SaveNexovccSettings(cfg, nexovcc.Credentials{}, false); err != nil {
		t.Fatal(err)
	}
	if err := s.autoWithdrawNexovccLocked(context.Background()); err != nil || len(writes) != 2 {
		t.Fatal(err, writes)
	}
	if writes[1].Sub(writes[0]) < time.Second {
		t.Fatal("configured batch gap missing", writes)
	}
	// A stale repeated balance cannot replay an unresolved money operation.
	for i := 0; i < 2; i++ {
		card := cards[i]
		card.Balance = "10.00"
		if _, err := s.store.SaveNexovccCard(card, nil); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.autoWithdrawNexovccLocked(context.Background()); err != nil || len(writes) != 2 {
		t.Fatal("duplicate withdrawal", err, writes)
	}
	before := nexovccAutoWithdrawID(cards[0], f)
	f.At = time.Now().Add(time.Hour)
	f.Funds = append(f.Funds, nexovcc.Fund{ID: "our-withdrawal", Serial: cards[0].Serial, Kind: 4002, Amount: "9.00"})
	if nexovccAutoWithdrawID(cards[0], f) != before {
		t.Fatal("polling/own withdrawal changed id")
	}
	f.Funds = append(f.Funds, nexovcc.Fund{ID: "new-deposit", Serial: cards[0].Serial, Kind: 4001, Amount: "9.00"})
	if nexovccAutoWithdrawID(cards[0], f) == before {
		t.Fatal("new funding cycle did not change id")
	}
}

func TestNexovccCombinedCloseAtOrBelowOneDollar(t *testing.T) {
	for _, amount := range []string{"1.00", "0.50", "0.00"} {
		for _, automatic := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/auto=%v", amount, automatic), func(t *testing.T) {
				s, f := nexFixtureServer(t)
				card, err := s.store.SaveNexovccCard(nexovcc.Card{Serial: "nex-test-serial", Status: "1", Balance: amount}, &nexovcc.PrivateCard{Number: "4242424242424242", CVV: "123", Month: 12, Year: 2099})
				if err != nil {
					t.Fatal(err)
				}
				base := s.nexovcc.transport
				var frozen, closed time.Time
				receipt := false
				s.nexovcc.transport = func(ctx context.Context, r nexovcc.Request) (nexovcc.Response, error) {
					switch r.Path {
					case "/api/card/getUserCardBalance":
						return nexReply(map[string]any{"balance": amount}), nil
					case "/api/card/userCardWithdraw":
						t.Fatal("zero-value withdrawal sent")
					case "/api/card/lockUserCard":
						frozen = time.Now()
					case "/api/card/deleteUserCard":
						closed = time.Now()
						if frozen.IsZero() || closed.Sub(frozen) < 3*time.Second {
							t.Fatal("freeze/gap missing")
						}
					case "/api/findLogWebFunds":
						rows := []any{}
						if receipt {
							rows = append(rows, map[string]any{"ID": "close-refund", "serial_no": card.Serial, "money_id": 3, "change_type_id": 3002, "amount": amount, "after_balance": "110.00", "created_at": closed.Format(time.RFC3339Nano)})
						}
						return nexReply(map[string]any{"list": rows, "total": len(rows), "page": 1}), nil
					}
					return base(ctx, r)
				}
				op, err := s.actionNexovccLocked(context.Background(), nexovccInput{Action: "withdraw_cancel", CardID: card.ID, RequestID: "nex-one-dollar1", CapacityRecycle: automatic})
				if err != nil || op.WithdrawAmount != "0.00" || f.cancelCalls != 1 || op.BeforeBalance != amount {
					t.Fatal(op, err, f.cancelCalls)
				}
				if amount != "0.00" {
					if op.Status != "processing" {
						t.Fatal("marked success before refund receipt", op)
					}
					receipt = true
					if err = s.syncNexovccFinanceLocked(context.Background()); err != nil {
						t.Fatal(err)
					}
					op = s.verifyNexovccOperationLocked(context.Background(), op)
					if !strings.HasPrefix(op.Evidence, "fund:") {
						t.Fatal("refund not verified", op)
					}
				}
				if op.Status != "succeeded" || f.cancelCalls != 1 {
					t.Fatal(op, f.cancelCalls)
				}
			})
		}
	}
}
