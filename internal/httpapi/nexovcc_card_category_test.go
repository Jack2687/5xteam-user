package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"chatgpt-space-merge/internal/nexovcc"
)

func TestNexovccCardCategoryIsLocalAndValidates(t *testing.T) {
	s, _ := nexFixtureServer(t)
	s.nexovcc.transport = func(context.Context, nexovcc.Request) (nexovcc.Response, error) {
		t.Fatal("classification contacted supplier")
		return nexovcc.Response{}, nil
	}
	c, err := s.store.SaveNexovccCard(nexovcc.Card{Serial: "category", Status: "1", Balance: "1.00"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	m := http.NewServeMux()
	s.nexovccRoutes(m)
	call := func(id, body string, want int) {
		r := httptest.NewRequest("PUT", "/api/nexovcc/cards/"+id+"/category", strings.NewReader(body))
		w := httptest.NewRecorder()
		m.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("got %d want %d: %s", w.Code, want, w.Body.String())
		}
	}
	call(c.ID, `{"category":"normal_pending"}`, 200)
	got, _ := s.store.NexovccCard(c.ID)
	if !got.NormalPending || got.Status != "1" || got.Balance != "1.00" {
		t.Fatal("wrong card data", got)
	}
	call(c.ID, `{"category":"normal"}`, 200)
	call(c.ID, `{"category":"closed"}`, 400)
	call(c.ID, `{}`, 400)
	call("missing", `{"category":"normal_pending"}`, 404)
	c.Status = "4"
	if _, err = s.store.SaveNexovccCard(c, nil); err != nil {
		t.Fatal(err)
	}
	call(c.ID, `{"category":"normal_pending"}`, 409)
}
