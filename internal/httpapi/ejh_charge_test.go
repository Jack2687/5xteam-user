package httpapi

import (
	"chatgpt-space-merge/internal/ejh"
	"chatgpt-space-merge/internal/gptpay"
	"chatgpt-space-merge/internal/store"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestEJHChargeAPI(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	enableEJHPaymentFixture(t, st)
	card, err := st.ImportEJHCard(ejh.Card{OrderNo: "fixture", Number: "4242424242424242", Validity: "1299", CVV: "123", Status: "1"}, false)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	order := gptpay.Order{ID: "payment", Email: "demo@example.com", CardID: card.ID, Status: "success", CreatedAt: now, UpdatedAt: now}
	snap := gptpay.Snapshot{Input: gptpay.CreateInput{CardSecret: gptpay.CardSecret{Number: "4242424242424242", Year: 2099, Month: 12, CVV: "123"}}}
	if err = st.CreateGPTPayOrder(order, snap); err != nil {
		t.Fatal(err)
	}
	trade := ejh.Trade{OrderNo: "fixture", TradeNo: "debit", Type: "4", Status: "2", Number: "4242424242424242", CardAmount: "20.00", CardCurrency: "USD", TradeTime: now.Format(time.RFC3339)}
	if err = st.EJHSaveTrade(card.ID, trade); err != nil {
		t.Fatal(err)
	}
	s := &Server{store: st}
	mux := http.NewServeMux()
	s.ejhRoutes(mux)
	for _, endpoint := range []struct{ method, path string }{{"PUT", "/api/ejh/cases/payment/charge"}, {"PUT", "/api/ejh/trades/payment/match"}, {"POST", "/api/ejh/trades/payment/unmatch"}} {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest(endpoint.method, endpoint.path, strings.NewReader("{}")))
		if w.Code != http.StatusGone {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	if err = st.EJHReconcile(); err != nil {
		t.Fatal(err)
	}
	cases, err := st.EJHCasesForEmails([]string{order.Email})
	if err != nil || len(cases) != 1 || cases[0].Charge == nil {
		t.Fatal(cases, err)
	}
	raw, _ := json.Marshal(cases)
	if strings.Contains(string(raw), trade.Number) {
		t.Fatal("PAN exposed in account response")
	}
	if cases[0].Charge.CardAmount != "20.00" || cases[0].State != "opened" {
		t.Fatal(cases[0])
	}
}
