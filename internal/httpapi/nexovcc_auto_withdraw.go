package httpapi

import (
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

// Called after a complete fresh sync, with both the Pro admission and issuer
// mutexes held. Withdrawal-only never freezes or deletes a card.
func (s *Server) autoWithdrawNexovccLocked(ctx context.Context) error {
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
	groups := nexovcc.Groups(finance)
	sort.Slice(cards, func(i, j int) bool {
		if cards[i].CreatedAt.Equal(cards[j].CreatedAt) {
			return cards[i].ID < cards[j].ID
		}
		return cards[i].CreatedAt.Before(cards[j].CreatedAt)
	})
	var last time.Time
	for _, card := range cards {
		if err = ctx.Err(); err != nil {
			return err
		}
		amount, e := nexovcc.Cents(card.Balance)
		if e != nil || amount <= 100 || !nexovccRecycleCandidate(card, cases, groups) || !nexovccCardUsed(card, cases, groups) {
			continue
		}
		local, _, e := s.store.GPTPayCard(card.ID)
		if e != nil {
			return e
		}
		if local.IssuerLocked || local.ActiveAccounts > 0 || local.PendingAccounts > 0 || local.AccountLimit < 1 || local.OpenedAccounts < local.AccountLimit {
			continue
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
