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

func TestEJHExternalTransferAPI(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err = st.SaveEJHRechargeLedger("3000"); err != nil {
		t.Fatal(err)
	}
	s := &Server{store: st, sessions: map[string]time.Time{"transfer-test": time.Now().Add(time.Hour)}}
	mux := http.NewServeMux()
	s.ejhRoutes(mux)
	handler := s.authMiddleware(mux)
	for _, tc := range []struct {
		body   string
		auth   bool
		origin string
		status int
	}{
		{`{"amount":"100"}`, false, "", 401},
		{`{"amount":"100"}`, true, "https://unrelated.example", 403},
		{`{}`, true, "", 400}, {`{"amount":null}`, true, "", 400},
		{`{"amount":100}`, true, "", 400}, {`{"amount":"-1"}`, true, "", 400},
		{`{"amount":"100.001"}`, true, "", 400},
		{`{"amount":"100.5","note":"外部消费"}`, true, "", 200},
	} {
		r := httptest.NewRequest("PUT", "/api/ejh/external-transfer", strings.NewReader(tc.body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Origin", tc.origin)
		if tc.auth {
			r.AddCookie(&http.Cookie{Name: authCookieName, Value: "transfer-test"})
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != tc.status {
			t.Fatal(tc, w.Code, w.Body.String())
		}
	}
	w := httptest.NewRecorder()
	s.ejhDashboard(w, httptest.NewRequest("GET", "/api/ejh/dashboard", nil))
	var dashboard struct {
		Data struct {
			Ledger store.EJHRechargeLedger `json:"recharge_ledger"`
		} `json:"data"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &dashboard) != nil || dashboard.Data.Ledger.ExternalTransferAmount != "100.50" || dashboard.Data.Ledger.Amount != "3000.00" {
		t.Fatal(w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	s.ejhFinance(w, httptest.NewRequest("GET", "/api/ejh/finance", nil))
	var finance struct {
		Data struct {
			Transfers []store.EJHExternalTransferEvent `json:"external_transfers"`
		} `json:"data"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &finance) != nil || len(finance.Data.Transfers) != 1 || finance.Data.Transfers[0].Note != "外部消费" {
		t.Fatal(w.Code, w.Body.String())
	}
	if s.ejh.client != nil {
		t.Fatal("accounting entry called supplier")
	}
}
