package httpapi

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"chatgpt-space-merge/internal/nexovcc"
)

// One persisted clock is shared by page entry, background monitoring and the
// capacity workflow. Reloading the browser or restarting Space cannot reset it.
type nexovccSyncState struct {
	LastAttempt time.Time `json:"last_attempt_at,omitempty"`
	LastSuccess time.Time `json:"last_success_at,omitempty"`
	RetryAfter  time.Time `json:"retry_after,omitempty"`
	NextRefresh time.Time `json:"next_refresh_at,omitempty"`
	Failures    int       `json:"failures"`
	Error       string    `json:"error,omitempty"`
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
	interval := max(time.Duration(cfg.PageRefreshSeconds)*time.Second, time.Minute)
	state.NextRefresh = state.LastSuccess.Add(interval)
	if state.LastAttempt.After(state.LastSuccess) {
		state.NextRefresh = state.LastAttempt.Add(interval)
	}
	if !state.RetryAfter.IsZero() {
		state.NextRefresh = state.RetryAfter
	}
	return state, nil
}

func (s *Server) nexovccSyncWait(cfg nexovcc.Settings, automatic bool) (string, error) {
	if automatic && !cfg.Enabled {
		return "环球自动查询已关闭，显示已保存的数据", nil
	}
	state, err := s.nexovccSyncState(cfg)
	if err != nil {
		return "", err
	}
	until := state.RetryAfter
	if automatic {
		until = state.NextRefresh
	}
	if s.nexovcc.client != nil && s.nexovcc.client.RetryAt().After(until) {
		until = s.nexovcc.client.RetryAt()
	}
	if time.Now().Before(until) {
		if state.Error != "" || !state.RetryAfter.IsZero() {
			return fmt.Sprintf("保留上次数据，约 %d 秒后重试", int(time.Until(until).Seconds())+1), nil
		}
		return "数据已更新，使用缓存", nil
	}
	return "", nil
}

func (s *Server) syncNexovccLocked(ctx context.Context, forceAccount ...bool) (err error) {
	cfg, _, err := s.store.NexovccSettings()
	if err != nil {
		return err
	}
	state, err := s.nexovccSyncState(cfg)
	if err != nil {
		return err
	}
	state.LastAttempt = time.Now()
	if err = s.store.NexovccCache("sync_state", state); err != nil {
		return err
	}
	defer func() {
		state.NextRefresh = time.Time{}
		if errors.Is(err, errNexovccReadYielded) {
			// A user operation took priority, not an upstream failure. Resume later
			// using the per-card checkpoints, without increasing the failure counter.
			state.RetryAfter = time.Now().Add(30 * time.Second)
		} else if err == nil {
			state.LastSuccess = time.Now()
			state.Error, state.Failures, state.RetryAfter = "", 0, time.Time{}
		} else {
			state.Failures++
			delay := min(30*time.Second*time.Duration(1<<min(state.Failures-1, 4)), 5*time.Minute)
			state.RetryAfter = time.Now().Add(delay)
			if s.nexovcc.client != nil && s.nexovcc.client.RetryAt().After(state.RetryAfter) {
				state.RetryAfter = s.nexovcc.client.RetryAt()
			}
			state.Error = err.Error()
			slog.Warn("环球同步失败，已安排退避", "error", state.Error, "retry_at", state.RetryAfter)
		}
		if saveErr := s.store.NexovccCache("sync_state", state); saveErr != nil && err == nil {
			err = saveErr
		}
	}()
	return s.fetchNexovccLocked(ctx, forceAccount...)
}
