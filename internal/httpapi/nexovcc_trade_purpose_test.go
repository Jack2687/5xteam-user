package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"chatgpt-space-merge/internal/nexovcc"
)

func TestNexovccTradePurposeAPI(t *testing.T) {
	s, _ := nexFixtureServer(t)
	s.nexovcc.transport = func(context.Context, nexovcc.Request) (nexovcc.Response, error) {
		t.Error("classification must never call issuer")
		return nexovcc.Response{}, nil
	}
	s.sessions["fixture"] = time.Now().Add(time.Hour)
	f := nexovcc.Finance{At: time.Now(), Trades: []nexovcc.Trade{{ID: "manual-payment", Serial: "card", Authorization: "auth", Currency: "USD", Amount: "46.21", MerchantCurrency: "PHP", MerchantAmount: "2900", Status: "PENDING", At: time.Now()}}}
	if err := s.store.SaveNexovccFinance(f); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	s.nexovccRoutes(mux)
	handler := s.authMiddleware(mux)
	call := func(body string, logged bool, origin string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("PUT", "/api/nexovcc/trades/manual-payment/purpose", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		if logged {
			r.AddCookie(&http.Cookie{Name: authCookieName, Value: "fixture"})
		}
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	body := `{"non_pro":true,"revision":0}`
	if w := call(body, false, ""); w.Code != 401 {
		t.Fatal(w.Code)
	}
	if w := call(body, true, "https://unrelated.example"); w.Code != 403 {
		t.Fatal(w.Code)
	}
	for _, b := range []string{`{}`, `{"non_pro":true}`, `{"revision":0}`, `{"non_pro":true,"revision":-1}`} {
		if w := call(b, true, ""); w.Code != 400 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	w := call(body, true, "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w = call(`{"non_pro":false,"revision":0}`, true, ""); w.Code != 409 {
		t.Fatal("stale request", w.Code)
	}
	w = httptest.NewRecorder()
	s.nexovccDashboard(w, httptest.NewRequest("GET", "/api/nexovcc/dashboard", nil))
	var result struct {
		Data struct {
			Groups  []nexovcc.TradeGroup `json:"groups"`
			Finance nexovcc.Finance      `json:"finance"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || w.Code != 200 || len(result.Data.Groups) != 1 || !result.Data.Groups[0].NonPro || result.Data.Finance.Trades[0].Amount != "46.21" {
		t.Fatal(w.Code, w.Body.String(), err)
	}
	card := nexovcc.Card{ID: "test", Serial: "card", Status: "1", Imported: true}
	if nexovccRecycleCandidate(card, nil, result.Data.Groups) {
		t.Fatal("non-Pro label allowed cancellation before refund")
	}
	if w = call(`{"non_pro":false,"revision":1}`, true, ""); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
}
