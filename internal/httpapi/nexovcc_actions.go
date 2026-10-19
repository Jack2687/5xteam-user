package httpapi

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"chatgpt-space-merge/internal/nexovcc"
)

func nexovccClosing(action string) bool { return action == "cancel" || action == "withdraw_cancel" }

// Keep the card's durable operation lock across all three external mutations.
// A submitted step is never replayed merely because its HTTP request timed out.
func (s *Server) actionNexovccLocked(ctx context.Context, in nexovccInput) (nexovcc.Operation, error) {
	var op nexovcc.Operation
	if !nexovcc.ValidRequestID(in.RequestID) {
		return op, fmt.Errorf("操作请求编号无效")
	}
	if prior, err := s.store.NexovccOperation(in.RequestID); err == nil {
		if prior.Action != in.Action || prior.CardID != in.CardID || prior.Remark != in.Remark || ((in.Action == "withdraw" || in.Action == "deposit") && prior.Amount != in.Amount) {
			return prior, fmt.Errorf("请求编号已用于其他参数")
		}
		return prior, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return op, err
	}
	client, _, err := s.nexovccClientLocked()
	if err != nil {
		return op, err
	}
	s.nexovccProgress("正在查询最新卡片状态和余额", 0, 3)
	card, err := s.refreshNexovccLocked(ctx, in.CardID, "")
	if err != nil {
		return op, err
	}
	if card.Status == "4" {
		return op, fmt.Errorf("该卡已销卡")
	}
	// Old versions left acknowledged freezes processing indefinitely. Only that
	// acknowledged state is recoverable here; unknown requests remain blocked.
	ops, err := s.store.NexovccOperations()
	if err != nil {
		return op, err
	}
	for _, old := range ops {
		if old.CardID == card.ID && old.Action == "freeze" && old.Status == "processing" {
			old.Status, old.Error, old.Evidence = "succeeded", "", "卡台已确认冻结成功"
			old.StepAt = old.UpdatedAt
			if err = s.store.SaveNexovccOperation(old); err != nil {
				return op, err
			}
		}
	}
	card, err = s.store.NexovccCard(card.ID)
	if err != nil {
		return op, err
	}
	paths := map[string]string{"deposit": "userCardDeposit", "withdraw": "userCardWithdraw", "withdraw_all": "userCardWithdraw", "freeze": "lockUserCard", "unfreeze": "unLockUserCard", "cancel": "deleteUserCard", "withdraw_cancel": "deleteUserCard", "remark": "updateCardRemark"}
	path := paths[in.Action]
	if path == "" {
		return op, fmt.Errorf("操作类型无效")
	}
	cardBalance, err := nexovcc.Cents(card.Balance)
	if err != nil {
		return op, err
	}
	if in.Action == "withdraw_all" {
		in.Amount = nexovcc.Money(max(int64(0), cardBalance-100))
	}
	body := map[string]any{"serial_no": card.Serial}
	if in.Action == "deposit" || in.Action == "withdraw" || in.Action == "withdraw_all" {
		amount, e := nexovcc.Cents(in.Amount)
		if e != nil || (amount <= 0 && in.Action != "withdraw_all") {
			return op, fmt.Errorf("请输入有效金额")
		}
		if in.Action != "deposit" && amount > max(int64(0), cardBalance-100) {
			return op, fmt.Errorf("提现后须至少保留 1 USD")
		}
		body["amount"], body["money_id"] = in.Amount, 3
	}
	if in.Action == "cancel" && cardBalance > 100 {
		return op, fmt.Errorf("余额超过 1 USD，请选择“提现留 1U 并销卡”或先提现")
	}
	if in.Action == "remark" {
		if len([]rune(in.Remark)) > 40 {
			return op, fmt.Errorf("备注不能超过 40 字符")
		}
		body["remark"] = in.Remark
	}
	balance, err := client.Balance(ctx)
	if err != nil {
		return op, err
	}
	if err = s.store.NexovccCache("balance", balance); err != nil {
		return op, err
	}
	op = nexovcc.Operation{ID: in.RequestID, Action: in.Action, CardID: card.ID, Serial: card.Serial, Amount: in.Amount, Remark: in.Remark, Status: "submitted", BeforeBalance: card.Balance, BeforeAccountBalance: balance.Amount, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	op.CapacityRecycle = in.CapacityRecycle
	op.AutoWithdraw = in.AutoWithdraw
	if nexovccClosing(in.Action) {
		op.Step = "ready"
		if in.Action == "withdraw_cancel" {
			op.WithdrawAmount = nexovcc.Money(max(int64(0), cardBalance-100))
			op.Amount = op.WithdrawAmount
		}
	}
	if err = s.store.BeginNexovccOperation(op); err != nil {
		return op, err
	}
	if nexovccClosing(in.Action) {
		return s.advanceNexovccCloseLocked(ctx, op)
	}
	if in.Action == "withdraw_all" && in.Amount == "0.00" {
		op.Status, op.Evidence = "succeeded", "余额不超过 1 USD，无需提现"
		return op, s.store.SaveNexovccOperation(op)
	}
	if in.Action == "freeze" && card.Control == "frozen" {
		op.Status, op.Evidence, op.StepAt = "succeeded", "卡片已冻结", card.ControlAt
		return op, s.store.SaveNexovccOperation(op)
	}
	s.nexovccProgress("正在"+nexovccActionName(in.Action), 1, 3)
	_, err = client.Do(ctx, "/api/card/"+path, body)
	op.StepAt = time.Now()
	if err != nil {
		return s.nexovccActionError(op, err)
	}
	op.Status, op.Error = "processing", "卡台已受理，正在同步结果"
	if in.Action == "freeze" || in.Action == "unfreeze" {
		op.Status, op.Error, op.Evidence = "succeeded", "", "卡台已确认"+nexovccActionName(in.Action)+"成功"
	}
	if err = s.store.SaveNexovccOperation(op); err != nil {
		return op, err
	}
	if !op.Terminal() {
		s.nexovccProgress("正在核实资金到账", 2, 3)
		op = s.verifyNexovccOperationLocked(ctx, op)
		err = s.store.SaveNexovccOperation(op)
	}
	return op, err
}

func nexovccActionName(action string) string {
	return map[string]string{"deposit": "充值", "withdraw": "提现", "withdraw_all": "提现留 1U", "freeze": "冻结", "unfreeze": "解冻", "cancel": "销卡", "withdraw_cancel": "提现留 1U 并销卡", "remark": "修改备注"}[action]
}

func (s *Server) nexovccActionError(op nexovcc.Operation, err error) (nexovcc.Operation, error) {
	op.Status, op.Error = "unknown", err.Error()
	if !nexovcc.IsUnknown(err) {
		op.Status = "failed"
	}
	return op, s.store.SaveNexovccOperation(op)
}

func (s *Server) nexovccGap(ctx context.Context, after time.Time, message string) error {
	return s.nexovccWaitGap(ctx, after, 3*time.Second, message)
}

func (s *Server) nexovccBatchGap(ctx context.Context, after time.Time) error {
	cfg, _, err := s.store.NexovccSettings()
	if err != nil {
		return err
	}
	seconds := cfg.BatchIntervalSeconds
	if seconds < 1 {
		seconds = 3
	}
	return s.nexovccWaitGap(ctx, after, time.Duration(seconds)*time.Second, "准备处理下一张卡")
}

func (s *Server) nexovccWaitGap(ctx context.Context, after time.Time, gap time.Duration, message string) error {
	for remaining := time.Until(after.Add(gap)); remaining > 0; remaining = time.Until(after.Add(gap)) {
		s.nexovccProgress(fmt.Sprintf("%s · 等待 %d 秒", message, int(remaining.Seconds())+1), 1, 3)
		t := time.NewTimer(min(remaining, time.Second))
		select {
		case <-ctx.Done():
			t.Stop()
			return ctx.Err()
		case <-t.C:
		}
	}
	return nil
}

func (s *Server) advanceNexovccCloseLocked(ctx context.Context, op nexovcc.Operation) (nexovcc.Operation, error) {
	client, _, err := s.nexovccClientLocked()
	if err != nil {
		return op, err
	}
	// Always save the about-to-submit state before a mutation. A process crash
	// at that boundary leaves a queryable record, never a fresh retry.
	send := func(step, path string, body map[string]any) error {
		op.Step, op.StepAt, op.Status = step, time.Now(), "submitted"
		if e := s.store.SaveNexovccOperation(op); e != nil {
			return e
		}
		_, e := client.Do(ctx, path, body)
		return e
	}
	if op.Step == "ready" && op.Action == "withdraw_cancel" && op.WithdrawAmount != "0.00" {
		s.nexovccProgress("1/3 正在提现，保留 1 USD", 0, 3)
		if err = send("withdraw_submitting", "/api/card/userCardWithdraw", map[string]any{"serial_no": op.Serial, "amount": op.WithdrawAmount, "money_id": 3}); err != nil {
			return s.nexovccActionError(op, err)
		}
		op.Step, op.StepAt, op.Status, op.Error = "withdraw_waiting", time.Now(), "processing", "提现已受理，正在核实到账"
		if err = s.store.SaveNexovccOperation(op); err != nil {
			return op, err
		}
		if err = s.nexovccGap(ctx, op.StepAt, "提现已受理"); err != nil {
			return op, err
		}
	}
	if op.Step == "withdraw_waiting" || op.Step == "withdraw_submitting" {
		// A withdrawal is confirmed using the same card's unique ledger credit.
		// Only after confirmation may the next, different operation be submitted.
		confirmed := false
		for attempt := 0; attempt < 4; attempt++ {
			s.nexovccProgress("1/3 正在核实提现到账", 0, 3)
			if err = s.syncNexovccFinanceLocked(ctx); err != nil {
				op.Error = "提现已提交，等待资金流水同步：" + err.Error()
				break
			}
			probe := op
			if s.nexovccTransferConfirmedLocked(ctx, &probe, 4002, op.WithdrawAmount) {
				op.WithdrawEvidence, op.Step, op.Status, op.Error = probe.Evidence, "withdrawn", "processing", ""
				confirmed = true
				break
			}
			op.Error = "提现到账待同步，到账后自动继续冻结和销卡"
			if attempt < 3 {
				if err = s.nexovccGap(ctx, time.Now(), "等待提现到账"); err != nil {
					break
				}
			}
		}
		if err = s.store.SaveNexovccOperation(op); err != nil || !confirmed {
			return op, err
		}
	}
	if op.Step == "ready" || op.Step == "withdrawn" {
		if err = s.nexovccGap(ctx, op.StepAt, "准备冻结"); err != nil {
			return op, err
		}
		card, e := s.refreshNexovccLocked(ctx, op.CardID, op.Serial)
		if e != nil {
			op.Error = "正在等待卡片余额同步：" + e.Error()
			op.Status = "processing"
			return op, s.store.SaveNexovccOperation(op)
		}
		balance, e := nexovcc.Cents(card.Balance)
		if e != nil || balance > 100 {
			op.Status, op.Error = "processing", "提现后的卡余额仍待同步，稍后自动继续"
			return op, s.store.SaveNexovccOperation(op)
		}
		op.BeforeBalance = card.Balance
		if card.Control == "frozen" {
			op.FrozenAt = card.ControlAt
		} else {
			s.nexovccProgress("2/3 正在冻结卡片", 1, 3)
			if err = send("freeze_submitting", "/api/card/lockUserCard", map[string]any{"serial_no": op.Serial}); err != nil {
				return s.nexovccActionError(op, err)
			}
			op.FrozenAt = time.Now()
		}
		op.Step, op.StepAt, op.Status, op.Error = "frozen", op.FrozenAt, "processing", "冻结成功，等待销卡"
		if err = s.store.SaveNexovccOperation(op); err != nil {
			return op, err
		}
	}
	if op.Step == "frozen" {
		if err = s.nexovccGap(ctx, op.FrozenAt, "冻结成功"); err != nil {
			return op, err
		}
		s.nexovccProgress("3/3 正在销卡", 2, 3)
		if err = send("cancel_submitting", "/api/card/deleteUserCard", map[string]any{"serial_no": op.Serial}); err != nil {
			return s.nexovccActionError(op, err)
		}
		op.Step, op.StepAt, op.Status, op.Error = "cancel_waiting", time.Now(), "processing", "销卡已受理，正在核实剩余余额退回"
		if err = s.store.SaveNexovccOperation(op); err != nil {
			return op, err
		}
	}
	if strings.HasPrefix(op.Step, "cancel_") {
		op = s.verifyNexovccOperationLocked(ctx, op)
		return op, s.store.SaveNexovccOperation(op)
	}
	return op, nil
}
