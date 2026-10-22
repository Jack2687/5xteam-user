package nexovcc

import (
	"context"
	"sync"
)

const StoppedMessage = "环球接口已停止，请点击恢复后再操作"

// Shared across client replacements. Resuming creates a new epoch and cannot
// revive requests or pipeline contexts canceled by an earlier emergency stop.
type RequestGate struct {
	mu     sync.Mutex
	epoch  context.Context
	cancel context.CancelFunc
	paused bool
}
type requestGateKey struct{}
type requestGateBinding struct {
	gate  *RequestGate
	epoch context.Context
}

func NewRequestGate(paused bool) *RequestGate {
	g := &RequestGate{}
	g.epoch, g.cancel = context.WithCancel(context.Background())
	if paused {
		g.Stop()
	}
	return g
}
func (g *RequestGate) Stop() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.paused = true
	g.cancel()
}
func (g *RequestGate) Resume() {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.paused {
		g.epoch, g.cancel = context.WithCancel(context.Background())
		g.paused = false
	}
}
func (g *RequestGate) Check(ctx context.Context) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	binding, ok := ctx.Value(requestGateKey{}).(requestGateBinding)
	if g.paused || (ok && binding.gate == g && (binding.epoch != g.epoch || binding.epoch.Err() != nil)) {
		return &Error{Message: StoppedMessage}
	}
	return nil
}
func (g *RequestGate) Bind(parent context.Context) (context.Context, func(), error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.paused || parent.Err() != nil {
		return nil, nil, &Error{Message: StoppedMessage}
	}
	if binding, ok := parent.Value(requestGateKey{}).(requestGateBinding); ok && binding.gate == g {
		if binding.epoch != g.epoch || binding.epoch.Err() != nil {
			return nil, nil, &Error{Message: StoppedMessage}
		}
		return parent, func() {}, nil
	}
	ctx, cancel := context.WithCancel(parent)
	stop := context.AfterFunc(g.epoch, cancel)
	ctx = context.WithValue(ctx, requestGateKey{}, requestGateBinding{g, g.epoch})
	return ctx, func() { stop(); cancel() }, nil
}
