package httpapi

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"time"

	"chatgpt-space-merge/internal/nexovcc"
)

// Recover only an already-issued card. This clock is independent of bulk
// balance/ledger refreshes and survives restart through the saved operation.
func (s *Server) startNexovccImportMonitor(ctx context.Context) {
	s.nexovcc.wg.Add(1)
	go func() {
		defer s.nexovcc.wg.Done()
		ticker := time.NewTicker(3 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-ticker.C:
				if err := s.retryNexovccImport(ctx, now); err != nil && ctx.Err() == nil {
					slog.Warn("环球开卡资料补查失败", "error", err)
				}
			}
		}
	}()
}

func nexovccImportDue(op nexovcc.Operation, now time.Time) bool {
	if op.Action != "open" || op.Status != "import_pending" || op.Serial == "" {
		return false
	}
	delay := 3 * time.Second
	if now.Sub(op.CreatedAt) > 10*time.Minute {
		delay = time.Minute
	} else if now.Sub(op.CreatedAt) > 2*time.Minute {
		delay = 15 * time.Second
	}
	return !now.Before(op.UpdatedAt.Add(delay))
}

func (s *Server) retryNexovccImport(parent context.Context, now time.Time) error {
	ctx, finish, err := s.nexovccOperationContext(parent)
	if err != nil {
		return nil // Emergency stop also stops this worker.
	}
	defer finish()
	if ctx.Err() != nil {
		return nil
	}
	op, err := s.store.NexovccPendingImport()
	if err != nil || !nexovccImportDue(op, now) {
		return err
	}
	cfg, _, err := s.store.NexovccSettings()
	if err != nil || !cfg.Enabled {
		return err
	}
	card, err := s.store.NexovccCard(nexovcc.CardID(op.Serial))
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if card.NormalPending || card.Status == "4" {
		return nil
	}
	if !s.nexovcc.mu.TryLock() {
		// A newly issued card takes priority over a long read-only ledger
		// sweep. Never cancel a payment, opening, withdrawal or cancellation.
		if now.Sub(op.CreatedAt) <= 2*time.Minute {
			s.yieldNexovccRead()
		}
		return nil
	}
	defer s.nexovcc.mu.Unlock()
	// Re-read after obtaining the issuer lock: manual confirmation or the
	// opening worker may already have completed this operation.
	op, err = s.store.NexovccOperation(op.ID)
	if err != nil || !nexovccImportDue(op, now) {
		return err
	}
	card, err = s.store.NexovccCard(nexovcc.CardID(op.Serial))
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if card.NormalPending || card.Status == "4" {
		return nil
	}
	s.nexovcc.jobMu.Lock()
	closed := s.nexovcc.closed
	s.nexovcc.jobMu.Unlock()
	if closed || ctx.Err() != nil {
		return nil
	}
	if s.nexovcc.client != nil && time.Now().Before(s.nexovcc.client.RetryAt()) {
		return nil
	}
	if card.Imported {
		op.Status, op.Error = "succeeded", ""
	} else {
		work, cancel := context.WithTimeout(ctx, 45*time.Second)
		op = s.verifyNexovccOperationLocked(work, op)
		cancel()
	}
	if err := s.store.SaveNexovccOperation(op); err != nil {
		return err
	}
	s.nexovcc.jobMu.Lock()
	defer s.nexovcc.jobMu.Unlock()
	if job := &s.nexovcc.job; job.Status == "attention" && job.Operation != nil && job.Operation.ID == op.ID {
		job.Operation = &op
		job.Message = op.Error
		if op.Status == "succeeded" {
			job.Status, job.Message, job.Done = "completed", "开卡资料已补齐", job.Total
		}
	}
	return nil
}
