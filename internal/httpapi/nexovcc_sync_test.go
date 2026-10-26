package httpapi

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"chatgpt-space-merge/internal/nexovcc"
)

func TestNexovccPageAndBackgroundSharePersistentRefreshClock(t *testing.T) {
	s, _ := nexFixtureServer(t)
	calls := 0
	base := s.nexovcc.transport
	s.nexovcc.transport = func(ctx context.Context, r nexovcc.Request) (nexovcc.Response, error) { calls++; return base(ctx, r) }
	if err := s.syncNexovccLocked(context.Background()); err != nil {
		t.Fatal(err)
	}
	before := calls
	for i := 0; i < 3; i++ {
		w := httptest.NewRecorder()
		s.runNexovcc(w, httptest.NewRequest("POST", "/api/nexovcc/run", strings.NewReader(`{"action":"sync","automatic_refresh":true}`)))
		if w.Code != 200 || calls != before {
			t.Fatal("page reentry repeated issuer requests", w.Code, calls, before)
		}
	}
	cfg, _, _ := s.store.NexovccSettings()
	if message, err := s.nexovccSyncWait(cfg, true); err != nil || message == "" {
		t.Fatal("background ignored page refresh", message, err)
	}
	restarted := &Server{store: s.store}
	if message, err := restarted.nexovccSyncWait(cfg, true); err != nil || message == "" {
		t.Fatal("restart reset shared clock", message, err)
	}
	if message, err := s.nexovccSyncWait(cfg, false); err != nil || message != "" {
		t.Fatal("manual refresh incorrectly blocked by healthy cache", message, err)
	}
}

func TestNexovccFailedSyncKeepsFinanceAndConfiguredInterval(t *testing.T) {
	s, _ := nexFixtureServer(t)
	if err := s.syncNexovccLocked(context.Background()); err != nil {
		t.Fatal(err)
	}
	var before nexovcc.Finance
	s.store.NexovccCached("finance", &before)
	calls := 0
	s.nexovcc.client = nil
	s.nexovcc.transport = func(_ context.Context, r nexovcc.Request) (nexovcc.Response, error) {
		calls++
		if r.Path == "/api/login" {
			v := nexReply(map[string]any{"user_id": 7})
			v.Cookie = "fixture"
			return v, nil
		}
		return nexovcc.Response{Status: 200, Body: `{"code":500,"message":"请求过于频繁，稍后重试"}`}, nil
	}
	if err := s.syncNexovccLocked(context.Background()); err == nil {
		t.Fatal("rate limit accepted")
	}
	count := calls
	for _, automatic := range []string{"true", "true", "true"} {
		w := httptest.NewRecorder()
		s.runNexovcc(w, httptest.NewRequest("POST", "/api/nexovcc/run", strings.NewReader(`{"action":"sync","automatic_refresh":`+automatic+`}`)))
		if w.Code != 200 || calls != count {
			t.Fatal("configured interval bypass", w.Code, calls, count)
		}
	}
	var after nexovcc.Finance
	s.store.NexovccCached("finance", &after)
	a, _ := json.Marshal(before)
	b, _ := json.Marshal(after)
	if string(a) != string(b) {
		t.Fatal("failed sync replaced complete financial data")
	}
	cfg, _, _ := s.store.NexovccSettings()
	state, err := s.nexovccSyncState(cfg)
	if err != nil || state.Failures != 1 || !state.RetryAfter.IsZero() || state.Error == "" {
		t.Fatal(state, err)
	}
	restarted := &Server{store: s.store}
	if message, err := restarted.nexovccSyncWait(cfg, true); err != nil || message == "" {
		t.Fatal("restart lost configured interval", message, err)
	}
	if message, err := restarted.nexovccSyncWait(cfg, false); err != nil || message != "" {
		t.Fatal("explicit manual action still waits for the scheduled interval", message, err)
	}
}

