package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"chatgpt-space-merge/internal/nexovcc"
)

// Aggregate status and per-category clocks survive page reloads and restarts.
type nexovccSyncState struct {
	Parts       map[string]nexovccSyncPart `json:"parts,omitempty"`
	LastAttempt time.Time                  `json:"last_attempt_at,omitempty"`
	LastSuccess time.Time                  `json:"last_success_at,omitempty"`
	RetryAfter  time.Time                  `json:"retry_after,omitempty"`
	NextRefresh time.Time                  `json:"next_refresh_at,omitempty"`
	Failures    int                        `json:"failures"`
	Error       string                     `json:"error,omitempty"`
}

func (s *Server) nexovccSyncState(cfg nexovcc.Settings) (nexovccSyncState, error) {
	var state nexovccSyncState
	if err := s.store.NexovccCached("sync_state", &state); err != nil {
		return state, err
	}
	if state.LastAttempt.IsZero() && state.LastSuccess.IsZero() {
		var finance nexovcc.Finance
		if err := s.store.NexovccCached("finance", &finance); err != nil {
			return state, err
		}
		state.LastSuccess = finance.At
	}
	parts, err := s.nexovccSyncParts(cfg)
	if err != nil {
		return state, err
	}
	state.Parts = parts
	state.NextRefresh = time.Time{}
	for _, part := range parts {
		if state.NextRefresh.IsZero() || part.NextRefresh.Before(state.NextRefresh) {
			state.NextRefresh = part.NextRefresh
		}
	}
	state.RetryAfter = time.Time{}
	if state.Error == context.Canceled.Error() {
		state.Error = ""
	}
	return state, nil
}

func (s *Server) nexovccSyncWait(cfg nexovcc.Settings, automatic bool) (string, error) {
	if !automatic {
		return "", nil
	}
	if automatic && !cfg.Enabled {
		return "环球自动查询已关闭，显示已保存的数据", nil
	}
	state, err := s.nexovccSyncState(cfg)
	if err != nil {
		return "", err
	}
	if time.Now().Before(state.NextRefresh) {
		return "按配置间隔同步，当前显示已保存数据", nil
	}
	return "", nil
}

func (s *Server) syncNexovccLocked(ctx context.Context, forceAccount ...bool) (err error) {
	bound, finishOperation, gateErr := s.nexovccOperationContext(ctx)
	if gateErr != nil {
		return gateErr
	}
	defer finishOperation()
	ctx = bound

	cfg, _, err := s.store.NexovccSettings()
	if err != nil {
		return err
	}
	state, err := s.nexovccSyncState(cfg)
	if err != nil {
		return err
	}
	state.LastAttempt = time.Now()
	state.Parts = nil
	if err = s.store.NexovccCache("sync_state", state); err != nil {
		return err
	}
	defer func() {
		state.NextRefresh = time.Time{}
		state.Parts = nil
		state.RetryAfter = time.Time{}
		if errors.Is(err, errNexovccReadYielded) || errors.Is(ctx.Err(), context.Canceled) {
			// A user operation took priority, not an upstream failure. Resume later
			// using the per-card checkpoints, without increasing the failure counter.
			state.Error = ""
		} else if err == nil {
			state.LastSuccess = time.Now()
			state.Error, state.Failures, state.RetryAfter = "", 0, time.Time{}
		} else {
			state.Failures++
			state.Error = err.Error()
			slog.Warn("环球同步失败，后续按配置间隔执行", "error", state.Error)
		}
		if saveErr := s.store.NexovccCache("sync_state", state); saveErr != nil && err == nil {
			err = saveErr
		}
	}()
	return s.fetchNexovccLocked(ctx, forceAccount...)
}
