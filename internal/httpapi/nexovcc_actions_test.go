package httpapi

import (
	"context"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"chatgpt-space-merge/internal/nexovcc"
)

func TestNexovccFreezeAcknowledgementUnlocksActionsAndBlocksPayments(t *testing.T) {
	s, f := nexFixtureServer(t)
	ctx := context.Background()
	op, err := s.openNexovccLocked(ctx, "nex-open-freeze1", "template", false)
	if err != nil {
		t.Fatal(err)
	}
	base := s.nexovcc.transport
	calls := 0
	s.nexovcc.transport = func(ctx context.Context, r nexovcc.Request) (nexovcc.Response, error) {
		if r.Path == "/api/card/lockUserCard" {
			calls++
			return nexReply(nil), nil
		} // detail can lag behind the success acknowledgement
		return base(ctx, r)
	}
	s.nexovcc.client = nil
	frozen, err := s.actionNexovccLocked(ctx, nexovccInput{Action: "freeze", CardID: op.CardID, RequestID: "nex-freeze-ack1"})
	if err != nil || frozen.Status != "succeeded" || calls != 1 {
		t.Fatal(frozen, err, calls)
	}
	if _, err = s.refreshNexovccLocked(ctx, op.CardID, op.Serial); err != nil {
		t.Fatal(err)
	}
	card, _, err := s.store.GPTPayCard(op.CardID)
	if err != nil || !card.IssuerFrozen || card.Available {
		t.Fatal("frozen card was not shown/blocked", card, err)
	}
	if _, err = s.actionNexovccLocked(ctx, nexovccInput{Action: "freeze", CardID: op.CardID, RequestID: "nex-freeze-ack2"}); err != nil || calls != 1 {
		t.Fatal("repeated freeze", calls, err)
	}
	unfrozen, err := s.actionNexovccLocked(ctx, nexovccInput{Action: "unfreeze", CardID: op.CardID, RequestID: "nex-unfreeze-ack1"})
	card, _, _ = s.store.GPTPayCard(op.CardID)
	if err != nil || unfrozen.Status != "succeeded" || card.IssuerFrozen || f.status != "1" {
		t.Fatal(unfrozen, card, err)
	}
}

func TestNexovccLegacyAcknowledgedFreezeRecoversWithoutManualConfirmation(t *testing.T) {
	s, _ := nexFixtureServer(t)
	ctx := context.Background()
	opened, err := s.openNexovccLocked(ctx, "nex-open-legacy1", "template", false)
	if err != nil {
		t.Fatal(err)
	}
	op := nexovcc.Operation{ID: "nex-freeze-legacy1", Action: "freeze", CardID: opened.CardID, Serial: opened.Serial, Status: "processing", CreatedAt: time.Now().Add(-time.Hour), UpdatedAt: time.Now().Add(-time.Minute)}
	if err = s.store.BeginNexovccOperation(op); err != nil {
		t.Fatal(err)
	}
	op = s.verifyNexovccOperationLocked(ctx, op)
	if err = s.store.SaveNexovccOperation(op); err != nil || op.Status != "succeeded" {
		t.Fatal(op, err)
	}
	card, _, _ := s.store.GPTPayCard(opened.CardID)
	if !card.IssuerFrozen {
		t.Fatal("freeze state lost")
	}
}

