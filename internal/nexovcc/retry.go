package nexovcc

import (
	"context"
	"errors"
	"time"
)

// RetryableHTTPStatus excludes JSON business error codes.
func RetryableHTTPStatus(err error) int {
	var remote *Error
	if errors.As(err, &remote) && remote.Unknown && (remote.Code == 500 || remote.Code == 522) {
		return remote.Code
	}
	return 0
}

// SetHTTP522Retry retains the saved setting's name; it covers both 500 and 522.
func (c *Client) SetHTTP522Retry(count int, interval time.Duration, progress func(string)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.http522Retries = max(0, min(count, 10))
	c.http522Interval = max(time.Second, min(interval, 300*time.Second))
	c.retryProgress = progress
}

type no522RetriesKey struct{}

// WithOutcomeProbe bounds reconciliation at the operation layer, without
// multiplying each probe into another retry loop.
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
