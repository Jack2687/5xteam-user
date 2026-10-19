package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"chatgpt-space-merge/internal/store"
)

func TestEJHRechargeLedgerAPI(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	s := &Server{store: st, sessions: map[string]time.Time{"ledger-test": time.Now().Add(time.Hour)}}
	mux := http.NewServeMux()
	s.ejhRoutes(mux)
	handler := s.authMiddleware(mux)
	call := func(body string, auth bool, origin string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("PUT", "/api/ejh/recharge-ledger", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Origin", origin)
		if auth {
			r.AddCookie(&http.Cookie{Name: authCookieName, Value: "ledger-test"})
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	for _, v := range []struct {
		body   string
		auth   bool
		origin string
		code   int
	}{
		{`{"amount":"2000.50"}`, false, "", 401},
		{`{"amount":"2000.50"}`, true, "https://unrelated.example", 403},
		{"{", true, "", 400}, {`{}`, true, "", 400}, {`{"amount":null}`, true, "", 400},
		{`{"amount":2000}`, true, "", 400}, {`{"amount":"-2"}`, true, "", 400},
		{`{"amount":"2000.50"}`, true, "", 200},
	} {
		w := call(v.body, v.auth, v.origin)
		if w.Code != v.code {
			t.Fatal(v, w.Code, w.Body.String())
		}
	}
	w := httptest.NewRecorder()
	s.ejhDashboard(w, httptest.NewRequest("GET", "/api/ejh/dashboard", nil))
	var out struct {
		Data struct {
			Ledger store.EJHRechargeLedger `json:"recharge_ledger"`
		} `json:"data"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &out) != nil || out.Data.Ledger.Amount != "2000.50" {
		t.Fatal(w.Code, w.Body.String())
	}
	if s.ejh.client != nil {
		t.Fatal("manual ledger unexpectedly called supplier")
	}
	if w = call(`{"amount":""}`, true, ""); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
}
