package httpapi

import (
	"context"
	"testing"
	"time"

	"chatgpt-space-merge/internal/nexovcc"
)

func TestNexovccWithdrawClockPersistsAndDoesNotConsumeBusySlot(t *testing.T) {
	s, _ := nexFixtureServer(t)
	cfg, _, _ := s.store.NexovccSettings()
	cfg.AutoWithdrawEnabled, cfg.AutoWithdrawIntervalSeconds = true, 120
	if _, err := s.store.SaveNexovccSettings(cfg, nexovcc.Credentials{}, false); err != nil {
		t.Fatal(err)
	}
	s.nexovcc.transport = func(context.Context, nexovcc.Request) (nexovcc.Response, error) {
		t.Fatal("no eligible card should contact supplier")
		return nexovcc.Response{}, nil
	}
	ctx := context.Background()
	s.proCardAdmissionMu.Lock()
	ran, err := s.runNexovccWithdrawCheck(ctx)
	s.proCardAdmissionMu.Unlock()
	if ran || err != nil {
		t.Fatal(ran, err)
	}
	s.nexovcc.mu.Lock()
	ran, err = s.runNexovccWithdrawCheck(ctx)
	s.nexovcc.mu.Unlock()
	if ran || err != nil {
		t.Fatal(ran, err)
	}
	state, err := s.nexovccWithdrawState(cfg)
	if err != nil || !state.LastAttempt.IsZero() {
		t.Fatal("busy slot consumed interval", state, err)
	}
	if ran, err = s.runNexovccWithdrawCheck(ctx); !ran || err != nil {
		t.Fatal(ran, err)
	}
	state, err = s.nexovccWithdrawState(cfg)
	if err != nil || state.NextCheck.Sub(state.LastSuccess) != 120*time.Second {
		t.Fatal(state, err)
	}
	restarted := &Server{store: s.store}
	if ran, err = restarted.runNexovccWithdrawCheck(ctx); ran || err != nil {
		t.Fatal("restart reset interval", ran, err)
	}
	// An independent failed attempt takes precedence over the normal interval.
	state.LastSuccess = time.Now().Add(-time.Hour)
	state.RetryAfter, state.Failures, state.Error = time.Now().Add(time.Minute), 1, "temporary timeout"
	if err = s.store.NexovccCache("withdraw_state", state); err != nil {
		t.Fatal(err)
	}
	if ran, err = restarted.runNexovccWithdrawCheck(ctx); ran || err != nil {
		t.Fatal("persisted retry bypassed", ran, err)
	}
	state.RetryAfter = time.Now().Add(-time.Second)
	if err = s.store.NexovccCache("withdraw_state", state); err != nil {
		t.Fatal(err)
	}
	cfg.Enabled = false
	if _, err = s.store.SaveNexovccSettings(cfg, nexovcc.Credentials{}, false); err != nil {
		t.Fatal(err)
	}
	if ran, err = restarted.runNexovccWithdrawCheck(ctx); ran || err != nil {
		t.Fatal("disabled issuer ran", ran, err)
	}
	cfg.Enabled = true
	if _, err = s.store.SaveNexovccSettings(cfg, nexovcc.Credentials{}, false); err != nil {
		t.Fatal(err)
	}
	if ran, err = restarted.runNexovccWithdrawCheck(ctx); !ran || err != nil {
		t.Fatal("pending check lost", ran, err)
	}
}

func TestNexovccWithdrawIntervalValidation(t *testing.T) {
	for _, seconds := range []int{0, 30, 60, 86400, -1, 29, 86401} {
		cfg := nexovcc.Defaults()
		cfg.AutoWithdrawIntervalSeconds = seconds
		normalized, err := nexovcc.Normalize(cfg)
		valid := seconds == 0 || seconds >= 30 && seconds <= 86400
		if valid != (err == nil) {
			t.Fatal(seconds, err)
		}
		if seconds == 0 && normalized.AutoWithdrawIntervalSeconds != 60 {
			t.Fatal("legacy default missing")
		}
	}
}

func TestNexovccWithdrawNoticeTracksUnresolvedResultsInsteadOfOldFailures(t *testing.T) {
	s, _ := nexFixtureServer(t)
	s.nexovcc.transport = func(context.Context, nexovcc.Request) (nexovcc.Response, error) {
		t.Fatal("reading the notice must not contact the supplier")
		return nexovcc.Response{}, nil
	}
	cfg, _, _ := s.store.NexovccSettings()
	before := nexovccWithdrawState{LastAttempt: time.Now(), LastSuccess: time.Now(), Blocked: 8}
	if err := s.store.NexovccCache("withdraw_state", before); err != nil {
		t.Fatal(err)
	}
	state, err := s.nexovccWithdrawState(cfg)
	if err != nil || state.Blocked != 0 || !state.LastAttempt.Equal(before.LastAttempt) {
		t.Fatal("stale notice survived", state, err)
	}
	ops := []nexovcc.Operation{
		{ID: "historical-failure", AutoWithdraw: true, Action: "withdraw_all", Status: "failed"},
		{ID: "manual-unknown", Action: "cancel", Status: "unknown"},
		{ID: "actual-unknown", AutoWithdraw: true, Action: "withdraw_all", Status: "unknown"},
	}
	state, err = s.nexovccWithdrawState(cfg, ops)
	if err != nil || state.Blocked != 1 {
		t.Fatal("notice mixed history or manual actions into pending withdrawals", state, err)
	}
	for _, outcome := range []string{"succeeded", "failed"} {
		ops[2].Status = outcome
		state, err = s.nexovccWithdrawState(cfg, ops)
		if err != nil || state.Blocked != 0 || !state.LastSuccess.Equal(before.LastSuccess) {
			t.Fatal("resolved notice waited for another sweep", outcome, state, err)
		}
	}
	var stored nexovccWithdrawState
	if err = s.store.NexovccCached("withdraw_state", &stored); err != nil || stored.Blocked != before.Blocked {
		t.Fatal("read changed scheduled task state", stored, err)
	}
}
