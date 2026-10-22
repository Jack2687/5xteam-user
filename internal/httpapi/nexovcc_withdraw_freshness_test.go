package httpapi

import (
	"context"
	"testing"
	"time"

	"chatgpt-space-merge/internal/gptpay"
	"chatgpt-space-merge/internal/nexovcc"
)

func TestNexovccAutoWithdrawRefreshesStaleCandidates(t *testing.T) {
	for _, scenario := range []string{"refund arrived", "prior balance error", "one dollar", "ledger failed", "balance failed", "refund absent", "pending category", "closed category", "reclassified during query", "under limit", "unknown operation"} {
		t.Run(scenario, func(t *testing.T) {
			t.Parallel()
			s, _ := nexFixtureServer(t)
			cfg, _, _ := s.store.NexovccSettings()
			cfg.AutoWithdrawEnabled = true
			if _, err := s.store.SaveNexovccSettings(cfg, nexovcc.Credentials{}, false); err != nil {
				t.Fatal(err)
			}
			old := time.Now().Add(-2 * time.Hour)
			card, err := s.store.SaveNexovccCard(nexovcc.Card{Serial: "stale-refund", Status: "1", Balance: "1.00", BalanceAt: old}, &nexovcc.PrivateCard{Number: "4242424242424242", CVV: "123", Month: 12, Year: 2099})
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "pending category" {
				if _, err = s.store.SetNexovccCardNormalPending(card.ID, true); err != nil {
					t.Fatal(err)
				}
			}
			paidAt := old.Add(-time.Minute)
			order := gptpay.Order{ID: "stale-order", Email: "stale@example.com", CardID: card.ID, Status: "success", CreatedAt: paidAt, SucceededAt: &paidAt}
			order.Remote.PaymentAmount, order.Remote.PaymentCurrency = "9.00", "USD"
			if err = s.store.CreateGPTPayOrder(order, gptpay.Snapshot{Input: gptpay.CreateInput{CardSecret: gptpay.CardSecret{Number: "4242424242424242", CVV: "123", Month: 12, Year: 2099}}}); err != nil {
				t.Fatal(err)
			}
			finance := nexovcc.Finance{At: old, CardAt: map[string]time.Time{card.Serial: old}, Trades: []nexovcc.Trade{{ID: "paid", Serial: card.Serial, Authorization: "auth", Currency: "USD", Amount: "9.00", MerchantAmount: "9.00", MerchantCurrency: "USD", Status: "PENDING", At: paidAt}}}
			if err = s.store.SaveNexovccFinance(finance); err != nil {
				t.Fatal(err)
			}
			if scenario == "prior balance error" {
				card.Error = "余额同步失败：HTTP 522"
				if _, err = s.store.SaveNexovccCard(card, nil); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "closed category" {
				card.Status = "4"
				if _, err = s.store.SaveNexovccCard(card, nil); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "under limit" {
				cfg.DefaultMaxAccounts = 2
				if _, err = s.store.SaveNexovccSettings(cfg, nexovcc.Credentials{}, false); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "unknown operation" {
				if err = s.store.BeginNexovccOperation(nexovcc.Operation{ID: "nex-unknown1", Action: "freeze", CardID: card.ID, Serial: card.Serial, Status: "unknown", Amount: "1.00", CreatedAt: old}); err != nil {
					t.Fatal(err)
				}
			}
			balance, reads, writes, ledgerReads := "10.00", 0, 0, 0
			if scenario == "one dollar" {
				balance = "1.00"
			}
			base := s.nexovcc.transport
			s.nexovcc.transport = func(ctx context.Context, r nexovcc.Request) (nexovcc.Response, error) {
				reads++
				switch r.Path {
				case "/api/card/getUserCardDetail":
					if scenario != "prior balance error" {
						t.Fatal("unnecessary card details")
					}
					return base(ctx, r)
				case "/api/card/findUserCardList", "/api/findLogRecharge", "/api/findLogReportCardDay", "/api/product/findUserProductList", "/api/card/lockUserCard", "/api/card/deleteUserCard":
					t.Fatal("withdrawal expanded outside candidate", r.Path)
				case "/api/card/getUserCardBalance":
					if scenario == "reclassified during query" {
						if _, err := s.store.SetNexovccCardNormalPending(card.ID, true); err != nil {
							t.Fatal(err)
						}
					}
					if r.Body["serial_no"] != card.Serial {
						t.Fatal("wrong card", r.Body)
					}
					if scenario == "balance failed" {
						return nexovcc.Response{Status: 522}, nil
					}
					return nexReply(map[string]any{"balance": balance}), nil
				case "/api/findLogCardTrades":
					ledgerReads++
					if r.Body["card_no"] != "4242424242424242" {
						t.Fatal("unscoped trade query", r.Body)
					}
					if scenario == "ledger failed" {
						return nexovcc.Response{Status: 522}, nil
					}
					rows := []any{map[string]any{"log_no": "paid", "serial_no": card.Serial, "business_token": "auth", "money_id": 3, "trade_amount": "9.00", "merchant_amount": "9.00", "merchant_currency": "USD", "status": "PENDING", "created_at": paidAt.Format(time.RFC3339Nano)}}
					if scenario != "refund absent" {
						rows = append(rows, map[string]any{"log_no": "returned", "serial_no": card.Serial, "business_token": "auth", "money_id": 3, "trade_amount": "9.00", "merchant_amount": "9.00", "merchant_currency": "USD", "status": "REVERSED", "created_at": old.Add(time.Hour).Format(time.RFC3339Nano)})
					}
					return nexReply(map[string]any{"list": rows, "total": len(rows), "page": r.Body["page"]}), nil
				case "/api/findLogWebFunds":
					if r.Body["card_no"] != "4242424242424242" {
						t.Fatal("unscoped fund query", r.Body)
					}
					rows := []any{}
					if writes > 0 {
						rows = append(rows, map[string]any{"ID": "withdraw-receipt", "serial_no": card.Serial, "money_id": 3, "change_type_id": 4002, "amount": "9.00", "after_balance": "109.00", "created_at": time.Now().Format(time.RFC3339Nano)})
					}
					return nexReply(map[string]any{"list": rows, "total": len(rows), "page": r.Body["page"]}), nil
				case "/api/card/userCardWithdraw":
					writes++
					if r.Body["serial_no"] != card.Serial || r.Body["amount"] != "9.00" {
						t.Fatal("wrong withdrawal", r.Body)
					}
					balance = "1.00"
					return nexReply(nil), nil
				}
				return base(ctx, r)
			}
			err = s.autoWithdrawNexovccLocked(context.Background())
			wantErr := scenario == "ledger failed" || scenario == "balance failed"
			if (err != nil) != wantErr {
				t.Fatal("unexpected error", err)
			}
			wantWrites := 0
			if scenario == "refund arrived" || scenario == "prior balance error" {
				wantWrites = 1
			}
			if writes != wantWrites {
				t.Fatal("withdrawals", writes, "wanted", wantWrites)
			}
			if scenario == "under limit" || scenario == "unknown operation" || scenario == "pending category" || scenario == "closed category" {
				if reads != 0 {
					t.Fatal("ineligible card contacted supplier", reads)
				}
			}
			if scenario == "one dollar" && ledgerReads != 0 {
				t.Fatal("unnecessary ledgers for 1U")
			}
			if wantWrites == 1 {
				ops, err := s.store.NexovccOperations()
				if err != nil || len(ops) != 1 || ops[0].Status != "succeeded" {
					t.Fatal(ops, err)
				}
				if err = s.autoWithdrawNexovccLocked(context.Background()); err != nil || writes != 1 {
					t.Fatal("duplicate withdrawal", writes, err)
				}
			}
		})
	}
}