func TestNexovccWithdrawAndCloseSequenceSpacingAndIdempotence(t *testing.T) {
	s, f := nexFixtureServer(t)
	ctx := context.Background()
	base := s.nexovcc.transport
	balance := int64(1000)
	var calls []string
	var times []time.Time
	var funds []any
	s.nexovcc.transport = func(ctx context.Context, r nexovcc.Request) (nexovcc.Response, error) {
		switch r.Path {
		case "/api/card/getUserCardBalance":
			return nexReply(map[string]any{"balance": nexovcc.Money(balance)}), nil
		case "/api/findLogWebFunds":
			return nexReply(map[string]any{"list": funds, "total": len(funds), "page": 1, "pageSize": 100}), nil
		case "/api/card/userCardWithdraw":
			if r.Body["amount"] != "9.00" {
				t.Fatalf("wrong withdraw amount %v", r.Body["amount"])
			}
			balance = 100
			calls = append(calls, "withdraw")
			times = append(times, time.Now())
			funds = append(funds, map[string]any{"ID": "withdraw-test", "serial_no": "nex-test-serial", "money_id": 3, "change_type_id": 4002, "amount": "9.00", "after_balance": "109.00", "created_at": time.Now().Format(time.RFC3339Nano)})
			return nexReply(nil), nil
		case "/api/card/lockUserCard":
			calls = append(calls, "freeze")
			times = append(times, time.Now())
			if err := s.store.BeginNexovccOperation(nexovcc.Operation{ID: "nex-interleave1", Action: "withdraw", CardID: nexovcc.CardID("nex-test-serial"), Status: "submitted"}); err == nil {
				t.Fatal("pipeline lost its card lock")
			}
		case "/api/card/deleteUserCard":
			if f.status == "1" {
				t.Fatal("cancel before freeze")
			}
			calls = append(calls, "cancel")
			times = append(times, time.Now())
			balance = 0
			funds = append(funds, map[string]any{"ID": "cancel-test", "serial_no": "nex-test-serial", "money_id": 3, "change_type_id": 3002, "amount": "1.00", "after_balance": "110.00", "created_at": time.Now().Format(time.RFC3339Nano)})
		}
		return base(ctx, r)
	}
	opened, err := s.openNexovccLocked(ctx, "nex-open-sequence1", "template", false)
	if err != nil {
		t.Fatal(err)
	}
	in := nexovccInput{Action: "withdraw_cancel", CardID: opened.CardID, RequestID: "nex-close-sequence1"}
	op, err := s.actionNexovccLocked(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	// Final cancellation receipt may need the next read-only financial refresh.
	if err = s.syncNexovccFinanceLocked(ctx); err != nil {
		t.Fatal(err)
	}
	op = s.verifyNexovccOperationLocked(ctx, op)
	if err = s.store.SaveNexovccOperation(op); err != nil {
		t.Fatal(err)
	}
	if op.Status != "succeeded" || fmt.Sprint(calls) != "[withdraw freeze cancel]" || op.WithdrawEvidence == "" || op.Evidence == "" {
		t.Fatal(op, calls)
	}
	for i := 1; i < len(times); i++ {
		if times[i].Sub(times[i-1]) < 3*time.Second {
			t.Fatal("missing interface interval", times)
		}
	}
	if _, err = s.actionNexovccLocked(ctx, in); err != nil || len(calls) != 3 {
		t.Fatal("duplicate request replayed", calls, err)
	}
}

func TestNexovccCloseUnknownStepsNeverReplay(t *testing.T) {
	for _, step := range []string{"withdraw", "freeze"} {
		t.Run(step, func(t *testing.T) {
			s, _ := nexFixtureServer(t)
			ctx := context.Background()
			base := s.nexovcc.transport
			writes := 0
			s.nexovcc.transport = func(ctx context.Context, r nexovcc.Request) (nexovcc.Response, error) {
				if step == "freeze" && r.Path == "/api/card/getUserCardBalance" {
					return nexReply(map[string]any{"balance": "1.00"}), nil
				}
				if r.Path == "/api/card/userCardWithdraw" || r.Path == "/api/card/lockUserCard" {
					writes++
					return nexovcc.Response{Status: 502}, nil
				}
				if r.Path == "/api/card/deleteUserCard" {
					t.Fatal("cancel after unknown result")
				}
				return base(ctx, r)
			}
			op, err := s.openNexovccLocked(ctx, "nex-open-unknown1", "template", false)
			if err != nil {
				t.Fatal(err)
			}
			in := nexovccInput{Action: "withdraw_cancel", CardID: op.CardID, RequestID: "nex-close-unknown1"}
			result, err := s.actionNexovccLocked(ctx, in)
			if err != nil || result.Status != "unknown" || writes != 1 {
				t.Fatal(result, err, writes)
			}
			if _, err = s.actionNexovccLocked(ctx, in); err != nil || writes != 1 {
				t.Fatal(err, writes)
			}
			in.RequestID = "nex-close-unknown2"
			if _, err = s.actionNexovccLocked(ctx, in); err == nil || writes != 1 {
				t.Fatal("unknown operation did not lock card", err, writes)
			}
		})
	}
}

func TestNexovccOneClickWithdrawUsesLiveBalanceAndSkipsSmallBalance(t *testing.T) {
	s, _ := nexFixtureServer(t)
	ctx := context.Background()
	base := s.nexovcc.transport
	balance := "10.37"
	writes := 0
	s.nexovcc.transport = func(ctx context.Context, r nexovcc.Request) (nexovcc.Response, error) {
		if r.Path == "/api/card/getUserCardBalance" {
			return nexReply(map[string]any{"balance": balance}), nil
		}
		if r.Path == "/api/card/userCardWithdraw" {
			writes++
			if r.Body["amount"] != "9.37" {
				t.Fatal(r.Body["amount"])
			}
			return nexReply(nil), nil
		}
		return base(ctx, r)
	}
	op, err := s.openNexovccLocked(ctx, "nex-open-withdraw1", "template", false)
	if err != nil {
		t.Fatal(err)
	}
	for _, amount := range []string{"1.00", "0.50"} {
		balance = amount
		result, err := s.actionNexovccLocked(ctx, nexovccInput{Action: "withdraw_all", CardID: op.CardID, RequestID: fmt.Sprintf("nex-small-%d", len(amount)) + amount[:1]})
		if err != nil || result.Status != "succeeded" || writes != 0 {
			t.Fatal(result, err, writes)
		}
	}
	balance = "10.37"
	result, err := s.actionNexovccLocked(ctx, nexovccInput{Action: "withdraw_all", CardID: op.CardID, RequestID: "nex-withdraw-live1"})
	if err != nil || result.Amount != "9.37" || writes != 1 {
		t.Fatal(result, err, writes)
	}
}

func TestNexovccBatchContinuesAfterBrowserDisconnectAndSkipsClosed(t *testing.T) {
	s, _ := nexFixtureServer(t)
	base := s.nexovcc.transport
	writes := 0
	s.nexovcc.transport = func(ctx context.Context, r nexovcc.Request) (nexovcc.Response, error) {
		if r.Path == "/api/card/getUserCardBalance" {
			return nexReply(map[string]any{"balance": "1.00"}), nil
		}
		if r.Path == "/api/card/userCardWithdraw" {
			writes++
			t.Fatal("low-balance card should not withdraw")
		}
		return base(ctx, r)
	}
	opened, err := s.openNexovccLocked(context.Background(), "nex-open-batch1", "template", false)
	if err != nil {
		t.Fatal(err)
	}
	closed, err := s.store.SaveNexovccCard(nexovcc.Card{Serial: "closed-fixture", Status: "4", Balance: "0.00"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	request := httptest.NewRequest("POST", "/api/nexovcc/run", strings.NewReader(fmt.Sprintf(`{"action":"withdraw_all","card_ids":[%q,%q,%q],"request_id":"nex-batch-run1","confirm":true}`, opened.CardID, closed.ID, opened.CardID))).WithContext(ctx)
	w := httptest.NewRecorder()
	s.runNexovcc(w, request)
	cancel()
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	s.nexovcc.wg.Wait()
	if s.nexovcc.job.Status != "completed" || s.nexovcc.job.CardDone != 2 || s.nexovcc.job.CardTotal != 2 || writes != 0 {
		t.Fatal(s.nexovcc.job, writes)
	}
}

func TestNexovccCombinedCloseWaitsForReceiptThenResumesWithoutReplay(t *testing.T) {
	s, f := nexFixtureServer(t)
	ctx := context.Background()
	base := s.nexovcc.transport
	balance := int64(1000)
	withdraws, freezes := 0, 0
	receipt := false
	var withdrawTime time.Time
	s.nexovcc.transport = func(ctx context.Context, r nexovcc.Request) (nexovcc.Response, error) {
		switch r.Path {
		case "/api/card/getUserCardBalance":
			return nexReply(map[string]any{"balance": nexovcc.Money(balance)}), nil
		case "/api/card/userCardWithdraw":
			withdraws++
			balance = 100
			withdrawTime = time.Now()
			return nexReply(nil), nil
		case "/api/card/lockUserCard":
			freezes++
		case "/api/findLogWebFunds":
			rows := []any{}
			if receipt {
				rows = append(rows, map[string]any{"serial_no": "nex-test-serial", "money_id": 3, "change_type_id": 4002, "amount": "9.00", "after_balance": "109.00", "created_at": withdrawTime.Format(time.RFC3339Nano)})
			}
			return nexReply(map[string]any{"list": rows, "total": len(rows), "page": 1, "pageSize": 100}), nil
		}
		return base(ctx, r)
	}
	opened, err := s.openNexovccLocked(ctx, "nex-open-delay1", "template", false)
	if err != nil {
		t.Fatal(err)
	}
	op, err := s.actionNexovccLocked(ctx, nexovccInput{Action: "withdraw_cancel", CardID: opened.CardID, RequestID: "nex-close-delay1"})
	if err != nil || op.Step != "withdraw_waiting" || withdraws != 1 || freezes != 0 || f.cancelCalls != 0 {
		t.Fatal(op, err, withdraws, freezes)
	}
	receipt = true
	op = s.verifyNexovccOperationLocked(ctx, op)
	if op.Step != "cancel_waiting" || withdraws != 1 || freezes != 1 || f.cancelCalls != 1 {
		t.Fatal("resume replayed or skipped a step", op, withdraws, freezes, f.cancelCalls)
	}
}
