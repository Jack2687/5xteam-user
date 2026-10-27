package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"chatgpt-space-merge/internal/nexovcc"
)

func TestManualProStartAfterRecentOpenFailure(t *testing.T) {
	s, f, email, c := restockWindowIntegration(t, func(http.ResponseWriter, *http.Request) { t.Error("unexpected payment") })
	p, _, _, _ := s.store.ProSettings()
	p.CardProvider, p.RetryCount = "nexovcc", 0
	if _, err := s.store.SaveProSettings(p, "", ""); err != nil {
		t.Fatal(err)
	}
	c.NoRestockEnabled = false
	if _, err := s.store.SaveProRestockConfig(c); err != nil {
		t.Fatal(err)
	}
	op := nexovcc.Operation{ID: "nex-oldfailed", Action: "open", Status: "submitted", Amount: "10.00", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := s.store.BeginNexovccOperation(op); err != nil {
		t.Fatal(err)
	}
	op.Status, op.Automatic = "failed", true
	if err := s.store.SaveNexovccOperation(op); err != nil {
		t.Fatal(err)
	}
	client, _, err := s.nexovccClientLocked()
	if err != nil {
		t.Fatal(err)
	}
	client.SetRequestInterval(time.Hour)
	s.proAutoHooks = &proAutomationHooks{Session: func(context.Context, string, func(string, map[string]any) (any, error)) error {
		return errors.New("fixture stops before payment")
	}}
	pay, _, _ := s.store.GPTPaySettings()
	w := payCall(t, s.startProAutomation, "email", email, map[string]any{"provider": pay.Provider, "plan_code": pay.PlanCode, "card_provider": "nexovcc"})
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	profile := waitProAutoFinished(t, s, email)
	if profile.ProAuto.CardID == "" || f.opens.Load() != 1 {
		t.Fatal("manual card preparation did not complete", profile.ProAuto, f.opens.Load())
	}
}

func TestManualNexovccActionBypassesClientCooldown(t *testing.T) {
	s, _ := nexFixtureServer(t)
	transport := s.nexovcc.transport
	fail := true
	s.nexovcc.transport = func(ctx context.Context, r nexovcc.Request) (nexovcc.Response, error) {
		if r.Path == "/api/getBalanceInfo" && fail {
			fail = false
			return nexovcc.Response{Status: 522, RetryAfter: "120"}, nil
		}
		return transport(ctx, r)
	}
	if err := s.nexovccBalanceLocked(context.Background()); err == nil {
		t.Fatal("expected initial timeout")
	}
	w := httptest.NewRecorder()
	s.runNexovcc(w, httptest.NewRequest("POST", "/", strings.NewReader(`{"action":"balance"}`)))
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	s.nexovcc.wg.Wait()
	if s.nexovcc.job.Status != "completed" {
		t.Fatal("manual action still blocked by cooldown", s.nexovcc.job)
	}
}
