package httpapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"chatgpt-space-merge/internal/nexovcc"
)

func (s *Server) syncNexovccFinanceLocked(ctx context.Context) error {
	c, _, err := s.nexovccClientLocked()
	if err != nil {
		return err
	}
	step := 0
	f, err := c.ReadFinance(ctx, func(msg string) { s.nexovccProgress(msg, step, 4); step++ })
	if err == nil {
		err = s.store.CaptureNexovccRevenue()
	}
	if err == nil {
		err = s.store.SaveNexovccFinance(f)
	}
	if err != nil {
		_ = s.store.NexovccCache("finance_error", err.Error())
		return err
	}
	return s.store.NexovccCache("finance_error", "")
}

func (s *Server) nexovccCaseMatch(w http.ResponseWriter, r *http.Request) {
	var in struct {
		GroupID string `json:"group_id"`
		Reason  string `json:"reason"`
		Confirm bool   `json:"confirm"`
	}
	if decodeJSON(w, r, &in, 4096) != nil {
		return
	}
	if !in.Confirm {
		writeAPI(w, 400, nil, "请确认账号与流水的对应关系")
		return
	}
	if err := s.store.MatchNexovccCase(r.PathValue("id"), in.GroupID, in.Reason); err != nil {
		writeAPI(w, 400, nil, err.Error())
		return
	}
	writeAPI(w, 200, map[string]bool{"saved": true}, "")
}

func (s *Server) nexovccTradePurpose(w http.ResponseWriter, r *http.Request) {
	var in struct {
		NonPro   *bool `json:"non_pro"`
		Revision *int  `json:"revision"`
	}
	if decodeJSON(w, r, &in, 4096) != nil {
		return
	}
	if in.NonPro == nil || in.Revision == nil || *in.Revision < 0 {
		writeAPI(w, 400, nil, "请提供付款标记及当前版本")
		return
	}
	group, err := s.store.SetNexovccTradePurpose(r.PathValue("id"), *in.NonPro, *in.Revision)
	if err != nil {
		writeAPI(w, 409, nil, err.Error())
		return
	}
	writeAPI(w, 200, group, "")
}

// A rise in the aggregate wallet balance can be caused by another card or a
// recharge. Only the same card's uniquely identified transfer confirms arrival.
func (s *Server) nexovccTransferConfirmedLocked(ctx context.Context, op *nexovcc.Operation, kind int, amount string) bool {
	expected, err := nexovcc.Cents(amount)
	if err != nil {
		return false
	}
	if kind == 3002 && expected == 0 {
		op.Evidence = "卡台已销卡，销卡前余额为 0"
		return true
	}
	var f nexovcc.Finance
	if s.store.NexovccCached("finance", &f) != nil {
		return false
	}
	freshAfter := op.CreatedAt
	if op.StepAt.After(freshAfter) {
		freshAfter = op.StepAt
	}
	if f.At.Before(freshAfter) {
		if s.syncNexovccFinanceLocked(ctx) != nil {
			return false
		}
		if s.store.NexovccCached("finance", &f) != nil {
			return false
		}
	}
	if !s.nexovccTransferEvidenceLocked(op, kind, expected, f) {
		return false
	}
	_ = s.nexovccBalanceLocked(ctx)
	return true
}

// Match persisted ledger evidence without requiring another network request.
func (s *Server) nexovccTransferEvidenceLocked(op *nexovcc.Operation, kind int, expected int64, f nexovcc.Finance) bool {
	candidates := []nexovcc.Fund{}
	ops, err := s.store.NexovccOperations()
	if err != nil {
		return false
	}
	for _, r := range f.Funds {
		if r.Kind != kind || r.Serial != op.Serial || r.Currency != "USD" || r.At.Before(op.CreatedAt.Add(-time.Second)) || r.At.After(op.CreatedAt.Add(24*time.Hour)) {
			continue
		}
		value, err := nexovcc.SignedCents(r.Amount)
		if kind == 4001 {
			value = -value
		}
		if err != nil || value != expected {
			continue
		}
		used := false
		for _, prior := range ops {
			if prior.ID != op.ID && (prior.Evidence == "fund:"+r.ID || prior.WithdrawEvidence == "fund:"+r.ID) {
				used = true
				break
			}
		}
		if !used {
			candidates = append(candidates, r)
		}
	}
	if len(candidates) != 1 {
		return false
	}
	op.Evidence = "fund:" + candidates[0].ID
	return true
}