func TestNexovccSyncJoinsRunningJobAndDisabledEntryStaysLocal(t *testing.T) {
	s, _ := nexFixtureServer(t)
	s.nexovcc.job = nexovccJob{ID: "original", Status: "running", Message: "同步中"}
	s.nexovcc.mu.Lock()
	w := httptest.NewRecorder()
	s.runNexovcc(w, httptest.NewRequest("POST", "/api/nexovcc/run", strings.NewReader(`{"action":"sync","automatic_refresh":true}`)))
	s.nexovcc.mu.Unlock()
	if w.Code != 202 || !strings.Contains(w.Body.String(), "original") {
		t.Fatal("duplicate did not join existing job", w.Body.String())
	}
	cfg, _, _ := s.store.NexovccSettings()
	cfg.Enabled = false
	if _, err := s.store.SaveNexovccSettings(cfg, nexovcc.Credentials{}, false); err != nil {
		t.Fatal(err)
	}
	s.nexovcc.transport = func(context.Context, nexovcc.Request) (nexovcc.Response, error) {
		t.Fatal("disabled page contacted issuer")
		return nexovcc.Response{}, nil
	}
	w = httptest.NewRecorder()
	s.runNexovcc(w, httptest.NewRequest("POST", "/api/nexovcc/run", strings.NewReader(`{"action":"sync","automatic_refresh":true}`)))
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestNexovccCancelledSyncDoesNotCreateFailureOrCooldown(t *testing.T) {
	s, _ := nexFixtureServer(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	base := s.nexovcc.transport
	interrupt := true
	s.nexovcc.transport = func(ctx context.Context, r nexovcc.Request) (nexovcc.Response, error) {
		if interrupt && r.Path == "/api/getBalanceInfo" {
			cancel()
			return nexovcc.Response{}, ctx.Err()
		}
		return base(ctx, r)
	}
	if err := s.syncNexovccLocked(ctx); err == nil {
		t.Fatal("cancelled sync reported success")
	}
	cfg, _, _ := s.store.NexovccSettings()
	state, err := s.nexovccSyncState(cfg)
	if err != nil || state.Error != "" || state.Failures != 0 || !state.RetryAfter.IsZero() || state.Parts["balance"].Error != "" || state.Parts["balance"].Failures != 0 {
		t.Fatal("cancellation was presented as an upstream failure", state, err)
	}
	if message, err := s.nexovccSyncWait(cfg, true); err != nil || message != "" {
		t.Fatal("cancelled read delayed the next scheduled attempt", message, err)
	}
	interrupt = false
	if err = s.syncNexovccLocked(nexovccScheduledSync(context.Background())); err != nil {
		t.Fatal("resume after cancellation", err)
	}
}

func TestNexovccLegacyCooldownDoesNotBlockOtherDueParts(t *testing.T) {
	s, _ := nexFixtureServer(t)
	cfg, _, _ := s.store.NexovccSettings()
	old := time.Now().Add(-24 * time.Hour)
	parts := map[string]nexovccSyncPart{}
	for key := range nexovccSyncIntervals(cfg) {
		parts[key] = nexovccSyncPart{LastAttempt: old, LastSuccess: old, RetryAfter: time.Now().Add(time.Hour), Error: "context canceled"}
	}
	if err := s.store.NexovccCache("sync_parts", parts); err != nil {
		t.Fatal(err)
	}
	if err := s.store.NexovccCache("sync_state", nexovccSyncState{Error: "context canceled", RetryAfter: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	state, err := s.nexovccSyncState(cfg)
	if err != nil || state.Error != "" || !state.RetryAfter.IsZero() || time.Now().Before(state.NextRefresh) {
		t.Fatal(state, err)
	}
	if message, err := s.nexovccSyncWait(cfg, true); err != nil || message != "" {
		t.Fatal(message, err)
	}
	// One recent failed part cannot postpone a different category that is due.
	parts["balance"] = nexovccSyncPart{LastAttempt: time.Now(), Error: "HTTP 522", Failures: 5}
	if err = s.store.NexovccCache("sync_parts", parts); err != nil {
		t.Fatal(err)
	}
	if message, err := s.nexovccSyncWait(cfg, true); err != nil || message != "" {
		t.Fatal("unrelated categories blocked", message, err)
	}
	if err = s.syncNexovccLocked(nexovccScheduledSync(context.Background())); err != nil {
		t.Fatal(err)
	}
	state, err = s.nexovccSyncState(cfg)
	if err != nil || state.Parts["cards"].LastSuccess.Before(old.Add(time.Hour)) || state.Parts["balance"].Error != "HTTP 522" {
		t.Fatal(state, err)
	}
}
