package httpapi

import (
	"chatgpt-space-merge/internal/cardplatform"
	"context"
	"crypto/sha256"
	"fmt"
	"sort"
	"time"

	"chatgpt-space-merge/internal/nexovcc"
	"chatgpt-space-merge/internal/store"
)

type nexovccCapacityWaitError struct{ message string }

func (e *nexovccCapacityWaitError) Error() string { return e.message }

func nexovccRecycleCandidate(card nexovcc.Card, cases []store.NexovccCase, groups []nexovcc.TradeGroup) bool {
	if !cardplatform.IsOAI(card.Platform) || card.Status == "4" || !card.Imported || card.Error != "" {
		return false
	}
	for _, c := range cases {
		if c.CardID != card.ID {
			continue
		}
		if c.PaymentStatus != "failed" && c.PaymentStatus != "success" {
			return false
		}
		if c.PaymentStatus == "success" && (c.Match == nil || !c.Match.Group.Valid || !c.Match.Group.FullyReturned || c.State != "returned") {
			return false
		}
	}
	// Include supplier payments which have no local account match as well.
	for _, g := range groups {
		if g.Serial == card.Serial && (!g.Valid || (g.Payment != "0.00" && !g.ReturnResolved())) {
			return false
		}
	}
	return true
}

// The monitor only retires used cards. A fresh, unused card may be waiting for
// Pro admission and must not be recycled just because its issuance hit a limit.
func nexovccCardUsed(card nexovcc.Card, cases []store.NexovccCase, groups []nexovcc.TradeGroup) bool {
	for _, c := range cases {
		if c.CardID == card.ID && c.PaymentStatus == "success" {
			return true
		}
	}
	for _, g := range groups {
		if g.Serial == card.Serial {
			if amount, err := nexovcc.Cents(g.Payment); err == nil && amount > 0 {
				return true
			}
		}
	}
	return false
}

func nexovccOnlineCount(cards []nexovcc.Card) int {
	count := 0
	for _, c := range cards {
		if c.Status != "4" {
			count++
		}
	}
	return count
}

func (s *Server) oldestNexovccRecycleCandidateLocked(cards []nexovcc.Card, usedOnly bool) (*nexovcc.Card, error) {
	cases, err := s.store.NexovccCases()
	if err != nil {
		return nil, err
	}
	var finance nexovcc.Finance
	if err = s.store.NexovccCached("finance", &finance); err != nil {
		return nil, err
	}
	groups, err := s.store.NexovccTradeGroups(finance)
	if err != nil {
		return nil, err
	}
	sort.Slice(cards, func(i, j int) bool {
		if cards[i].CreatedAt.Equal(cards[j].CreatedAt) {
			return cards[i].ID < cards[j].ID
		}
		if cards[i].CreatedAt.IsZero() {
			return false
		}
		if cards[j].CreatedAt.IsZero() {
			return true
		}
		return cards[i].CreatedAt.Before(cards[j].CreatedAt)
	})
	for i := range cards {
		card := cards[i]
		if !nexovccRecycleCandidate(card, cases, groups) || (usedOnly && !nexovccCardUsed(card, cases, groups)) {
			continue
		}
		local, _, e := s.store.GPTPayCard(card.ID)
		if e != nil || local.IssuerLocked || local.ActiveAccounts > 0 || local.PendingAccounts > 0 {
			continue
		}
		return &cards[i], nil
	}
	return nil, nil
}

// Caller holds proCardAdmissionMu before nexovcc.mu, sharing the same lock order
// as Pro card selection and reservation. Run only after a complete fresh sync.
func (s *Server) autoRecycleNexovccLocked(ctx context.Context) error {
	var lastCard time.Time
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		cfg, _, err := s.store.NexovccSettings()
		if err != nil {
			return err
		}
		if !cfg.Enabled || cfg.AutoRecycleAt == 0 {
			return nil
		}
		ops, err := s.store.NexovccOperations()
		if err != nil {
			return err
		}
		for _, op := range ops {
			if op.CapacityRecycle && !op.Terminal() {
				if err = s.waitNexovccCapacityCloseLocked(ctx, op); err != nil {
					return err
				}
			} else if op.CapacityRecycle && op.Status == "succeeded" {
				if err = s.nexovccGap(ctx, op.UpdatedAt, "旧卡余款已退回，等待操作间隔"); err != nil {
					return err
				}
			}
		}
		cards, err := s.store.NexovccCards()
		if err != nil {
			return err
		}
		online := nexovccOnlineCount(cards)
		if online < cfg.AutoRecycleAt {
			return nil
		}
		card, err := s.oldestNexovccRecycleCandidateLocked(cards, true)
		if err != nil {
			return err
		}
		if card == nil {
			return nil // Wait for actual refunds; do not retire unused new cards.
		}
		if !lastCard.IsZero() {
			if err = s.nexovccBatchGap(ctx, lastCard); err != nil {
				return err
			}
		}
		s.nexovccProgress(fmt.Sprintf("在线卡 %d 张达到回收值 %d，回收尾号 %s", online, cfg.AutoRecycleAt, card.Last4), 0, 4)
		// Stable across scans/restarts: an ambiguous or failed money operation
		// must never become another withdrawal merely because the ticker ran.
		hash := sha256.Sum256([]byte(card.ID))
		op, err := s.actionNexovccLocked(ctx, nexovccInput{Action: "withdraw_cancel", CardID: card.ID, RequestID: fmt.Sprintf("nex-auto-%x", hash[:10]), CapacityRecycle: true})
		if err != nil {
			return err
		}
		if err = s.waitNexovccCapacityCloseLocked(ctx, op); err != nil {
			return err
		}
		lastCard = time.Now()
	}
}

