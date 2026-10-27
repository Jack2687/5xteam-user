package httpapi

import (
	"chatgpt-space-merge/internal/cardplatform"
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"chatgpt-space-merge/internal/nexovcc"
)

// Stable for the same refund/funding cycle, including after restarts. Our own
// withdrawals and polling timestamps never generate another money request.
func nexovccAutoWithdrawID(card nexovcc.Card, f nexovcc.Finance) string {
	parts := []string{card.ID}
	for _, g := range nexovcc.Groups(f) {
		if g.Serial == card.Serial {
			parts = append(parts, g.ID+":"+g.Returned+":"+g.ReturnedAt.UTC().Format(time.RFC3339Nano))
		}
	}
	for _, fund := range f.Funds {
		if fund.Serial == card.Serial && (fund.Kind == 4001 || fund.Kind == 4004) {
			parts = append(parts, fmt.Sprintf("%d:%s:%s", fund.Kind, fund.ID, fund.Amount))
		}
	}
	sort.Strings(parts)
	h := sha256.Sum256([]byte(strings.Join(parts, "|")))
	return fmt.Sprintf("nex-wd-%x", h[:10])
}

// Runs on its own persisted schedule with both Pro admission and issuer mutexes
// held. Select structural candidates locally, refresh stale same-card evidence,
// then recheck live balance and admission before submitting. Withdrawal-only never freezes or deletes a card.
func (s *Server) autoWithdrawNexovccLocked(ctx context.Context, onlyCard ...string) error {
	cfg, _, err := s.store.NexovccSettings()
	if err != nil || !cfg.Enabled || !cfg.AutoWithdrawEnabled {
		return err
	}
	cards, err := s.store.NexovccCards()
	if err != nil {
		return err
	}
	cases, err := s.store.NexovccCases()
	if err != nil {
		return err
	}
	var finance nexovcc.Finance
	if err = s.store.NexovccCached("finance", &finance); err != nil {
		return err
	}
	groups, err := s.store.NexovccTradeGroups(finance)
	if err != nil {
		return err
	}
	ops, err := s.store.NexovccOperations()
	if err != nil {
		return err
	}
	waiting := nexovccWaitingReturnCards(cases)
	sort.Slice(cards, func(i, j int) bool {
		if waiting[cards[i].ID] != waiting[cards[j].ID] {
			return waiting[cards[i].ID]
		}
		if cards[i].CreatedAt.Equal(cards[j].CreatedAt) {
			return cards[i].ID < cards[j].ID
		}
		return cards[i].CreatedAt.Before(cards[j].CreatedAt)
	})
	var last time.Time
	for _, card := range cards {
		if len(onlyCard) > 0 && card.ID != onlyCard[0] {
			continue
		}
		if err = ctx.Err(); err != nil {
			return err
		}
		if !cardplatform.IsOAI(card.Platform) || card.NormalPending || card.Status != "1" || card.Control == "frozen" || !card.Imported || !nexovccCardUsed(card, cases, groups) {
			continue
		}
		if nexovccWithdrawnToReserve(card, ops) {
			continue
		}
		local, _, e := s.store.GPTPayCard(card.ID)
		if e != nil {
			return e
		}
		if (local.IssuerLocked && card.Error == "") || local.PendingAccounts > 0 || local.AccountLimit < 1 || local.OpenedAccounts < local.AccountLimit {
			continue
		}
		// A previous read error may be repaired, but never touch a card whose money
		// operation is still unresolved.
		pendingOperation := false
		for _, op := range ops {
			if op.CardID == card.ID && !op.Terminal() {
				pendingOperation = true
			}
		}
		if pendingOperation {
			continue
		}
		cycle, withdrawn := nexovcc.WithdrawFundingCycle(card, finance), false
		for _, op := range ops {
			if op.CardID == card.ID && op.BalanceWithdrawal != nil && op.BalanceWithdrawal.Cycle == cycle {
				withdrawn = true
			}
		}
		if withdrawn {
			continue // Late refund rows cannot withdraw the same recovered funds again.
		}
		// Only the normal category participates in automatic withdrawals.
		// A recent successful ordinary sync can supply this evidence too.
		freshAfter := time.Now().Add(-time.Duration(max(cfg.AutoWithdrawIntervalSeconds, 30)) * time.Second)
		s.nexovccProgress("自动提现检查 · 更新尾号 "+card.Last4+" 余额与流水", 0, 3)
		if card.BalanceAt.Before(freshAfter) || finance.CardSyncedAt(card.Serial).Before(freshAfter) || card.Error != "" {
			if e = s.syncNexovccCardSnapshotLocked(ctx, card.ID); e != nil {
				return fmt.Errorf("提现前更新尾号 %s 余额与流水：%w", card.Last4, e)
			}
			card, e = s.store.NexovccCard(card.ID)
			if e != nil {
				return e
			}
			if e = s.store.NexovccCached("finance", &finance); e != nil {
				return e
			}
			cases, e = s.store.NexovccCases()
			if e != nil {
				return e
			}
			groups, e = s.store.NexovccTradeGroups(finance)
			if e != nil {
				return e
			}
		}
		amount, e := nexovcc.Cents(card.Balance)
		if e != nil || amount <= 100 || card.NormalPending || card.Status != "1" || card.Error != "" {
			continue
		}
		local, _, e = s.store.GPTPayCard(card.ID)
		if e != nil {
			return e
		}
		if local.IssuerLocked || local.PendingAccounts > 0 || local.AccountLimit < 1 || local.OpenedAccounts < local.AccountLimit {
			continue
		}
		if local.ActiveAccounts > 0 || !s.nexovccAccountingRecycleCandidate(card, cases, groups) {
			evidence, err := s.store.NexovccBalanceWithdrawal(card.ID)
			if err != nil {
				return err
			}
			if evidence == nil {
				continue
			}
		}
		id := nexovccAutoWithdrawID(card, finance)
		if _, e = s.store.NexovccOperation(id); e == nil {
			continue // Includes failures/unknowns: retry the original operation manually.
		} else if !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		if !last.IsZero() {
			if err = s.nexovccBatchGap(ctx, last); err != nil {
				return err
			}
		}
		s.nexovccProgress("自动提现留 1U · 尾号 "+card.Last4, 0, 3)
		op, e := s.actionNexovccLocked(ctx, nexovccInput{Action: "withdraw_all", CardID: card.ID, RequestID: id, AutoWithdraw: true})
		if e != nil {
			return e
		}
		last = time.Now()
		if op.Status == "unknown" || op.Status == "failed" {
			return fmt.Errorf("自动提现已停止：%s", op.Error)
		}
	}
	return nil
}
