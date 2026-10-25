package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"chatgpt-space-merge/internal/nexovcc"
)

func TestNexovccHolderCopy(t *testing.T) {
	for _, mode := range []string{"legacy", "synced", "closed", "paused", "mismatch", "missing", "failure"} {
		t.Run(mode, func(t *testing.T) {
			s, _ := nexFixtureServer(t)
			holder := map[string]any{"first_name": "Test", "last_name": "Holder", "country": "US", "state": "NY", "city": "Example", "street": "1 Test Street", "address": "1 Test Street, Example, NY 01234", "zip_code": "01234"}
			detail := map[string]any{"serial_no": "copy-holder", "status": "1", "user_card_holder": map[string]any{"card_holder": holder}}
			card := nexovcc.Card{Serial: "copy-holder", Status: "1", Balance: "25.00", NormalPending: true}
			if mode == "closed" {
				card.Status = "4"
			}
			if mode == "synced" {
				parsed, err := nexovcc.CardFrom(detail)
				if err != nil || parsed.Holder == nil {
					t.Fatal("sync did not parse holder")
				}
				card.Holder = parsed.Holder
			}
			card, err := s.store.SaveNexovccCard(card, &nexovcc.PrivateCard{Number: "4242424242424242", CVV: "012", Month: 3, Year: 2099})
			if err != nil {
				t.Fatal(err)
			}
			calls := 0
			s.nexovcc.transport = func(ctx context.Context, r nexovcc.Request) (nexovcc.Response, error) {
				if r.Path == "/api/login" {
					v := nexReply(map[string]any{"user_id": 7})
					v.Cookie = "fixture-cookie"
					return v, nil
				}
				calls++
				if r.Path != "/api/card/getUserCardDetail" || r.Body["serial_no"] != card.Serial || !nexovcc.ImmediateRequests(ctx) {
					t.Fatal("copy must query only this card's details, as a manual request")
				}
				if mode == "mismatch" {
					detail["serial_no"] = "another-card"
				}
				if mode == "missing" {
					delete(detail, "user_card_holder")
				}
				if mode == "failure" {
					return nexovcc.Response{Status: 522}, nil
				}
				return nexReply(detail), nil
			}
			if mode == "paused" {
				if err = s.store.NexovccCache("request_control", nexovccControlState{Paused: true}); err != nil {
					t.Fatal(err)
				}
			}
			request := func(auth bool) *httptest.ResponseRecorder {
				r := httptest.NewRequest("POST", "/api/nexovcc/cards/"+card.ID+"/private?include_holder=true", strings.NewReader(`{}`))
				r.Header.Set("Content-Type", "application/json")
				if auth {
					r.AddCookie(&http.Cookie{Name: authCookieName, Value: s.createSession()})
				}
				w := httptest.NewRecorder()
				s.Handler().ServeHTTP(w, r)
				return w
			}
			if w := request(false); w.Code != 401 || calls != 0 {
				t.Fatal("unauthenticated copy was allowed")
			}
			w := request(true)
			if mode == "paused" || mode == "mismatch" || mode == "missing" || mode == "failure" {
				if w.Code != 400 {
					t.Fatal("failed holder lookup should not report copy success", w.Code)
				}
				saved, _ := s.store.NexovccCard(card.ID)
				if saved.Holder != nil || saved.Balance != "25.00" || !saved.NormalPending || (mode == "paused" && calls != 0) {
					t.Fatal("failed lookup changed card state or bypassed stop")
				}
				return
			}
			if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("private copy failed", w.Code)
			}
			var response struct {
				Data struct {
					Holder *nexovcc.CardHolder `json:"holder"`
					CVV    string              `json:"cvv"`
				} `json:"data"`
			}
			if err = json.Unmarshal(w.Body.Bytes(), &response); err != nil || response.Data.CVV != "012" {
				t.Fatal("private response invalid")
			}
			if mode != "closed" && (response.Data.Holder == nil || response.Data.Holder.FirstName != "Test" || response.Data.Holder.ZipCode != "01234") {
				t.Fatal("holder fields lost")
			}
			// Later partial list/balance snapshots must preserve holder details.
			card.Balance = "31.00"
			card.Holder = nil
			if _, err = s.store.SaveNexovccCard(card, nil); err != nil {
				t.Fatal(err)
			}
			if w = request(true); w.Code != 200 {
				t.Fatal("cached copy failed")
			}
			saved, _ := s.store.NexovccCard(card.ID)
			if saved.Balance != "31.00" || (mode != "closed" && (saved.Holder == nil || !saved.NormalPending)) {
				t.Fatal("partial sync lost saved holder or card state")
			}
			wantCalls := 0
			if mode == "legacy" {
				wantCalls = 1
			}
			if calls != wantCalls {
				t.Fatal("unexpected repeated or closed-card queries", calls)
			}
		})
	}
}
