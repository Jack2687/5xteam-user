package httpapi

import (
	"context"
	"fmt"
	"strings"
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
		if p.IsProDead() {
			return errProAccountDead
		}
		if isProProxyTransportFailure(err) && p.ProAuto.Stage == "login" && p.ProAuto.OrderID == "" && p.ProAuto.Steps["recharge"] != "completed" {
			const limit = 3
			if p.ProAuto.ProxyRetryAttempts >= limit {
				return fmt.Errorf("代理隧道连续不可用，已更换会话重试 %d 次，尚未付款：%w", limit, err)
			}
			delay := time.Duration(5*(1<<p.ProAuto.ProxyRetryAttempts)) * time.Second
			at := time.Now().Add(delay)
			message := fmt.Sprintf("代理隧道失败，%d 秒后更换代理会话重试 %d/%d；保留原账号和原卡，不占账号业务重试次数", int(delay.Seconds()), p.ProAuto.ProxyRetryAttempts+1, limit)
			retried, e := s.store.PrepareProAutoProxyRetry(email, p.ProAuto, limit, at, message)
			if e != nil {
				return fmt.Errorf("%w；代理重试未执行：%v", err, e)
			}
			s.auditProEvent(retried, "login", "pending", "pro_auto", message, map[string]any{"proxy_error": redactSensitiveText(err.Error())})
			if e = waitProAuto(ctx, time.Until(at)); e != nil {
				return e
			}
			continue
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

// Only connection establishment failures qualify; never replay a payment RPC.
func isProProxyTransportFailure(err error) bool {
	if err == nil {
		return false
	}
	m := strings.ToLower(err.Error())
	return strings.Contains(m, "connect tunnel failed") || strings.Contains(m, "proxy connect aborted") || strings.Contains(m, "could not resolve proxy") || strings.Contains(m, "failed to connect to proxy")
}
