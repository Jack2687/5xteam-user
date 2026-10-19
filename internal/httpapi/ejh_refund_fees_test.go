package httpapi

import (
	"chatgpt-space-merge/internal/store"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestEJHRefundFeeAPI(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	s := &Server{store: st, sessions: map[string]time.Time{"refund-test": time.Now().Add(time.Hour)}}
	mux := http.NewServeMux()
	s.ejhRoutes(mux)
	h := s.authMiddleware(mux)
	good := `{"amount":"24.84","at":"2026-01-01T12:24:46+08:00","source":"balance_reconciliation","note":"4笔清算退款的余额核对"}`
	for _, test := range []struct {
		body   string
		auth   bool
		origin string
		code   int
	}{
		{good, false, "", 401}, {good, true, "https://unrelated.example", 403}, {`{}`, true, "", 400}, {good, true, "", 200}, {good, true, "", 200},
		{strings.Replace(good, "24.84", "25.00", 1), true, "", 409},
	} {
		r := httptest.NewRequest("PUT", "/api/ejh/refund-fees/window-1", strings.NewReader(test.body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Origin", test.origin)
		if test.auth {
			r.AddCookie(&http.Cookie{Name: authCookieName, Value: "refund-test"})
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != test.code {
			t.Fatal(w.Code, test.code, w.Body.String())
		}
	}
	w := httptest.NewRecorder()
	s.ejhDashboard(w, httptest.NewRequest("GET", "/api/ejh/dashboard", nil))
	var out struct {
		Data struct {
			Fees []store.EJHRefundFee `json:"refund_fees"`
		} `json:"data"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &out) != nil || len(out.Data.Fees) != 1 || out.Data.Fees[0].Amount != "24.84" {
		t.Fatal(w.Code, w.Body.String())
	}
	if s.ejh.client != nil {
		t.Fatal("recording expense called supplier")
	}
}
