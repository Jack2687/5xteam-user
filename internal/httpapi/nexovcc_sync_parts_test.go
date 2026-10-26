package httpapi

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"chatgpt-space-merge/internal/nexovcc"
)

func TestNexovccSyncIndependentIntervalsAndManualAll(t *testing.T) {
	s, _ := nexFixtureServer(t)
	ctx := context.Background()
	if err := s.syncNexovccLocked(ctx, true); err != nil {
		t.Fatal(err)
	}
	cfg, _, _ := s.store.NexovccSettings()
	cfg.BalanceSyncSeconds, cfg.CardSyncSeconds, cfg.TransactionSyncSeconds = 60, 600, 300
	cfg.AccountSyncSeconds, cfg.ProductSyncSeconds = 3600, 7200
	if _, err := s.store.SaveNexovccSettings(cfg, nexovcc.Credentials{}, false); err != nil {
		t.Fatal(err)
	}
	parts, err := s.nexovccSyncParts(cfg)
	if err != nil {
		t.Fatal(err)
	}
	baseline := time.Now().Add(-2 * time.Minute)
	for key, part := range parts {
		part.LastSuccess, part.LastAttempt = baseline, baseline
		parts[key] = part
	}
	if err = s.store.NexovccCache("sync_parts", parts); err != nil {
		t.Fatal(err)
	}
	calls := map[string]int{}
	s.nexovcc.client = nil
	base := s.nexovcc.transport
	s.nexovcc.transport = func(ctx context.Context, r nexovcc.Request) (nexovcc.Response, error) {
		calls[r.Path]++
		return base(ctx, r)
	}
	// The background worker must refresh even without pending returns or recycling.
	cases, _ := s.store.NexovccCases()
	if len(cases) != 0 || cfg.MaxOnlineCards != 0 || cfg.AutoRecycleAt != 0 || cfg.AutoWithdrawEnabled {
		t.Fatal("fixture must have no pending business")
	}
	s.tickNexovccMonitor(ctx)
	if s.nexovcc.job.Status != "completed" {
		t.Fatal(s.nexovcc.job)
	}
	delete(calls, "/api/login")
	if len(calls) != 1 || calls["/api/getBalanceInfo"] != 1 {
		t.Fatal("worker queried categories not due", calls)
	}
	after, _ := s.nexovccSyncParts(cfg)
	if !after["cards"].LastSuccess.Equal(baseline) {
		t.Fatal("skipped category marked fresh")
	}
	if message, err := (&Server{store: s.store}).nexovccSyncWait(cfg, true); err != nil || message == "" {
		t.Fatal("restart lost clocks", message, err)
	}
	calls = map[string]int{}
	s.tickNexovccMonitor(ctx)
	if len(calls) != 0 {
		t.Fatal("worker refreshed before next due time", calls)
	}
	// Shortening one setting takes effect immediately; other successful clocks stay intact.
	cfg.TransactionSyncSeconds = 60
	if _, err = s.store.SaveNexovccSettings(cfg, nexovcc.Credentials{}, false); err != nil {
		t.Fatal(err)
	}
	calls = map[string]int{}
	cfg.Enabled = false
	if _, err = s.store.SaveNexovccSettings(cfg, nexovcc.Credentials{}, false); err != nil {
		t.Fatal(err)
	}
	s.tickNexovccMonitor(ctx)
	if len(calls) != 0 {
		t.Fatal("disabled issuer made automatic requests", calls)
	}
	cfg.Enabled = true
	if _, err = s.store.SaveNexovccSettings(cfg, nexovcc.Credentials{}, false); err != nil {
		t.Fatal(err)
	}
	s.tickNexovccMonitor(ctx)
	if len(calls) != 2 || calls["/api/findLogCardTrades"] != 1 || calls["/api/findLogWebFunds"] != 1 {
		t.Fatal("worker didn't isolate ledgers", calls)
	}
	calls = map[string]int{}
	w := httptest.NewRecorder()
	s.runNexovcc(w, httptest.NewRequest("POST", "/api/nexovcc/run", strings.NewReader(`{"action":"sync"}`)))
	s.nexovcc.wg.Wait()
	if w.Code != 202 || s.nexovcc.job.Status != "completed" {
		t.Fatal(w.Code, s.nexovcc.job)
	}
	for _, path := range []string{"/api/getBalanceInfo", "/api/card/findUserCardList", "/api/card/getUserCardBalance", "/api/findLogCardTrades", "/api/findLogWebFunds", "/api/findLogRecharge", "/api/findLogReportCardDay", "/api/product/findUserProductList"} {
		if calls[path] != 1 {
			t.Fatal("manual all skipped or repeated category", path, calls)
		}
	}
}

func TestNexovccSyncFailureRetainsSuccessfulPartClocks(t *testing.T) {
	s, _ := nexFixtureServer(t)
	base := s.nexovcc.transport
	s.nexovcc.transport = func(ctx context.Context, r nexovcc.Request) (nexovcc.Response, error) {
		if r.Path == "/api/findLogCardTrades" {
			return nexovcc.Response{Status: 522}, nil
		}
		return base(ctx, r)
	}
	if err := s.syncNexovccLocked(context.Background(), true); err == nil {
		t.Fatal("failure hidden")
	}
	cfg, _, _ := s.store.NexovccSettings()
	parts, err := s.nexovccSyncParts(cfg)
	if err != nil || parts["cards"].LastSuccess.IsZero() || parts["balance"].LastSuccess.IsZero() || !parts["transactions"].LastSuccess.IsZero() || parts["transactions"].Error == "" {
		t.Fatal(parts, err)
	}
	calls := map[string]int{}
	s.nexovcc.client = nil
	s.nexovcc.transport = func(ctx context.Context, r nexovcc.Request) (nexovcc.Response, error) {
		calls[r.Path]++
		return base(ctx, r)
	}
	part := parts["transactions"]
	part.LastAttempt = time.Now().Add(-time.Hour)
	part.RetryAfter = time.Now().Add(time.Hour) // Legacy cooldown must not block the due read.
	parts["transactions"] = part
	if err = s.store.NexovccCache("sync_parts", parts); err != nil {
		t.Fatal(err)
	}
	if err = s.syncNexovccLocked(nexovccScheduledSync(context.Background())); err != nil {
		t.Fatal(err)
	}
	if calls["/api/getBalanceInfo"] != 0 || calls["/api/card/findUserCardList"] != 0 || calls["/api/findLogCardTrades"] != 1 {
		t.Fatal("retry re-ran successful categories", calls)
	}
}
