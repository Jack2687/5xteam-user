package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"chatgpt-space-merge/internal/model"
	"chatgpt-space-merge/internal/restock"
)

func TestProRestockPublishBeforeEvaluation(t *testing.T) {
	s, st, _ := gptPayFixture(t, func(http.ResponseWriter, *http.Request) { t.Error("unexpected supplier call") })
	configureProAutoFixture(t, s)
	s.proAutoCancel()
	c := restock.DefaultConfig()
	c.GroupID = 1
	if _, err := st.SaveProRestockConfig(c); err != nil {
		t.Fatal(err)
	}
	p, _, _, _ := st.ProSettings()
	previous := restock.Snapshot{ID: "evaluated", At: time.Now().Add(-time.Minute), GroupID: 1, Binding: restockBinding(p), Valid: true, LoadValid: true}
	s.proRestock.lastSnapshot = previous
	s.proRestock.decision = restock.Decision{ID: "previous-decision", Snapshot: previous}
	s.proRestock.confirmation = 1
	sample := previous
	sample.ID, sample.At = "collected", time.Now().Add(-time.Second)
	sample.Accounts = []restock.Account{{ID: 1, Capacity: 10, Used: 5, Ready: true}}
	s.beginRestockCollection(time.Now().Add(-2 * time.Second))
	s.publishRestockCollection(sample)
	assertResponse := func(failed bool) {
		t.Helper()
		rec := httptest.NewRecorder()
		s.getProRestock(rec, httptest.NewRequest(http.MethodGet, "/api/pro-restock", nil))
		var body struct {
			Data struct {
				Snapshot restock.Snapshot       `json:"snapshot"`
				Decision restock.Decision       `json:"decision"`
				Monitor  proRestockMonitorState `json:"monitor"`
			} `json:"data"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || rec.Code != 200 {
			t.Fatalf("state: %d %s %v", rec.Code, rec.Body.String(), err)
		}
		if body.Data.Snapshot.ID != sample.ID || !body.Data.Snapshot.At.Equal(sample.At) || body.Data.Decision.Snapshot.ID != previous.ID {
			t.Fatalf("collection not published independently of evaluation: %+v", body.Data)
		}
		if !failed && (!body.Data.Monitor.Running || body.Data.Monitor.Phase != "reconciling") {
			t.Fatal("missing processing status")
		}
		if failed && (body.Data.Monitor.Running || body.Data.Monitor.Error != "reconciliation failed") {
			t.Fatal("processing failure not visible")
		}
	}
	assertResponse(false)
	if s.proRestock.lastSnapshot.ID != previous.ID || s.proRestock.confirmation != 1 {
		t.Fatal("publishing a sample altered decision history or pressure confirmations")
	}
	s.finishRestockCollection(time.Now(), time.Now().Add(5*time.Second), errors.New("reconciliation failed"))
	assertResponse(true)
}

func TestProRestockStaleSampleClearsPressure(t *testing.T) {
	s, st, _ := gptPayFixture(t, func(http.ResponseWriter, *http.Request) { t.Error("unexpected supplier call") })
	configureProAutoFixture(t, s)
	s.proAutoCancel()
	c := restock.DefaultConfig()
	c.Enabled, c.GroupID, c.Mode, c.TimeEnabled = true, 1, "observe", false
	if _, err := st.SaveProRestockConfig(c); err != nil {
		t.Fatal(err)
	}
	p, _, _, _ := st.ProSettings()
	pay, _, _ := st.GPTPaySettings()
	stale := false
	s.proRestock.monitor = func(context.Context, restock.Config, model.ProSettings, string) (restock.Snapshot, error) {
		at := time.Now()
		if stale {
			at = at.Add(-time.Minute)
		}
		return restock.Snapshot{At: at, Valid: true, LoadValid: true, Binding: restockBinding(p), GroupID: 1, NewPlan: restockPlan(pay.Provider, pay.PlanCode, pay.Country, pay.CreditQuantity), Accounts: []restock.Account{{ID: 1, Capacity: 10, Used: 10, Ready: true}}}, nil
	}
	for _, want := range []int{1, 0, 1} {
		if err := s.tickProRestock(context.Background(), true); err != nil {
			t.Fatal(err)
		}
		if s.proRestock.confirmation != want {
			t.Fatalf("confirmation=%d, want=%d", s.proRestock.confirmation, want)
		}
		if stale && (s.proRestock.decision.DemandKnown || s.proRestock.decision.Started != 0 || s.proRestock.decision.Suggested != 0) {
			t.Fatal("stale sample allowed new work")
		}
		stale = !stale
	}
}

func TestProRestockCadence(t *testing.T) {
	start := time.Now()
	for _, tc := range []struct {
		elapsed time.Duration
		success bool
		want    time.Duration
	}{{time.Second, true, 5 * time.Second}, {8 * time.Second, true, 8 * time.Second}, {8 * time.Second, false, 13 * time.Second}} {
		got := nextRestockCheck(start, start.Add(tc.elapsed), 5, tc.success)
		if !got.Equal(start.Add(tc.want)) {
			t.Fatalf("elapsed=%s success=%t got=%s want=%s", tc.elapsed, tc.success, got.Sub(start), tc.want)
		}
	}
}
