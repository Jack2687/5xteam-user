package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"chatgpt-space-merge/internal/gptpay"
	"chatgpt-space-merge/internal/nexovcc"
	"chatgpt-space-merge/internal/store"
)

func TestNexovccDeleteUnopenedCasesAPI(t *testing.T) {
	s, _ := nexFixtureServer(t)
	s.nexovcc.transport = func(context.Context, nexovcc.Request) (nexovcc.Response, error) {
		t.Error("return-list cleanup must not call the card platform")
		return nexovcc.Response{}, nil
	}
	card, err := s.store.SaveNexovccCard(nexovcc.Card{Serial: "cleanup", Status: "1", Balance: "10.00"}, &nexovcc.PrivateCard{Number: "4242424242424242", CVV: "123", Month: 12, Year: 2099})
	if err != nil {
		t.Fatal(err)
	}
	for _, status := range []string{"failed", "processing"} {
		o := gptpay.Order{ID: status, Email: status + "@example.com", CardID: card.ID, Status: status, CreatedAt: time.Now()}
		snapshot := gptpay.Snapshot{APIKey: "secret-fixture", Input: gptpay.CreateInput{CardSecret: gptpay.CardSecret{Number: "4242424242424242", CVV: "123", Month: 12, Year: 2099}}}
		if err = s.store.CreateGPTPayOrder(o, snapshot); err != nil {
			t.Fatal(err)
		}
	}
	s.sessions["test"] = time.Now().Add(time.Hour)
	mux := http.NewServeMux()
	s.nexovccRoutes(mux)
	handler := s.authMiddleware(mux)
	call := func(method, path, body string, logged bool, origin string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Origin", origin)
		if logged {
			r.AddCookie(&http.Cookie{Name: authCookieName, Value: "test"})
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	for _, tc := range []struct {
		body, origin string
		logged       bool
		code         int
	}{
		{`{"confirm":true}`, "", false, 401},
		{`{"confirm":true}`, "https://unrelated.example", true, 403},
		{`{`, "", true, 400},
		{`{}`, "", true, 400},
	} {
		w := call("DELETE", "/api/nexovcc/cases/unopened", tc.body, tc.logged, tc.origin)
		if w.Code != tc.code {
			t.Fatal(tc, w.Code, w.Body.String())
		}
	}
	for _, count := range []int{1, 0} {
		w := call("DELETE", "/api/nexovcc/cases/unopened", `{"confirm":true}`, true, "")
		var out struct{ Data store.NexovccCaseRemoval }
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &out) != nil || out.Data.Deleted != count || out.Data.Skipped != 1 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	w := call("GET", "/api/nexovcc/dashboard", "", true, "")
	var out struct {
		Data struct{ Cases []store.NexovccCase }
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &out) != nil || len(out.Data.Cases) != 2 {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, c := range out.Data.Cases {
		if (c.RemovedAt != "") != (c.ID == "failed") {
			t.Fatal("wrong visibility", c)
		}
	}
	for _, secret := range []string{"secret-fixture", "4242424242424242"} {
		if strings.Contains(w.Body.String(), secret) {
			t.Fatal("sensitive data exposed")
		}
	}
}
