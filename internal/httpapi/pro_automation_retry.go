package httpapi

import (
	"context"
	"fmt"
	"time"

	"chatgpt-space-merge/internal/gptpay"
)

// Retry the pre-payment chain while retaining the account lock, worker slot
// and restock claim. Paid recovery deliberately remains payment-free.
func (s *Server) runProProvisioning(ctx context.Context, email string, cfg gptpay.Settings, key string, card gptpay.Card, secret gptpay.CardSecret) error {
	settings, _, _, err := s.store.ProSettings()
	if err != nil {
		return err
	}
	for {
		if err = ctx.Err(); err != nil {
			return err
		}
		if err = s.autoProUpdate(email, "login", "running", "开始 Pro 全自动流程，建立连续登录会话"); err != nil {
			return err
		}
		sessionCtx, stop := context.WithTimeout(ctx, time.Duration(cfg.SessionTimeoutMinutes)*time.Minute)
		rpc := func(method string, args map[string]any) (any, error) {
			return s.proAutoRPC(sessionCtx, email, cfg, key, card, secret, method, args)
		}
		if s.proAutoHooks != nil && s.proAutoHooks.Session != nil {
			err = s.proAutoHooks.Session(sessionCtx, email, rpc)
		} else {
			err = s.runProContinuousSession(sessionCtx, email, rpc)
		}
		stop()
		if err == nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if ctx.Value(proPaidRecoveryKey{}) != nil || ctx.Value(proRestockRecoveryKey{}) != nil {
			return err
		}
		p, _, readErr := s.store.MailAccountCredential(email)
		if readErr != nil {
			return err
		}
		if p.ProAuto.RetryAttempts >= settings.RetryCount {
			if p.ProAuto.RetryAttempts > 0 {
				return fmt.Errorf("原账号重试 %d 次后仍失败：%w", p.ProAuto.RetryAttempts, err)
			}
			return err
		}
		// Completed payment/OAuth continues through the existing recovery path.
		if p.ProAuto.Stage != "login" && p.ProAuto.Stage != "recharge" || p.ProAuto.Steps["recharge"] == "completed" {
			return err
		}
		delay := time.Duration(settings.RetryIntervalSeconds) * time.Second
		at := time.Now().Add(delay)
		message := fmt.Sprintf("原账号重试 %d/%d，%d 秒后从登录继续；上次失败：%s", p.ProAuto.RetryAttempts+1, settings.RetryCount, settings.RetryIntervalSeconds, err)
		retried, retryErr := s.store.PrepareProAutoRetry(email, p.ProAuto, settings.RetryCount, at, message)
		if retryErr != nil {
			return fmt.Errorf("%w；未自动重试：%v", err, retryErr)
		}
		s.auditProEvent(retried, "login", "pending", "pro_auto", message, map[string]any{
			"automation_id": p.ProAuto.ID, "previous_order_id": p.ProAuto.OrderID,
			"retry_attempt": retried.ProAuto.RetryAttempts, "retry_limit": settings.RetryCount, "retry_at": at,
		})
		if err = waitProAuto(ctx, time.Until(at)); err != nil {
			return err
		}
	}
}
