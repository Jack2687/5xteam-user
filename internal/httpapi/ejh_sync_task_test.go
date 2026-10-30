package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func requestEJHSyncFixture(t *testing.T, s *Server, body string, ctx context.Context) ejhSyncResult {
	t.Helper()
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/ejh/sync", strings.NewReader(body)).WithContext(ctx)
	s.syncEJH(w, r)
	if w.Code != http.StatusAccepted {
		t.Fatalf("async sync: %d %s", w.Code, w.Body)
	}
	var response struct{ Data ejhSyncResult }
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	return response.Data
}

func awaitEJHSyncFixture(t *testing.T, s *Server, id string) ejhSyncResult {
	t.Helper()
	s.ejh.syncMu.Lock()
	task := s.ejh.syncJobs[id]
	s.ejh.syncMu.Unlock()
	select {
	case <-task.done:
	case <-time.After(5 * time.Second):
		t.Fatal("sync did not finish")
	}
	return s.ejhSyncSnapshot(task)
}

func TestEJHSyncTaskWaitsAndSharesAcrossClients(t *testing.T) {
	s, _ := ejhTestServer(t, 200)
	t.Cleanup(s.stopEJH)
	var calls atomic.Int32
	s.ejh.client.HTTP = ejhFixtureTransport(t, func(method string, _ map[string]any) any {
		if method != "card.list.info" {
			t.Errorf("unexpected issuer call %s", method)
		}
		calls.Add(1)
		return map[string]any{"cardInfos": []any{}}
	})
	s.ejh.mu.Lock()
	ctx, cancel := context.WithCancel(context.Background())
	first := requestEJHSyncFixture(t, s, `{"async":true}`, ctx)
	second := requestEJHSyncFixture(t, s, `{"async":true}`, context.Background())
	// The scheduled refresh joins exactly the same task as the page.
	now := time.Now()
	background, err := s.requestEJHSync(ejhRecentSyncKey(now), now.AddDate(0, 0, -90), now)
	var clients sync.WaitGroup
	ids := make(chan string, 12)
	for i := 0; i < 12; i++ {
		clients.Add(1)
		go func() {
			defer clients.Done()
			task, requestErr := s.requestEJHSync(ejhRecentSyncKey(now), now.AddDate(0, 0, -90), now)
			if requestErr != nil {
				ids <- "error"
				return
			}
			ids <- s.ejhSyncSnapshot(task).ID
		}()
	}
	clients.Wait()
	close(ids)
	for id := range ids {
		if id != first.ID {
			t.Error("concurrent clients started separate tasks")
		}
	}
	cancel()
	if err != nil || first.ID != second.ID || first.ID != background.result.ID || first.Status != "queued" || calls.Load() != 0 {
		s.ejh.mu.Unlock()
		t.Fatalf("busy requests were not coalesced: %+v %+v %v calls=%d", first, second, err, calls.Load())
	}
	s.ejh.mu.Unlock()
	result := awaitEJHSyncFixture(t, s, first.ID)
	if result.Status != "completed" || result.StartedAt == nil || result.FinishedAt == nil || calls.Load() != 1 {
		t.Fatalf("shared task failed or duplicated: %+v calls=%d", result, calls.Load())
	}
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/ejh/sync/"+first.ID, nil)
	r.SetPathValue("id", first.ID)
	s.getEJHSyncTask(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"status":"completed"`) || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal(w.Code, w.Body)
	}
}

func TestEJHSyncTaskPreservesRealErrorsAndLegacyResponse(t *testing.T) {
	s, _ := ejhTestServer(t, 200)
	t.Cleanup(s.stopEJH)
	s.ejh.client.HTTP = ejhFixtureTransport(t, func(string, map[string]any) any { return map[string]any{} })
	first := requestEJHSyncFixture(t, s, `{"async":true}`, context.Background())
	result := awaitEJHSyncFixture(t, s, first.ID)
	if result.Status != "failed" || !strings.Contains(result.Error, "cardInfos") {
		t.Fatalf("real upstream failure swallowed: %+v", result)
	}
	w := httptest.NewRecorder()
	s.syncEJH(w, httptest.NewRequest(http.MethodPost, "/api/ejh/sync", strings.NewReader(`{}`)))
	if w.Code != 502 || !strings.Contains(w.Body.String(), "cardInfos") {
		t.Fatal("legacy request did not wait for actual result", w.Code, w.Body)
	}
}

func TestEJHSyncTaskRangeIsolationQueueLimitAndShutdown(t *testing.T) {
	s, _ := ejhTestServer(t, 200)
	t.Cleanup(s.stopEJH)
	s.ejh.mu.Lock()
	defer s.ejh.mu.Unlock()
	first := requestEJHSyncFixture(t, s, `{"async":true,"start":"2026-10-01","end":"2026-10-02"}`, context.Background())
	second := requestEJHSyncFixture(t, s, `{"async":true,"start":"2026-10-01","end":"2026-10-03"}`, context.Background())
	duplicate := requestEJHSyncFixture(t, s, `{"async":true,"start":"2026-10-01","end":"2026-10-02"}`, context.Background())
	if first.ID == second.ID || first.ID != duplicate.ID {
		t.Fatal("different date ranges incorrectly shared completion")
	}
	requestEJHSyncFixture(t, s, `{"async":true,"start":"2026-10-01","end":"2026-10-04"}`, context.Background())
	requestEJHSyncFixture(t, s, `{"async":true,"start":"2026-10-01","end":"2026-10-05"}`, context.Background())
	w := httptest.NewRecorder()
	s.syncEJH(w, httptest.NewRequest(http.MethodPost, "/api/ejh/sync", strings.NewReader(`{"async":true,"start":"2026-10-01","end":"2026-10-06"}`)))
	if w.Code != 503 {
		t.Fatal("unbounded queue", w.Code)
	}
	s.stopEJH() // must interrupt queued lock waits, with no issuer operation
	result := awaitEJHSyncFixture(t, s, first.ID)
	if result.Status != "failed" || result.StartedAt != nil {
		t.Fatal("shutdown did not cancel queued work", result)
	}
	if _, err := s.requestEJHSync("new", time.Now().Add(-time.Hour), time.Now()); err == nil {
		t.Fatal("task accepted after shutdown")
	}
}

func TestEJHSyncTaskBoundedLockWaitAndMissingStatus(t *testing.T) {
	s, _ := ejhTestServer(t, 200)
	s.ejh.mu.Lock()
	err := s.performEJHSyncTask(context.Background(), &ejhSyncTask{}, 20*time.Millisecond, time.Second)
	s.ejh.mu.Unlock()
	if err == nil || !strings.Contains(err.Error(), "本次未开始同步") {
		t.Fatal("lock wait did not report timeout", err)
	}
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/ejh/sync/expired", nil)
	r.SetPathValue("id", "expired")
	s.getEJHSyncTask(w, r)
	if w.Code != 404 || !strings.Contains(w.Body.String(), "重启") {
		t.Fatal(w.Code, w.Body)
	}
}

func TestEJHSyncTaskExecutionDeadlineReleasesLock(t *testing.T) {
	s, _ := ejhTestServer(t, 200)
	s.ejh.client.HTTP = &http.Client{Transport: ejhBalanceTransport(func(r *http.Request) (*http.Response, error) {
		<-r.Context().Done()
		return nil, r.Context().Err()
	})}
	now := time.Now()
	task := &ejhSyncTask{result: ejhSyncResult{ID: "deadline", QueuedAt: now}, start: now.Add(-time.Hour), end: now}
	if err := s.performEJHSyncTask(context.Background(), task, time.Second, 20*time.Millisecond); err == nil {
		t.Fatal("hung upstream did not time out")
	}
	if !s.ejh.mu.TryLock() {
		t.Fatal("timed out sync retained issuer lock")
	}
	s.ejh.mu.Unlock()
}
