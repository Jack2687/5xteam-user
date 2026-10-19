package httpapi

import (
	"chatgpt-space-merge/internal/ejh"
	"chatgpt-space-merge/internal/gptpay"
	"chatgpt-space-merge/internal/store"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestEJHManualCaseStateAPI(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	enableEJHPaymentFixture(t, st)
	card, err := st.ImportEJHCard(ejh.Card{OrderNo: "fixture", Number: "4242424242424242", Validity: "1299", CVV: "123", Status: "1"}, true)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	order := gptpay.Order{ID: "manual", Email: "deleted@example.com", CardID: card.ID, Status: "success", CreatedAt: now, UpdatedAt: now}
	snap := gptpay.Snapshot{APIKey: "private-test-key", Input: gptpay.CreateInput{CardSecret: gptpay.CardSecret{Number: "4242424242424242", Year: 2099, Month: 12, CVV: "123"}}}
	if err = st.CreateGPTPayOrder(order, snap); err != nil {
		t.Fatal(err)
	}
	s := &Server{store: st, sessions: map[string]time.Time{"test": now.Add(time.Hour)}}
	mux := http.NewServeMux()
	s.ejhRoutes(mux)
	handler := s.authMiddleware(mux)
	body := `{"state":"returned","reason":"已核实退款到账","expected_state":"opened","expected_revision":0,"confirm":true}`
	call := func(body string, logged bool, origin string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("PUT", "/api/ejh/cases/manual/state", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		if logged {
			r.AddCookie(&http.Cookie{Name: authCookieName, Value: "test"})
		}
		r.Header.Set("Origin", origin)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	if w := call(body, false, ""); w.Code != 401 {
		t.Fatal(w.Code)
	}
	if w := call(body, true, "https://unrelated.example"); w.Code != 403 {
		t.Fatal(w.Code)
	}
	if w := call("{", true, ""); w.Code != 400 {
		t.Fatal(w.Code)
	}
	lock := &sync.Mutex{}
	s.proLocks.Store("account:"+order.Email, lock)
	lock.Lock()
	w := call(body, true, "")
	lock.Unlock()
	if w.Code != 409 {
		t.Fatal("concurrent task allowed edit", w.Code)
	}
	w = call(body, true, "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var out struct {
		Data ejh.Case `json:"data"`
	}
	if err = json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Data.State != "returned" || out.Data.StateOverride == nil || out.Data.StateOverride.Revision != 1 {
		t.Fatal(out)
	}
	if strings.Contains(w.Body.String(), snap.APIKey) || strings.Contains(w.Body.String(), snap.Input.Number) {
		t.Fatal("sensitive data in response")
	}
	if w = call(body, true, ""); w.Code != 409 {
		t.Fatal("stale repeated submit accepted", w.Code)
	}
	got, _, err := st.GPTPayCard(card.ID)
	if err != nil || !got.Available || got.OpenedAccounts != 1 {
		t.Fatal(got, err)
	}
}
