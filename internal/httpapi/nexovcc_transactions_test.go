package httpapi

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"chatgpt-space-merge/internal/nexovcc"
)

func TestNexovccTransactionActionsQueryOnlySelectedCards(t *testing.T) {
	for _, scope := range []string{"normal", "single", "deleted", "missing"} {
		t.Run(scope, func(t *testing.T) {
			s, _ := nexFixtureServer(t)
			cards := []nexovcc.Card{}
			pans := []string{"4242424242424242", "5555555555554444", "4000000000000002"}
			oldAt := time.Now().Add(-24 * time.Hour)
			f := nexovcc.Finance{At: oldAt, AccountAt: oldAt}
			for i, status := range []string{"1", "2", "4"} {
				card, err := s.store.SaveNexovccCard(nexovcc.Card{Serial: "scope-" + status, Status: status}, &nexovcc.PrivateCard{Number: pans[i], CVV: "123", Month: 12, Year: 2099})
				if err != nil {
					t.Fatal(err)
				}
				cards = append(cards, card)
				f.Funds = append(f.Funds, nexovcc.Fund{ID: card.Serial, Serial: card.Serial, Currency: "USD", Kind: 3001, Amount: "-10.00", At: oldAt})
			}
			if err := s.store.SaveNexovccFinance(f); err != nil {
				t.Fatal(err)
			}
			id := ""
			wanted := map[string]bool{pans[0]: true, pans[1]: true}
			switch scope {
			case "single":
				id, wanted = cards[1].ID, map[string]bool{pans[1]: true}
			case "deleted":
				id, wanted = cards[2].ID, map[string]bool{pans[2]: true}
			case "missing":
				id, wanted = "missing-card", map[string]bool{}
			}
			base := s.nexovcc.transport
			calls := map[string]int{}
			s.nexovcc.transport = func(ctx context.Context, r nexovcc.Request) (nexovcc.Response, error) {
				if r.Path == "/api/login" || r.Path == "/api/verifyGoogleCode" {
					return base(ctx, r)
				}
				if r.Path == "/api/card/getUserCardBalance" {
					selected := false
					for i, c := range cards {
						if c.Serial == r.Body["serial_no"] && wanted[pans[i]] && c.Status != "4" {
							selected = true
						}
					}
					if !selected {
						t.Fatal("balance query expanded scope", r.Body["serial_no"])
					}
					return base(ctx, r)
				}
				number, _ := r.Body["card_no"].(string)
				if !wanted[number] || (r.Path != "/api/findLogCardTrades" && r.Path != "/api/findLogWebFunds") {
					t.Errorf("transaction action expanded its scope: %s", r.Path)
				}
				if !nexovcc.ImmediateRequests(ctx) {
					t.Error("manual transaction sync should bypass pacing")
				}
				calls[number]++
				return base(ctx, r)
			}
			body, _ := json.Marshal(map[string]any{"action": "card_finance", "card_id": id})
			w := httptest.NewRecorder()
			s.runNexovcc(w, httptest.NewRequest("POST", "/", strings.NewReader(string(body))))
			if w.Code != 202 {
				t.Fatal(w.Code, w.Body.String())
			}
			s.nexovcc.wg.Wait()
			if (s.nexovcc.job.Status == "failed") != (scope == "missing") {
				t.Fatal(s.nexovcc.job)
			}
			for pan := range wanted {
				if calls[pan] != 2 {
					t.Fatal("expected trades and funds for each selected card", calls)
				}
			}
			var saved nexovcc.Finance
			if err := s.store.NexovccCached("finance", &saved); err != nil || !saved.AccountAt.Equal(oldAt) {
				t.Fatal("transaction sync changed account snapshot", err)
			}
			for i, card := range cards {
				if wanted[pans[i]] {
					if !saved.CardSyncedAt(card.Serial).After(oldAt) {
						t.Fatal("selected card timestamp not updated")
					}
				} else if !saved.CardSyncedAt(card.Serial).Equal(oldAt) {
					t.Fatal("another card was marked synchronized")
				}
			}
			if len(saved.Funds) != len(cards)-len(wanted) {
				t.Fatal("unselected history was changed", len(saved.Funds))
			}
		})
	}
}

func TestNexovccPageOptOutStillAllowsScopedManualFullSync(t *testing.T) {
	s, _ := nexFixtureServer(t)
	ctx := context.Background()
	op, err := s.openNexovccLocked(ctx, "nex-page-optout1", "template", false)
	if err != nil {
		t.Fatal(err)
	}
	cfg, _, _ := s.store.NexovccSettings()
	cfg.PageAutoRefresh = false
	if _, err = s.store.SaveNexovccSettings(cfg, nexovcc.Credentials{}, false); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if err = s.store.SaveNexovccFinance(nexovcc.Finance{At: now, AccountAt: now}); err != nil {
		t.Fatal(err)
	}
	base := s.nexovcc.transport
	calls, ledgers, summaries := 0, 0, 0
	s.nexovcc.transport = func(ctx context.Context, r nexovcc.Request) (nexovcc.Response, error) {
		calls++
		if r.Path == "/api/findLogRecharge" || r.Path == "/api/findLogReportCardDay" {
			summaries++
		} else if strings.Contains(r.Path, "findLog") {
			ledgers++
			if r.Body["card_no"] != "4242424242424242" || (r.Path != "/api/findLogWebFunds" && r.Path != "/api/findLogCardTrades") {
				t.Errorf("manual full refresh broadened ledger scope: %s", r.Path)
			}
		}
		return base(ctx, r)
	}
	s.nexovcc.client = nil
	w := httptest.NewRecorder()
	s.runNexovcc(w, httptest.NewRequest("POST", "/", strings.NewReader(`{"action":"sync","automatic_refresh":true}`)))
	s.nexovcc.wg.Wait()
	if w.Code != 200 || calls != 0 {
		t.Fatal("page opt-out queried upstream", w.Code, calls)
	}
	w = httptest.NewRecorder()
	s.runNexovcc(w, httptest.NewRequest("POST", "/", strings.NewReader(`{"action":"sync"}`)))
	s.nexovcc.wg.Wait()
	if w.Code != 202 || s.nexovcc.job.Status != "completed" || ledgers != 2 || summaries != 2 {
		t.Fatal(w.Code, s.nexovcc.job, ledgers)
	}
	var f nexovcc.Finance
	if err = s.store.NexovccCached("finance", &f); err != nil || !f.AccountAt.After(now) || !f.CardSyncedAt(op.Serial).After(now) {
		t.Fatal("manual sync missed account summary or same-card ledger", err)
	}
}
