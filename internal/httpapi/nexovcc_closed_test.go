package httpapi

import (
	"context"
	"strings"
	"testing"
	"time"

	"chatgpt-space-merge/internal/nexovcc"
)

// Production returns code=0,data=null for deleted card details and refuses
// balance queries. The list and the same-card refund are the completion proof.
func TestNexovccDeletedCardNullDetailConfirmsWithoutManualReview(t *testing.T) {
	for _, cached := range []bool{false, true} {
		name := "null detail falls back to list"
		if cached {
			name = "persisted deleted card skips detail"
		}
		t.Run(name, func(t *testing.T) {
			s, f := nexFixtureServer(t)
			ctx := context.Background()
			opened, err := s.openNexovccLocked(ctx, "nex-null-open1", "template", false)
			if err != nil {
				t.Fatal(err)
			}
			op := nexovcc.Operation{ID: "nex-null-close1", CardID: opened.CardID, Serial: opened.Serial, Action: "withdraw_cancel", Status: "processing", Step: "cancel_waiting", BeforeBalance: "1.00", Amount: "15.00", WithdrawAmount: "15.00", WithdrawEvidence: "fund:withdraw-receipt", CreatedAt: time.Now().Add(-time.Minute), StepAt: time.Now().Add(-30 * time.Second), Error: "结果同步失败，请稍后核实：环球响应数据不是对象"}
			if err = s.store.BeginNexovccOperation(op); err != nil {
				t.Fatal(err)
			}
			f.status = "4"
			if cached {
				if _, err = s.store.SaveNexovccCard(nexovcc.Card{Serial: op.Serial, Status: "4"}, nil); err != nil {
					t.Fatal(err)
				}
			}
			base := s.nexovcc.transport
			details, receipt := 0, false
			s.nexovcc.transport = func(ctx context.Context, r nexovcc.Request) (nexovcc.Response, error) {
				switch r.Path {
				case "/api/card/getUserCardDetail":
					details++
					return nexReply(nil), nil
				case "/api/card/getUserCardBalance", "/api/card/userCardWithdraw", "/api/card/lockUserCard", "/api/card/deleteUserCard":
					t.Fatalf("deleted card must not query balance or replay mutations: %s", r.Path)
				case "/api/findLogWebFunds":
					funds := []any{}
					if receipt {
						funds = append(funds, map[string]any{"ID": "close-receipt", "serial_no": op.Serial, "money_id": 3, "change_type_id": 3002, "amount": "1.00", "after_balance": "493.91", "created_at": op.StepAt.Format(time.RFC3339Nano)})
					}
					return nexReply(map[string]any{"list": funds, "total": len(funds), "page": 1, "pageSize": 100}), nil
				}
				return base(ctx, r)
			}
			s.nexovcc.client = nil
			if err = s.store.SaveNexovccFinance(nexovcc.Finance{At: time.Now()}); err != nil {
				t.Fatal(err)
			}
			op = s.verifyNexovccOperationLocked(ctx, op)
			if op.Status != "processing" || op.Error != "卡台已销卡，正在自动同步余款到账" {
				t.Fatal("missing receipt must remain automatic/pending", op)
			}
			if (cached && details != 0) || (!cached && details != 1) {
				t.Fatal("unexpected detail calls", details)
			}
			if err = s.store.SaveNexovccOperation(op); err != nil {
				t.Fatal(err)
			}
			// The regular background sync must repair this persisted record once
			// the receipt arrives, without any user resolution or money request.
			receipt = true
			if err = s.syncNexovccLocked(ctx); err != nil {
				t.Fatal(err)
			}
			op, err = s.store.NexovccOperation(op.ID)
			if err != nil || op.Status != "succeeded" || op.Error != "" || !strings.HasPrefix(op.Evidence, "fund:") || op.WithdrawEvidence != "fund:withdraw-receipt" {
				t.Fatal("automatic close reconciliation failed", op, err)
			}
		})
	}
}

