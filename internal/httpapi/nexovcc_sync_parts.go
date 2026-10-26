package httpapi

import (
	"context"
	"errors"
	"time"

	"chatgpt-space-merge/internal/nexovcc"
)

type nexovccSyncPart struct {
	LastAttempt time.Time `json:"last_attempt_at,omitempty"`
	LastSuccess time.Time `json:"last_success_at,omitempty"`
	RetryAfter  time.Time `json:"retry_after,omitempty"`
	NextRefresh time.Time `json:"next_refresh_at,omitempty"`
	Error       string    `json:"error,omitempty"`
	Failures    int       `json:"failures"`
}

type nexovccScheduledSyncKey struct{}

func nexovccScheduledSync(ctx context.Context) context.Context {
	return context.WithValue(ctx, nexovccScheduledSyncKey{}, true)
}
func nexovccSyncIntervals(cfg nexovcc.Settings) map[string]int {
	cfg = cfg.WithSyncDefaults()
	return map[string]int{"balance": cfg.BalanceSyncSeconds, "cards": cfg.CardSyncSeconds,
		"transactions": cfg.TransactionSyncSeconds, "account": cfg.AccountSyncSeconds, "products": cfg.ProductSyncSeconds}
}

// Each data category has a persisted clock shared by page entry and the worker.
// Legacy installations inherit their existing timestamps instead of requerying
// everything on the first visit after an upgrade.
func (s *Server) nexovccSyncParts(cfg nexovcc.Settings) (map[string]nexovccSyncPart, error) {
	parts := map[string]nexovccSyncPart{}
	if err := s.store.NexovccCached("sync_parts", &parts); err != nil {
		return nil, err
	}
	var legacy nexovccSyncState
	var finance nexovcc.Finance
	var productsAt time.Time
	missing := false
	for key := range nexovccSyncIntervals(cfg) {
		if _, ok := parts[key]; !ok {
			missing = true
		}
	}
	if missing {
		if err := s.store.NexovccCached("sync_state", &legacy); err != nil {
			return nil, err
		}
		if err := s.store.NexovccCached("finance", &finance); err != nil {
			return nil, err
		}
		if err := s.store.NexovccCached("products_at", &productsAt); err != nil {
			return nil, err
		}
	}
	for key, seconds := range nexovccSyncIntervals(cfg) {
		part, exists := parts[key]
		if !exists {
			part.LastSuccess = legacy.LastSuccess
			if part.LastSuccess.IsZero() {
				part.LastSuccess = finance.At
			}
			if key == "account" {
				part.LastSuccess = finance.AccountAt
			}
			if key == "products" {
				part.LastSuccess = productsAt
			}
		}
		// Ignore legacy exponential cooldowns. Both successful and failed reads
		// use the configured category interval; explicit refreshes bypass it.
		part.RetryAfter = time.Time{}
		if part.Error == context.Canceled.Error() {
			part.Error, part.LastAttempt = "", time.Time{}
		}
		last := part.LastSuccess
		if part.LastAttempt.After(last) {
			last = part.LastAttempt
		}
		part.NextRefresh = last.Add(time.Duration(seconds) * time.Second)
		parts[key] = part
	}
	return parts, nil
}

func (s *Server) runNexovccSyncPartLocked(ctx context.Context, key string, force bool, fn func(context.Context) error) (err error) {
	cfg, _, err := s.store.NexovccSettings()
	if err != nil {
		return err
	}
	parts, err := s.nexovccSyncParts(cfg)
	if err != nil {
		return err
	}
	part := parts[key]
	if !force && time.Now().Before(part.NextRefresh) {
		return nil
	}
	previousAttempt := part.LastAttempt
	part.LastAttempt = time.Now()
	part.RetryAfter = time.Time{}
	parts[key] = part
	if err = s.store.NexovccCache("sync_parts", parts); err != nil {
		return err
	}
	label := map[string]string{"balance": "主账户余额", "cards": "卡片资料与余额", "transactions": "卡片交易与资金流水", "account": "充值记录与每日汇总", "products": "产品 / BIN"}[key]
	s.nexovccProgress("同步"+label, 0, 1)
	err = fn(ctx)
	if err == nil {
		part.LastSuccess = time.Now()
		part.Error, part.Failures, part.RetryAfter = "", 0, time.Time{}
	} else if errors.Is(err, errNexovccReadYielded) || errors.Is(ctx.Err(), context.Canceled) {
		// Yielding a read must not delay its next turn or become an outage.
		part.LastAttempt, part.Error = previousAttempt, ""
	} else {
		part.Failures++
		part.Error = err.Error()
	}
	part.NextRefresh = time.Time{}
	parts[key] = part
	if saveErr := s.store.NexovccCache("sync_parts", parts); err == nil {
		err = saveErr
	}
	return err
}
