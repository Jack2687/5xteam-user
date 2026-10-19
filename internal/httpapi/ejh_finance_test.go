package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"chatgpt-space-merge/internal/store"
)

func TestEJHFinanceReadOnlyAndAuthentication(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	s := &Server{store: st, sessions: map[string]time.Time{"finance-test": time.Now().Add(time.Hour)}}
	mux := http.NewServeMux()
	s.ejhRoutes(mux)
	handler := s.authMiddleware(mux)
	for _, authorized := range []bool{false, true} {
		r := httptest.NewRequest("GET", "/api/ejh/finance", nil)
		if authorized {
			r.AddCookie(&http.Cookie{Name: authCookieName, Value: "finance-test"})
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if !authorized && w.Code != 401 || authorized && w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	snapshots, err := store.EJHRecords[store.EJHFinancialSnapshot](st, "finance_snapshot")
	if err != nil || len(snapshots) != 0 || s.ejh.client != nil {
		t.Fatal("GET mutated history or called supplier")
	}
	s.captureEJHFinance()
	w := httptest.NewRecorder()
	s.ejhFinance(w, httptest.NewRequest("GET", "/", nil))
	var result struct {
		Data struct {
			Recording ejhFinanceRecording          `json:"recording"`
			Snapshots []store.EJHFinancialSnapshot `json:"snapshots"`
		} `json:"data"`
	}
	if err = json.Unmarshal(w.Body.Bytes(), &result); err != nil || result.Data.Recording.LastSuccess == nil || len(result.Data.Snapshots) != 2 {
		t.Fatal(w.Body.String(), err)
	}
	if s.ejh.client != nil {
		t.Fatal("recorder called supplier")
	}
}