// Every entry point uses this gate while holding the issuer mutex. A pending
// close is resumed by its original ID, never replaced by another money request.
func (s *Server) ensureNexovccOnlineSlotLocked(ctx context.Context, openingID string, cfg nexovcc.Settings, checks ...func() error) error {
	// Online capacity is counted from persisted cards under the issuer lock.
	// Opening must not trigger a list/balance/history scan of the supplier.
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		for _, check := range checks {
			if err := check(); err != nil {
				return err
			}
		}
		current, _, err := s.store.NexovccSettings()
		if err != nil {
			return err
		}
		if !current.Enabled {
			return fmt.Errorf("环球卡台已停用，未开新卡")
		}
		cfg = current
		ops, err := s.store.NexovccOperations()
		if err != nil {
			return err
		}
		// Even if deletion already freed the slot, wait for its remaining
		// principal to return before a new opening can spend account funds.
		for _, op := range ops {
			if !op.CapacityRecycle {
				continue
			}
			if !op.Terminal() {
				if err = s.waitNexovccCapacityCloseLocked(ctx, op, checks...); err != nil {
					return err
				}
			} else if op.Status == "succeeded" {
				if err = s.nexovccGap(ctx, op.UpdatedAt, "旧卡余款已退回，准备开新卡"); err != nil {
					return err
				}
			}
		}
		cards, err := s.store.NexovccCards()
		if err != nil {
			return err
		}
		online := nexovccOnlineCount(cards)
		if cfg.MaxOnlineCards == 0 || online < cfg.MaxOnlineCards {
			return nil
		}
		candidate, err := s.oldestNexovccRecycleCandidateLocked(cards, false)
		if err != nil {
			return err
		}
		if candidate == nil {
			return &nexovccCapacityWaitError{fmt.Sprintf("环球在线卡 %d/%d，暂无全部回款且无占用的卡，等待回款后继续补卡", online, cfg.MaxOnlineCards)}
		}
		if err = s.syncNexovccCardFinanceLocked(ctx, candidate.ID); err != nil {
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
		if !nexovccRecycleCandidate(*candidate, cases, groups) {
			return &nexovccCapacityWaitError{"原卡仍有未回款交易，等待回款后继续补卡"}
		}
		for _, check := range checks {
			if err := check(); err != nil {
				return err
			}
		}
		s.nexovccProgress(fmt.Sprintf("在线卡 %d/%d，先回收尾号 %s：提现留 1U 并销卡", online, cfg.MaxOnlineCards, candidate.Last4), 0, 4)
		hash := sha256.Sum256([]byte(openingID + "|" + candidate.ID))
		op, err := s.actionNexovccLocked(ctx, nexovccInput{Action: "withdraw_cancel", CardID: candidate.ID, RequestID: fmt.Sprintf("nex-cap-%x", hash[:10]), CapacityRecycle: true})
		if err != nil {
			return err
		}
		if err = s.waitNexovccCapacityCloseLocked(ctx, op, checks...); err != nil {
			return err
		}
	}
}

func (s *Server) waitNexovccCapacityCloseLocked(parent context.Context, op nexovcc.Operation, checks ...func() error) error {
	ctx, cancel := context.WithTimeout(parent, 90*time.Second)
	defer cancel()
	for !op.Terminal() {
		for _, check := range checks {
			if err := check(); err != nil {
				return err
			}
		}
		if err := s.nexovccGap(ctx, time.Now(), "等待原卡销卡和余款到账"); err != nil {
			return &nexovccCapacityWaitError{"旧卡回收尚未完成，保留原操作，等待确认后继续补卡"}
		}
		var err error
		op, err = s.resolveNexovccLocked(ctx, nexovccInput{RequestID: op.ID, Outcome: "query"})
		if err != nil {
			return &nexovccCapacityWaitError{"旧卡回收等待同步：" + err.Error()}
		}
	}
	if op.Status != "succeeded" {
		return fmt.Errorf("旧卡回收失败，未开新卡：%s", op.Error)
	}
	return s.nexovccGap(ctx, time.Now(), "旧卡余款已退回，等待操作间隔")
}
