package httpapi

import (
	"context"
	"fmt"
	"testing"
	"time"

	"chatgpt-space-merge/internal/gptpay"
	"chatgpt-space-merge/internal/nexovcc"
)

func TestNexovccAutoWithdrawBalanceBeforeRefund(t *testing.T) {
	for _, scenario := range []string{"receipt confirmed", "receipt pending", "unknown response", "live balance 1U", "live balance not recovered", "limit raised before send"} {
		t.Run(scenario, func(t *testing.T) {
			t.Parallel()
			s, _ := nexFixtureServer(t)
			ctx := context.Background()
			cfg, _, _ := s.store.NexovccSettings()
			cfg.AutoWithdrawEnabled, cfg.DefaultMaxAccounts, cfg.RequestIntervalSeconds = true, 2, 1
			if _, err := s.store.SaveNexovccSettings(cfg, nexovcc.Credentials{}, false); err != nil {
				t.Fatal(err)
			}
			card, err := s.store.SaveNexovccCard(nexovcc.Card{Serial: "balance-before-refund", Status: "1", Balance: "169.39", BalanceAt: time.Now()}, &nexovcc.PrivateCard{Number: "4242424242424242", CVV: "123", Month: 12, Year: 2099})
			if err != nil {
				t.Fatal(err)
			}
			f := nexovcc.Finance{Funds: []nexovcc.Fund{{ID: "funding", Serial: card.Serial, Currency: "USD", Amount: "-170.00", Kind: 3001, At: time.Now().Add(-4 * time.Hour)}}}
			for i := 0; i < 2; i++ {
				at := time.Now().Add(time.Duration(i*2-3) * time.Hour)
				order := gptpay.Order{ID: fmt.Sprintf("balance-order-%d", i), Email: fmt.Sprintf("balance-%d@example.com", i), CardID: card.ID, Status: "success", CreatedAt: at, SucceededAt: &at}
				order.Remote.PaymentAmount, order.Remote.PaymentCurrency = "8919.64", "PHP"
				if err = s.store.CreateGPTPayOrder(order, gptpay.Snapshot{Input: gptpay.CreateInput{CardSecret: gptpay.CardSecret{Number: "4242424242424242", CVV: "123", Month: 12, Year: 2099}}}); err != nil {
					t.Fatal(err)
				}
				trade := nexovcc.Trade{ID: fmt.Sprintf("paid-%d", i), Serial: card.Serial, Authorization: fmt.Sprintf("AUTH%d", i), Currency: "USD", Amount: "141.82", MerchantAmount: "8919.64", MerchantCurrency: "PHP", Status: "PENDING", At: at}
				f.Trades = append(f.Trades, trade)
				if i == 0 {
					trade.ID = "refunded-0"
					trade.Status = "REVERSED"
					trade.At = at.Add(time.Minute)
					f.Trades = append(f.Trades, trade)
					if err = s.store.SaveNexovccFinance(f); err != nil {
						t.Fatal(err)
					}
				}
			}
			if err = s.store.SetNexovccCase("balance-order-1", "waiting", "已完成合并申请回款", 0); err != nil {
				t.Fatal(err)
			}
			f.At = time.Now()
			f.CardAt = map[string]time.Time{card.Serial: f.At}
			if err = s.store.SaveNexovccFinance(f); err != nil {
				t.Fatal(err)
			}
			card.BalanceAt = time.Now()
			if _, err = s.store.SaveNexovccCard(card, nil); err != nil {
				t.Fatal(err)
			}
			cases, err := s.store.NexovccCases()
			if err != nil || len(cases) != 2 {
				t.Fatal(cases, err)
			}
			if nexovccRecycleCandidate(card, cases, nexovcc.Groups(f)) {
				t.Fatal("unrefunded card eligible for recycling")
			}
			proof, err := s.store.NexovccBalanceWithdrawal(card.ID)
			if err != nil || proof == nil {
				t.Fatal("recovered balance not a candidate", proof, err)
			}
			base := s.nexovcc.transport
			liveBalance := "169.39"
			switch scenario {
			case "live balance 1U":
				liveBalance = "1.00"
			case "live balance not recovered":
				liveBalance = "28.18"
			}
			writes := 0
			s.nexovcc.transport = func(ctx context.Context, r nexovcc.Request) (nexovcc.Response, error) {
				switch r.Path {
				case "/api/card/getUserCardBalance":
					if scenario == "limit raised before send" {
						cfg.DefaultMaxAccounts = 3
						if _, err := s.store.SaveNexovccSettings(cfg, nexovcc.Credentials{}, false); err != nil {
							t.Fatal(err)
						}
					}
					return nexReply(map[string]any{"balance": liveBalance}), nil
				case "/api/card/userCardWithdraw":
					writes++
					if r.Body["amount"] != "168.39" || r.Body["serial_no"] != card.Serial {
						t.Fatal("wrong withdrawal", r.Body)
					}
					if scenario == "unknown response" {
						return nexovcc.Response{Status: 502}, nil
					}
					liveBalance = "1.00"
					if scenario == "receipt confirmed" {
						f.Funds = append(f.Funds, nexovcc.Fund{ID: "withdraw-receipt", Serial: card.Serial, Currency: "USD", Kind: 4002, Amount: "168.39", Balance: "268.39", At: time.Now()})
					}
					return nexReply(nil), nil
				case "/api/findLogWebFunds":
					if r.Body["card_no"] != "4242424242424242" {
						t.Fatal("withdrawal receipt query not scoped to card", r.Body)
					}
					rows := []any{}
					for _, fund := range f.Funds {
						rows = append(rows, map[string]any{"ID": fund.ID, "serial_no": fund.Serial, "money_id": 3, "change_type_id": fund.Kind, "amount": fund.Amount, "after_balance": "268.39", "created_at": fund.At.Format(time.RFC3339Nano)})
					}
					return nexReply(map[string]any{"list": rows, "total": len(rows), "page": 1}), nil
				case "/api/card/lockUserCard", "/api/card/deleteUserCard":
					t.Fatal("withdraw-only froze/closed a card")
				case "/api/findLogCardTrades", "/api/card/findUserCardList", "/api/card/getUserCardDetail":
					t.Fatal("withdrawal caused an unnecessary scan", r.Path)
				}
				return base(ctx, r)
			}
			err = s.autoWithdrawNexovccLocked(ctx)
			wantErr := scenario == "unknown response" || scenario == "live balance not recovered" || scenario == "limit raised before send"
			if (err != nil) != wantErr {
				t.Fatal("unexpected result", err)
			}
			wantWrites := 1
			if scenario == "live balance 1U" || scenario == "live balance not recovered" || scenario == "limit raised before send" {
				wantWrites = 0
			}
			if writes != wantWrites {
				t.Fatalf("writes=%d want=%d", writes, wantWrites)
			}
			if wantWrites == 0 {
				return
			}
			ops, err := s.store.NexovccOperations()
			if err != nil || len(ops) != 1 || ops[0].BalanceWithdrawal == nil {
				t.Fatal(ops, err)
			}
			wantStatus := map[string]string{"receipt confirmed": "succeeded", "receipt pending": "processing", "unknown response": "unknown"}[scenario]
			if ops[0].Status != wantStatus {
				t.Fatal(ops[0])
			}
			local, _, err := s.store.GPTPayCard(card.ID)
			if err != nil || local.ActiveAccounts != 1 || local.Available {
				t.Fatal("occupancy released before refund", local, err)
			}
			cases, err = s.store.NexovccCases()
			if err != nil {
				t.Fatal(err)
			}
			for _, c := range cases {
				if c.ID == "balance-order-1" && (c.State != "waiting" || c.Match == nil || c.Match.Group.FullyReturned) {
					t.Fatal("balance fabricated refund", c)
				}
			}
			for _, action := range []string{"cancel", "withdraw_cancel"} {
				_, err = s.actionNexovccLocked(ctx, nexovccInput{Action: action, CardID: card.ID, RequestID: "nex-close-" + action, CapacityRecycle: true})
				if err == nil {
					t.Fatal("closing allowed before refund", action)
				}
			}
			// Even stale cache + late refund rows + a new scheduler instance must
			// not turn this same recovered balance into a second money request.
			late := f.Trades[len(f.Trades)-1]
			late.ID = "refunded-1"
			late.Status = "REVERSED"
			late.Amount = "141.23"
			late.At = time.Now()
			f.Trades = append(f.Trades, late)
			f.At = time.Now()
			f.CardAt[card.Serial] = f.At
			if err = s.store.SaveNexovccFinance(f); err != nil {
				t.Fatal(err)
			}
			if scenario == "receipt confirmed" {
				if err = s.store.SetNexovccCase("balance-order-1", "auto", "恢复自动识别退款", 1); err != nil {
					t.Fatal(err)
				}
			}
			card.BalanceAt = time.Now()
			card.Balance = "169.39"
			if _, err = s.store.SaveNexovccCard(card, nil); err != nil {
				t.Fatal(err)
			}
			restarted := &Server{store: s.store}
			restarted.nexovcc.transport = s.nexovcc.transport
			if err = restarted.autoWithdrawNexovccLocked(ctx); err != nil || writes != 1 {
				t.Fatal("late refund replayed withdrawal", err, writes)
			}
		})
	}
}
