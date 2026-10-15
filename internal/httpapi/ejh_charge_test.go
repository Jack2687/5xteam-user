package httpapi

import (
	"chapt-space-user/internal/ejh"
	"chapt-space-user/internal/gptpay"
	"chapt-space-user/internal/store"
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
	trade := ejh.Trade{OrderNo: "fixture", TradeNo: "debit", Type: "4", Status: "2", Number: "4242424242424242", CardAmount: "20.00", CardCurrency: "USD"}
	if err = st.EJHSaveTrade(card.ID, trade); err != nil {
		t.Fatal(err)
	}
	s := &Server{store: st}
	mux := http.NewServeMux()
	s.ejhRoutes(mux)
	id := store.EJHID("trade", "fixture|debit|4")
	for _, tc := range []struct {
		body   string
		status int
	}{{"{", 400}, {`{"trade_id":"` + id + `","evidence":""}`, 400}, {`{"trade_id":"` + id + `","evidence":"verified order reference"}`, 200}} {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest("PUT", "/api/ejh/cases/payment/charge", strings.NewReader(tc.body)))
		if w.Code != tc.status {
			t.Fatal(w.Code, w.Body.String())
		}
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
