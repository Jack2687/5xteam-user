package httpapi

import (
	"chatgpt-space-merge/internal/gptpay"
	"chatgpt-space-merge/internal/nexovcc"
	"chatgpt-space-merge/internal/store"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestNexovccOccupancyResolutionAPI(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	card, err := st.SaveNexovccCard(nexovcc.Card{Serial: "fixture", Status: "1", Balance: "10.00"}, &nexovcc.PrivateCard{Number: "4242424242424242", CVV: "123", Month: 12, Year: 2099})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	order := gptpay.Order{ID: "legacy", Email: "deleted@example.com", CardID: card.ID, Status: "success", CreatedAt: now, UpdatedAt: now}
	snap := gptpay.Snapshot{APIKey: "private-test-key", Input: gptpay.CreateInput{CardSecret: gptpay.CardSecret{Number: "4242424242424242", Year: 2099, Month: 12, CVV: "123"}}}
	if err = st.CreateGPTPayOrder(order, snap); err != nil {
		t.Fatal(err)
	}
	s := &Server{store: st, sessions: map[string]time.Time{"test": now.Add(time.Hour)}}
	mux := http.NewServeMux()
	s.nexovccRoutes(mux)
	handler := s.authMiddleware(mux)
	in := store.EJHOccupancyResolutionInput{CardID: card.ID, Email: order.Email, ExpectedStatus: order.Status, Outcome: "success_removed", Evidence: "Verified completed merge and removal", Confirm: true}
	body, _ := json.Marshal(in)
	call := func(method, path, body string, logged bool, origin string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		if logged {
			r.AddCookie(&http.Cookie{Name: authCookieName, Value: "test"})
		}
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	path := "/api/nexovcc/occupancy/legacy/resolve"
	if w := call("POST", path, string(body), false, ""); w.Code != 401 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := call("POST", path, string(body), true, "https://unrelated.example"); w.Code != 403 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := call("POST", path, "{", true, ""); w.Code != 400 {
		t.Fatal(w.Code, w.Body.String())
	}
	w := call("GET", "/api/nexovcc/occupancy/legacy", "", true, "")
	if w.Code != 200 || strings.Contains(w.Body.String(), snap.APIKey) || strings.Contains(w.Body.String(), snap.Input.Number) {
		t.Fatal("unsafe order detail", w.Code, w.Body.String())
	}
	lock := &sync.Mutex{}
	s.proLocks.Store("account:"+order.Email, lock)
	lock.Lock()
	w = call("POST", path, string(body), true, "")
	lock.Unlock()
	if w.Code != 409 {
		t.Fatal("concurrent operation bypassed", w.Code, w.Body.String())
	}
	for i := 0; i < 2; i++ {
		w = call("POST", path, string(body), true, "")
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	got, _, err := st.GPTPayCard(card.ID)
	if err != nil || got.Available || got.ActiveAccounts != 1 || got.OpenedAccounts != 1 {
		t.Fatal(got, err)
	}
}
