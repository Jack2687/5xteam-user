package httpapi

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"chatgpt-space-merge/internal/nexovcc"
)

func TestNexovccPendingRefreshScopes(t *testing.T) {
	for _, mode := range []string{"page", "sync", "finance", "normal_finance", "pending_finance", "pending_sync", "single_pending", "failed_pending"} {
		t.Run(mode, func(t *testing.T) {
			s, _ := nexFixtureServer(t)
			oldAt := time.Now().Add(-24 * time.Hour)
			cards := map[string]nexovcc.Card{}
			numbers := map[string]string{"normal": "4242424242424242", "pending": "5555555555554444", "closed": "4000000000000002"}
			finance := nexovcc.Finance{At: oldAt, AccountAt: oldAt}
			for _, serial := range []string{"normal", "pending", "closed"} {
				status := "1"
				if serial == "closed" {
					status = "4"
				}
				card, err := s.store.SaveNexovccCard(nexovcc.Card{Serial: serial, Status: status, Balance: "20.00", BalanceAt: oldAt}, &nexovcc.PrivateCard{Number: numbers[serial], CVV: "123", Month: 12, Year: 2099})
				if err != nil {
					t.Fatal(err)
				}
				if serial == "pending" {
					card, err = s.store.SetNexovccCardNormalPending(card.ID, true)
					if err != nil {
						t.Fatal(err)
					}
				}
				cards[serial] = card
				finance.Funds = append(finance.Funds, nexovcc.Fund{ID: serial + "-old", Serial: serial, Kind: 3001, Amount: "-20.00", Currency: "USD", At: oldAt})
			}
			if err := s.store.SaveNexovccFinance(finance); err != nil {
				t.Fatal(err)
			}
			// General sync must not indirectly fetch pending cards via operation verification.
			if err := s.store.BeginNexovccOperation(nexovcc.Operation{ID: "nex-pending-freeze", Action: "freeze", CardID: cards["pending"].ID, Serial: "pending", Status: "processing", CreatedAt: oldAt}); err != nil {
				t.Fatal(err)
			}
			for serial, card := range cards {
				cards[serial], _ = s.store.NexovccCard(card.ID)
			}
			base := s.nexovcc.transport
			want := "normal"
			if strings.Contains(mode, "pending") {
				want = "pending"
			}
			calls := map[string]int{}
			s.nexovcc.transport = func(ctx context.Context, r nexovcc.Request) (nexovcc.Response, error) {
				calls[r.Path]++
				switch r.Path {
				case "/api/card/findUserCardList":
					if want == "pending" {
						t.Error("pending refresh fetched the full card list")
					}
					return nexReply(map[string]any{"list": []any{
						map[string]any{"serial_no": "normal", "status": "1", "card_no": numbers["normal"], "balance": "10.00"},
						// Stale list data must not replace the pending card's saved snapshot.
						map[string]any{"serial_no": "pending", "status": "4", "card_no": numbers["pending"], "balance": "0.00"},
					}}), nil
				case "/api/card/getUserCardDetail", "/api/card/getUserCardBalance":
					if r.Body["serial_no"] != want {
						t.Errorf("wrong card requested: %s %v", r.Path, r.Body["serial_no"])
					}
					if r.Path == "/api/card/getUserCardDetail" {
						return nexReply(map[string]any{"serial_no": want, "status": "1", "card_no": numbers[want], "cvv": "123", "expire_at_str": "12/99", "remark": "fresh pending"}), nil
					}
				case "/api/findLogCardTrades", "/api/findLogWebFunds":
					if r.Body["card_no"] != numbers[want] {
						t.Errorf("unscoped ledger query: %s", r.Path)
					}
					if mode == "failed_pending" && r.Path == "/api/findLogWebFunds" {
						return nexovcc.Response{Status: 522}, nil
					}
				default:
					if want == "pending" && r.Path != "/api/login" && r.Path != "/api/verifyGoogleCode" {
						t.Errorf("pending refresh expanded to %s", r.Path)
					}
				}
				return base(ctx, r)
			}
			input := nexovccInput{Action: "card_finance"}
			switch mode {
			case "page":
				input.Action = "sync"
				input.AutomaticRefresh = true
			case "sync", "finance":
				input.Action = mode
			case "normal_finance":
				input.CardCategory = "normal"
			case "pending_finance":
				input.CardCategory = "normal_pending"
			case "pending_sync", "failed_pending":
				input.Action = "pending_sync"
			case "single_pending":
				input.CardID = cards["pending"].ID
			}
			body, _ := json.Marshal(input)
			w := httptest.NewRecorder()
			s.runNexovcc(w, httptest.NewRequest("POST", "/api/nexovcc/run", strings.NewReader(string(body))))
			s.nexovcc.wg.Wait()
			if w.Code != 202 || (s.nexovcc.job.Status == "failed") != (mode == "failed_pending") {
				t.Fatal(w.Code, s.nexovcc.job)
			}
			if calls["/api/findLogCardTrades"] != 1 || calls["/api/findLogWebFunds"] != 1 {
				t.Fatal("wrong ledger request count", calls)
			}
			pending, _ := s.store.NexovccCard(cards["pending"].ID)
			if want == "pending" {
				wantBalance := "10.00"
				if mode == "failed_pending" {
					wantBalance = cards["pending"].Balance
				}
				if pending.Balance != wantBalance || !pending.NormalPending || calls["/api/card/getUserCardBalance"] != 1 {
					t.Fatal("wrong pending snapshot", pending, calls)
				}
				if mode == "pending_sync" || mode == "failed_pending" {
					if pending.Remark != "fresh pending" || calls["/api/card/getUserCardDetail"] != 1 {
						t.Fatal("pending metadata was not refreshed", pending, calls)
					}
				}
				if mode == "failed_pending" && (!pending.BalanceAt.Equal(oldAt) || pending.Error == "") {
					t.Fatal("partial finance published", pending)
				}
			} else if !reflect.DeepEqual(pending, cards["pending"]) {
				t.Fatal("pending snapshot changed during unrelated refresh")
			}
			var saved nexovcc.Finance
			if err := s.store.NexovccCached("finance", &saved); err != nil {
				t.Fatal(err)
			}
			for serial := range cards {
				refreshed := serial == want && mode != "failed_pending"
				if refreshed != saved.CardSyncedAt(serial).After(oldAt) {
					t.Fatalf("wrong finance timestamp for %s: %v", serial, saved.CardSyncedAt(serial))
				}
				found := false
				for _, fund := range saved.Funds {
					if fund.Serial == serial {
						found = true
					}
				}
				if found == refreshed {
					t.Fatalf("history erased or not refreshed for %s", serial)
				}
			}
			if want == "pending" && !saved.AccountAt.Equal(oldAt) {
				t.Fatal("pending refresh changed account snapshot")
			}
		})
	}
}

func TestNexovccPendingRefreshEmptyAndInvalid(t *testing.T) {
	s, _ := nexFixtureServer(t)
	s.nexovcc.transport = func(context.Context, nexovcc.Request) (nexovcc.Response, error) {
		t.Error("empty/invalid request contacted supplier")
		return nexovcc.Response{}, nil
	}
	for _, body := range []string{`{"action":"card_finance","card_category":"closed"}`, `{"action":"sync","card_category":"normal_pending"}`, `{"action":"pending_sync","automatic_refresh":true}`, `{"action":"card_finance","card_category":"normal_pending","automatic_refresh":true}`} {
		w := httptest.NewRecorder()
		s.runNexovcc(w, httptest.NewRequest("POST", "/", strings.NewReader(body)))
		if w.Code != 400 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	w := httptest.NewRecorder()
	s.runNexovcc(w, httptest.NewRequest("POST", "/", strings.NewReader(`{"action":"pending_sync"}`)))
	s.nexovcc.wg.Wait()
	if w.Code != 202 || s.nexovcc.job.Status != "completed" || !strings.Contains(s.nexovcc.job.Message, "0 张") {
		t.Fatal(w.Code, s.nexovcc.job)
	}
}