func TestNexovccNullDetailAloneNeverProvesDeletion(t *testing.T) {
	for _, scenario := range []string{"active", "absent", "different card"} {
		t.Run(scenario, func(t *testing.T) {
			s, _ := nexFixtureServer(t)
			ctx := context.Background()
			opened, err := s.openNexovccLocked(ctx, "nex-missing-open1", "template", false)
			if err != nil {
				t.Fatal(err)
			}
			base := s.nexovcc.transport
			s.nexovcc.transport = func(ctx context.Context, r nexovcc.Request) (nexovcc.Response, error) {
				if r.Path == "/api/card/getUserCardDetail" {
					return nexReply(nil), nil
				}
				if r.Path == "/api/card/findUserCardList" {
					rows := []any{}
					if scenario == "active" {
						rows = append(rows, map[string]any{"serial_no": opened.Serial, "status": "1"})
					} else if scenario == "different card" {
						rows = append(rows, map[string]any{"serial_no": "unrelated-card", "status": "4"})
					}
					return nexReply(map[string]any{"list": rows, "total": len(rows)}), nil
				}
				return base(ctx, r)
			}
			s.nexovcc.client = nil
			if _, err = s.refreshNexovccLocked(ctx, opened.CardID, opened.Serial); err == nil {
				t.Fatal("null detail incorrectly proved deletion")
			}
			card, _ := s.store.NexovccCard(opened.CardID)
			if card.Status != "1" {
				t.Fatal("card incorrectly marked deleted", card)
			}
		})
	}
}

func TestNexovccClosedReceiptRecoversDuringSupplierOutage(t *testing.T) {
	for _, mode := range []string{"monitor", "query"} {
		t.Run(mode, func(t *testing.T) {
			s, _ := nexFixtureServer(t)
			ctx := context.Background()
			opened, err := s.openNexovccLocked(ctx, "nex-outage-open1", "template", false)
			if err != nil {
				t.Fatal(err)
			}
			op := nexovcc.Operation{ID: "nex-outage-close1", Action: "withdraw_cancel", CardID: opened.CardID, Serial: opened.Serial, Status: "processing", Step: "cancel_waiting", BeforeBalance: "1.00", CreatedAt: time.Now().Add(-time.Minute), StepAt: time.Now().Add(-30 * time.Second)}
			if err = s.store.BeginNexovccOperation(op); err != nil {
				t.Fatal(err)
			}
			if _, err = s.store.SaveNexovccCard(nexovcc.Card{Serial: op.Serial, Status: "4"}, nil); err != nil {
				t.Fatal(err)
			}
			if err = s.store.SaveNexovccFinance(nexovcc.Finance{At: time.Now(), Funds: []nexovcc.Fund{{ID: "offline-close-credit", Serial: op.Serial, Currency: "USD", Kind: 3002, Amount: "1.00", At: op.StepAt}}}); err != nil {
				t.Fatal(err)
			}
			calls := 0
			s.nexovcc.transport = func(ctx context.Context, r nexovcc.Request) (nexovcc.Response, error) {
				calls++
				if r.Path != "/api/login" && r.Path != "/api/getBalanceInfo" {
					t.Fatalf("unexpected request during outage: %s", r.Path)
				}
				return nexovcc.Response{Status: 522}, nil
			}
			s.nexovcc.client = nil
			if mode == "monitor" {
				if err = s.syncNexovccLocked(ctx); err == nil {
					t.Fatal("other queries should report the outage")
				}
			} else {
				if _, err = s.resolveNexovccLocked(ctx, nexovccInput{RequestID: op.ID, Outcome: "query"}); err != nil || calls != 0 {
					t.Fatal("existing proof should not need another API call", err, calls)
				}
			}
			op, err = s.store.NexovccOperation(op.ID)
			if err != nil || op.Status != "succeeded" || op.Error != "" || op.Evidence != "fund:offline-close-credit" {
				t.Fatal("outage blocked automatic recovery", op, err)
			}
		})
	}
}