func (s *Server) confirmCachedNexovccCloseLocked(op nexovcc.Operation) (nexovcc.Operation, bool) {
	if op.Terminal() || !nexovccClosing(op.Action) || (op.Step != "" && op.Step != "cancel_waiting" && op.Step != "cancel_submitting") {
		return op, false
	}
	card, err := s.store.NexovccCard(op.CardID)
	if err != nil || card.Status != "4" || card.Serial != op.Serial {
		return op, false
	}
	expected, err := nexovcc.Cents(op.BeforeBalance)
	if err != nil {
		return op, false
	}
	if expected == 0 {
		op.Evidence = "卡台已销卡，销卡前余额为 0"
	} else {
		var f nexovcc.Finance
		if s.store.NexovccCached("finance", &f) != nil || f.At.Before(op.CreatedAt) || f.At.Before(op.StepAt) || !s.nexovccTransferEvidenceLocked(&op, 3002, expected, f) {
			return op, false
		}
	}
	op.Status, op.Error = "succeeded", ""
	return op, true
}

// Refresh pending returns even while the page is closed. It shares the
// operation mutex and can finish previously requested close pipelines after
// their withdrawal is confirmed. It never replays an uncertain submitted step.
func (s *Server) startNexovccMonitor() {
	ctx, cancel := context.WithCancel(context.Background())
	s.nexovcc.jobMu.Lock()
	s.nexovcc.monitorCancel = cancel
	s.nexovcc.wg.Add(1)
	s.nexovcc.jobMu.Unlock()
	go func() {
		defer s.nexovcc.wg.Done()
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			cfg, _, err := s.store.NexovccSettings()
			if err != nil || !cfg.Enabled {
				continue
			}
			cases, err := s.store.NexovccCases()
			if err != nil {
				continue
			}
			pending := cfg.AutoRecycleAt > 0 || cfg.AutoWithdrawEnabled
			for _, c := range cases {
				if c.PaymentStatus == "success" && (c.State != "returned" || c.Match == nil) {
					pending = true
					break
				}
			}
			ops, err := s.store.NexovccOperations()
			if err != nil {
				continue
			}
			for _, op := range ops {
				if !op.Terminal() {
					pending = true
				}
			}
			if !pending || !s.proCardAdmissionMu.TryLock() {
				continue
			}
			if !s.nexovcc.mu.TryLock() {
				s.proCardAdmissionMu.Unlock()
				continue
			}
			if message, err := s.nexovccSyncWait(cfg, true); err != nil || message != "" {
				s.nexovcc.mu.Unlock()
				s.proCardAdmissionMu.Unlock()
				continue
			}
			s.nexovcc.jobMu.Lock()
			if s.nexovcc.closed {
				s.nexovcc.jobMu.Unlock()
				s.nexovcc.mu.Unlock()
				s.proCardAdmissionMu.Unlock()
				return
			}
			s.nexovcc.job = nexovccJob{ID: randomRegistrationID(), Status: "running", Message: "自动同步回款与资金流水", Total: 4}
			s.nexovcc.jobMu.Unlock()
			workCtx, stop := context.WithTimeout(ctx, 10*time.Minute)
			err = s.syncNexovccLocked(workCtx)
			if err == nil {
				err = s.autoRecycleNexovccLocked(workCtx)
			}
			if err == nil {
				err = s.autoWithdrawNexovccLocked(workCtx)
			}
			stop()
			s.nexovcc.jobMu.Lock()
			s.nexovcc.job.Status = "completed"
			s.nexovcc.job.Message = "回款与资金流水已更新"
			s.nexovcc.job.Done = s.nexovcc.job.Total
			if err != nil {
				s.nexovcc.job.Status = "failed"
				s.nexovcc.job.Message = fmt.Sprint(err)
				var waiting *nexovccCapacityWaitError
				if errors.As(err, &waiting) {
					s.nexovcc.job.Status = "completed"
				}
			}
			s.nexovcc.jobMu.Unlock()
			s.nexovcc.mu.Unlock()
			s.proCardAdmissionMu.Unlock()
		}
	}()
}
