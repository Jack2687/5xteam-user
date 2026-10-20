package httpapi

import (
	"context"
	"fmt"
	"time"

	"chatgpt-space-merge/internal/nexovcc"
)

type nexovccRecoveryKey struct{}

// An HTTP 500/522 does not prove a money request failed. Reconcile the saved
// operation instead of replaying a debit without an upstream idempotency key.
func (s *Server) recoverNexovccHTTPErrorLocked(ctx context.Context, op nexovcc.Operation, code int) (nexovcc.Operation, error) {
	if ctx.Value(nexovccRecoveryKey{}) == true {
		return op, nil
	}
	cfg, _, err := s.store.NexovccSettings()
	if err != nil || cfg.HTTP522Retries == 0 {
		return op, err
	}
	ctx = context.WithValue(nexovcc.WithOutcomeProbe(ctx), nexovccRecoveryKey{}, true)
	for attempt := 1; attempt <= cfg.HTTP522Retries; attempt++ {
		s.nexovccProgress(fmt.Sprintf("HTTP %d · %d 秒后查询原操作结果 %d/%d", code, cfg.HTTP522RetrySeconds, attempt, cfg.HTTP522Retries), attempt-1, cfg.HTTP522Retries)
		timer := time.NewTimer(time.Duration(cfg.HTTP522RetrySeconds) * time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return op, ctx.Err()
		case <-timer.C:
		}
		updated, queryErr := s.resolveNexovccLocked(ctx, nexovccInput{RequestID: op.ID, Outcome: "query"})
		if updated.ID != "" {
			op = updated
		}
		if queryErr != nil {
			op.Error = queryErr.Error()
		}
		if op.Terminal() {
			return op, queryErr
		}
		if ctx.Err() != nil {
			return op, ctx.Err()
		}
	}
	op.Error = fmt.Sprintf("HTTP %d 后已查询原结果 %d 次，仍未确认，已停止本次请求：%s", code, cfg.HTTP522Retries, op.Error)
	return op, s.store.SaveNexovccOperation(op)
}
