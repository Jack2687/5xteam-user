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

func TestNexovccFailedSyncKeepsFinanceAndBlocksRepeatedAttempts(t *testing.T) {
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
	for _, automatic := range []string{"true", "false", "true"} {
		w := httptest.NewRecorder()
		s.runNexovcc(w, httptest.NewRequest("POST", "/api/nexovcc/run", strings.NewReader(`{"action":"sync","automatic_refresh":`+automatic+`}`)))
		if w.Code != 200 || calls != count {
			t.Fatal("cooldown bypass", w.Code, calls, count)
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
	if err != nil || state.Failures != 1 || !state.RetryAfter.After(time.Now()) || state.Error == "" {
		t.Fatal(state, err)
	}
	restarted := &Server{store: s.store}
	if message, err := restarted.nexovccSyncWait(cfg, false); err != nil || message == "" {
		t.Fatal("restart cleared backoff", message, err)
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
