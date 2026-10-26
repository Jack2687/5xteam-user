package httpapi

import (
	"chatgpt-space-merge/internal/nexovcc"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCardPaymentPolicyLocalValidation(t *testing.T) {
	s, _ := nexFixtureServer(t)
	s.nexovcc.transport = func(context.Context, nexovcc.Request) (nexovcc.Response, error) {
		t.Fatal("accounting contacted issuer")
		return nexovcc.Response{}, nil
	}
	c, err := s.store.SaveNexovccCard(nexovcc.Card{Serial: "payment-policy", Status: "1", Balance: "10"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	s.nexovccRoutes(mux)
	call := func(id, body string, want int) {
		t.Helper()
		w := httptest.NewRecorder()
		r := httptest.NewRequest("PUT", "/api/nexovcc/cards/"+id+"/payment-policy", strings.NewReader(body))
		mux.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("got %d want %d: %s", w.Code, want, w.Body.String())
		}
	}
	call(c.ID, `{"mode":"payment_only","note":"test"}`, 200)
	call(c.ID, `{"mode":"normal"}`, 409)
	call(c.ID, `{"mode":"returned","revision":1}`, 400)
	call("missing", `{"mode":"payment_only","note":"test"}`, 404)
	call(c.ID, `{"mode":"normal","revision":1}`, 200)
	after, _ := s.store.NexovccCard(c.ID)
	if after.Status != c.Status || after.Balance != c.Balance {
		t.Fatal("changed actual card")
	}
}
