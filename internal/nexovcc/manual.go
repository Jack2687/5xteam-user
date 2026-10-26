package nexovcc

import "context"

type immediateRequestsKey struct{}

// WithImmediateRequests bypasses local pacing for an explicit user
// action. It does not bypass operation deduplication or replay uncertain writes.
func WithImmediateRequests(ctx context.Context) context.Context {
	return context.WithValue(ctx, immediateRequestsKey{}, true)
}

func ImmediateRequests(ctx context.Context) bool {
	return ctx.Value(immediateRequestsKey{}) == true
}
