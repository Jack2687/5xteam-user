package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"chatgpt-space-merge/internal/ejh"
	"chatgpt-space-merge/internal/gptpay"
)

func TestEJHPrivateUsesSavedCredentials(t *testing.T) {
	s, st, _ := gptPayFixture(t, func(http.ResponseWriter, *http.Request) { t.Error("copy must not call supplier") })
	handler := s.Handler()
	for _, status := range []string{"1", "2", "5"} {
		card, err := st.ImportEJHCard(ejh.Card{OrderNo: "copy-card", Number: "4242424242424242", CVV: "012", Validity: "0399", Status: status}, false)
		if err != nil {
			t.Fatal(err)
		}
		r := httptest.NewRequest("POST", "/api/ejh/cards/"+card.ID+"/private", strings.NewReader(`{}`))
		r.AddCookie(&http.Cookie{Name: authCookieName, Value: s.createSession()})
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("status %s: HTTP %d", status, w.Code)
		}
		var response struct {
			Data gptpay.CardSecret `json:"data"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		if response.Data != (gptpay.CardSecret{Number: "4242424242424242", Month: 3, Year: 2099, CVV: "012"}) {
			t.Fatal("saved card credentials changed")
		}
	}
}
