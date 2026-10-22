package httpapi

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"chatgpt-space-merge/internal/nexovcc"
)

func TestNexovccEmergencyStopPersistenceAndAllEntrypoints(t *testing.T) {
	s, _ := nexFixtureServer(t)
	s.nexovcc.transport = func(context.Context, nexovcc.Request) (nexovcc.Response, error) {
		t.Error("paused operation contacted upstream")
		return nexovcc.Response{}, nil
	}
	ctx, finish, err := s.nexovccOperationContext(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer finish()
	// Stop cannot wait for an issuer lock held by a blocked network operation.
	s.nexovcc.mu.Lock()
	done := make(chan int, 1)
	go func() { done <- payCall(t, s.controlNexovcc, "", "", map[string]any{"action": "stop"}).Code }()
	select {
	case code := <-done:
		if code != 200 {
			t.Fatal(code)
		}
	case <-time.After(time.Second):
		t.Fatal("stop waited for busy issuer")
	}
	if r := payCall(t, s.controlNexovcc, "", "", map[string]any{"action": "resume"}); r.Code != 409 {
		t.Fatal("resumed before task drained")
	}
	s.nexovcc.mu.Unlock()
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("batch context not canceled")
	}
	if err := s.nexovccBatchGap(ctx, time.Now()); err == nil {
		t.Fatal("batch gap not canceled")
	}
	if r := payCall(t, s.runNexovcc, "", "", map[string]any{"action": "balance"}); r.Code != 409 {
		t.Fatal("manual request accepted")
	}
	if _, err := s.ensureNexovccCard(context.Background()); err == nil {
		t.Fatal("Pro opening accepted")
	}
	if err := s.syncNexovccLocked(context.Background()); err == nil {
		t.Fatal("sync accepted")
	}
	if ran, err := s.runNexovccWithdrawCheck(context.Background()); ran || err != nil {
		t.Fatal("withdraw check ran", err)
	}
	s.tickNexovccMonitor(context.Background())
	fresh := &Server{store: s.store}
	if state, err := fresh.nexovccControl(); err != nil || !state.Paused {
		t.Fatal("pause not persisted", err)
	}
	if r := payCall(t, s.controlNexovcc, "", "", map[string]any{"action": "resume"}); r.Code != 200 {
		t.Fatal(r.Code)
	}
	if _, _, err := s.nexovccOperationContext(ctx); err == nil {
		t.Fatal("old batch revived")
	}
	if state, err := (&Server{store: s.store}).nexovccControl(); err != nil || state.Paused {
		t.Fatal("resume not persisted", err)
	}
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest("POST", "/api/nexovcc/control", strings.NewReader(`{"action":"stop"}`)))
	if w.Code == 200 {
		t.Fatal("unauthenticated stop allowed")
	}
}

func TestNexovccStopSubmittedOpenRetainsUnknown(t *testing.T) {
	s, f := nexFixtureServer(t)
	original := s.nexovcc.transport
	started := make(chan struct{})
	s.nexovcc.transport = func(ctx context.Context, r nexovcc.Request) (nexovcc.Response, error) {
		if r.Path != "/api/card/createUserCard" {
			return original(ctx, r)
		}
		f.opens.Add(1)
		close(started)
		<-ctx.Done()
		return nexovcc.Response{}, ctx.Err()
	}
	done := make(chan nexovcc.Operation, 1)
	go func() {
		s.nexovcc.mu.Lock()
		defer s.nexovcc.mu.Unlock()
		op, _ := s.openNexovccLocked(context.Background(), "nex-stop-open1", "template", false)
		done <- op
	}()
	<-started
	if r := payCall(t, s.controlNexovcc, "", "", map[string]any{"action": "stop"}); r.Code != 200 {
		t.Fatal(r.Code)
	}
	select {
	case op := <-done:
		if op.Status != "unknown" {
			t.Fatal("submitted open marked", op.Status)
		}
	case <-time.After(time.Second):
		t.Fatal("open did not stop")
	}
	if f.opens.Load() != 1 {
		t.Fatal("duplicate opening")
	}
}
