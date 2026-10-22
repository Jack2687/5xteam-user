package httpapi

import (
	"chatgpt-space-merge/internal/model"
	"chatgpt-space-merge/internal/restock"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestStopProRestockCancelsOnlySelectedWorkers(t *testing.T) {
	s, st, email := gptPayFixture(t, func(http.ResponseWriter, *http.Request) { t.Error("unexpected supplier operation") })
	s.proAutoCancel()
	c := restock.DefaultConfig()
	c.Enabled, c.Mode, c.GroupID = true, "auto", 1
	c, _ = st.SaveProRestockConfig(c)
	tasks, err := st.ReserveProRestock(restock.Decision{ID: "stop-http", At: time.Now(), Version: c.Version, Suggested: 1}, "", 0)
	if err != nil || len(tasks) != 1 {
		t.Fatal(err)
	}
	task := tasks[0]
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	other, otherCancel := context.WithCancel(context.Background())
	defer otherCancel()
	s.proAutoJobs = map[string]context.CancelFunc{email: cancel, "other@example.com": otherCancel}
	s.proRestockStartJobs = map[string]context.CancelFunc{task.ID: cancel}
	if r := payCall(t, s.stopProRestockTask, "id", task.ID, map[string]any{"confirm": false}); r.Code != 400 {
		t.Fatal(r.Body.String())
	}
	if ctx.Err() != nil {
		t.Fatal("unconfirmed stop cancelled job")
	}
	if r := payCall(t, s.stopProRestockTask, "id", task.ID, map[string]any{"confirm": true}); r.Code != 200 {
		t.Fatal(r.Body.String())
	}
	if ctx.Err() == nil || other.Err() != nil {
		t.Fatal("wrong workers cancelled")
	}
	// Stopping before any workflow exists must still offer explicit manual retry.
	req := httptest.NewRequest("GET", "/", nil)
	req.SetPathValue("email", email)
	rec := httptest.NewRecorder()
	s.getProAutomation(rec, req)
	var result struct {
		Data struct {
			Retry bool `json:"restock_retry_available"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil || !result.Data.Retry {
		t.Fatal("manual retry unavailable", rec.Body.String())
	}
	// A repeated click for an old task must not cancel a newer manual flow.
	s.proAutoJobs[email] = otherCancel
	if r := payCall(t, s.stopProRestockTask, "id", task.ID, map[string]any{"confirm": true}); r.Code != 200 {
		t.Fatal(r.Body.String())
	}
	if other.Err() != nil {
		t.Fatal("idempotent stop cancelled newer work")
	}
	delete(s.proAutoJobs, email)
	delete(s.proAutoJobs, "other@example.com")
	delete(s.proRestockStartJobs, task.ID)
}

func TestStoppedRestockManualContinuationBecomesVisibleInflight(t *testing.T) {
	s, st, email := gptPayFixture(t, func(http.ResponseWriter, *http.Request) { t.Error("unexpected purchase") })
	configureProAutoFixture(t, s)
	s.proAutoCancel()
	c := restock.DefaultConfig()
	c.Enabled, c.Mode, c.GroupID = true, "auto", 1
	c, _ = st.SaveProRestockConfig(c)
	tasks, err := st.ReserveProRestock(restock.Decision{ID: "stop-resume", At: time.Now(), Version: c.Version, Suggested: 1}, "", 0)
	if err != nil || len(tasks) != 1 {
		t.Fatal(err)
	}
	st.UpdateProAccount(email, func(p *model.MailAccountProfile) {
		p.ProAuto = model.ProAutoState{ID: "paid-flow", Status: "interrupted", Steps: map[string]string{"oauth": "completed"}}
	})
	entered := make(chan struct{}, 2)
	s.proAutoHooks = &proAutomationHooks{Push: func(ctx context.Context, _ string) (model.MailAccountProfile, error) {
		entered <- struct{}{}
		<-ctx.Done()
		return model.MailAccountProfile{}, ctx.Err()
	}}
	if r := payCall(t, s.startProAutomation, "email", email, map[string]any{}); r.Code != 202 {
		t.Fatal(r.Body.String())
	}
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("worker not started")
	}
	if r := payCall(t, s.stopProRestockTask, "id", tasks[0].ID, map[string]any{"confirm": true}); r.Code != 200 {
		t.Fatal(r.Body.String())
	}
	p := waitProAutoFinished(t, s, email)
	if !p.ProAuto.StopRequested {
		t.Fatal("worker ignored stop")
	}
	if r := payCall(t, s.startProAutomation, "email", email, map[string]any{}); r.Code != 202 {
		t.Fatal(r.Body.String())
	}
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("manual continuation not started")
	}
	p, _, _ = st.MailAccountCredential(email)
	if p.ProRestockTaskID != "" || p.ProAuto.StopRequested {
		t.Fatal("continued workflow hidden behind cancelled restock")
	}
	if r := payCall(t, s.stopProRestockTask, "id", tasks[0].ID, map[string]any{"confirm": true}); r.Code != 200 {
		t.Fatal(r.Body.String())
	}
	if !s.proAutomationRunning(email) {
		t.Fatal("repeat stop killed manual continuation")
	}
	payCall(t, s.stopProAutomation, "email", email, nil)
	waitProAutoFinished(t, s, email)
}
