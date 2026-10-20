package nexovcc

import (
	"context"
	"errors"
	"time"
)

// ActualHTTP522 excludes a local cooldown and a business response numbered 522.
func ActualHTTP522(err error) bool {
	var remote *Error
	return errors.As(err, &remote) && remote.Code == 522 && remote.Unknown
}

func (c *Client) SetHTTP522Retry(count int, interval time.Duration, progress func(string)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.http522Retries = max(0, min(count, 10))
	c.http522Interval = max(time.Second, min(interval, 300*time.Second))
	c.retryProgress = progress
}

type no522RetriesKey struct{}

// WithOutcomeProbe bounds reconciliation at the operation layer, without
// multiplying each probe into another retry loop or waiting for old cooldowns.
func WithOutcomeProbe(ctx context.Context) context.Context {
	return context.WithValue(WithImmediateRequests(ctx), no522RetriesKey{}, true)
}

func no522Retries(ctx context.Context) bool { return ctx.Value(no522RetriesKey{}) == true }

func retryableRead(path string) bool {
	switch path {
	case "/api/login", "/api/verifyGoogleCode", "/api/getBalanceInfo",
		"/api/product/findUserProductList", "/api/card/findUserCardList",
		"/api/card/getUserCardDetail", "/api/card/getUserCardBalance",
		"/api/findLogRecharge", "/api/findLogCardTrades", "/api/findLogWebFunds", "/api/findLogReportCardDay":
		return true
	}
	// Money/control writes have no documented idempotency guarantee.
	return false
}
