package httpapi

import (
	"context"
	"errors"
	"fmt"

	"chatgpt-space-merge/internal/nexovcc"
)

// Explicit manual refresh of the whole pending category, independent of UI
// search, pagination and selection. No account-wide ledger or normal-card scan.
func (s *Server) syncNexovccPendingCardsLocked(ctx context.Context) error {
	all, err := s.store.NexovccCards()
	if err != nil {
		return err
	}
	cards := make([]nexovcc.Card, 0)
	for _, card := range all {
		if card.NormalPending && card.Status != "4" {
			cards = append(cards, card)
		}
	}
	var failures []error
	for i, card := range cards {
		if err = ctx.Err(); err != nil {
			return err
		}
		current, e := s.store.NexovccCard(card.ID)
		if e != nil {
			return e
		}
		if !current.NormalPending || current.Status == "4" {
			continue
		}
		s.nexovccProgress(fmt.Sprintf("刷新正常待定卡 %d/%d · 尾号 %s · 资料与余额", i+1, len(cards), card.Last4), i, len(cards))
		_, e = s.refreshNexovccLocked(ctx, card.ID, card.Serial)
		if e != nil {
			if ctx.Err() != nil || nexovcc.RetryableHTTPStatus(e) != 0 {
				return fmt.Errorf("尾号 %s：%w", card.Last4, e)
			}
			failures = append(failures, fmt.Errorf("尾号 %s 资料或余额：%w", card.Last4, e))
		}
		s.nexovccProgress(fmt.Sprintf("刷新正常待定卡 %d/%d · 尾号 %s · 交易与资金明细", i+1, len(cards), card.Last4), i, len(cards))
		if e = s.syncNexovccCardFinanceLocked(ctx, card.ID); e != nil {
			if ctx.Err() != nil || nexovcc.RetryableHTTPStatus(e) != 0 {
				return fmt.Errorf("尾号 %s：%w", card.Last4, e)
			}
			failures = append(failures, fmt.Errorf("尾号 %s 交易明细：%w", card.Last4, e))
		}
	}
	if err = s.store.CaptureNexovccRevenue(); err != nil {
		failures = append(failures, err)
	}
	if len(failures) > 0 {
		return errors.Join(failures...)
	}
	s.nexovccProgress(fmt.Sprintf("已刷新 %d 张正常待定卡的资料、余额、交易与资金明细", len(cards)), len(cards), len(cards))
	return nil
}
