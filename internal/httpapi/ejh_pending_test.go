package httpapi

import (
	"chatgpt-space-merge/internal/ejh"
	"chatgpt-space-merge/internal/nexovcc"
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestEJHPendingSyncExclusionAndManualRefresh(t *testing.T) {
	s, _ := ejhTestServer(t, 200)
	remote := ejh.Card{OrderNo: "pending", Number: "4242424242424242", CVV: "123", Validity: "1299", Status: "1", CardCurrency: "USD", Note: "before"}
	card, err := s.store.ImportEJHCard(remote, true)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("PUT", "/", strings.NewReader(`{"category":"normal_pending"}`))
	r.SetPathValue("id", card.ID)
	w := httptest.NewRecorder()
	s.ejhCardCategory(w, r)
	if w.Code != 200 || strings.Contains(w.Body.String(), remote.Number) || strings.Contains(w.Body.String(), `"cvvPassword":"123"`) {
		t.Fatal("category response", w.Code, w.Body.String())
	}
	manual := false
	calls := 0
	s.ejh.client.HTTP = ejhFixtureTransport(t, func(method string, data map[string]any) any {
		changed := remote
		changed.Note = "after"
		if method == "card.list.info" {
			return map[string]any{"cardInfos": []ejh.Card{changed}}
		}
		calls++
		if !manual || data["orderNo"] != remote.OrderNo {
			t.Fatal("unexpected pending request", method, data)
		}
		switch method {
		case "card.query.info":
			return changed
		case "card.balance.info":
			return map[string]string{"balance": "20.00"}
		case "card.trade.info":
			return map[string]any{"tradeInfos": []ejh.Trade{{OrderNo: remote.OrderNo, TradeNo: "saved-manually", Type: "4", Status: "2", TradeAmount: "1.00", TradeCurrency: "USD"}}}
		}
		t.Fatal("unexpected method", method)
		return nil
	})
	if err = s.syncEJHLocked(context.Background(), time.Now().Add(-time.Hour), time.Now()); err != nil {
		t.Fatal(err)
	}
	var rec ejh.CardRecord
	s.store.EJHGet("card", card.ID, &rec)
	if calls != 0 || rec.Remote.Note != "before" || !rec.NormalPending {
		t.Fatal("ordinary sync changed pending card", rec, calls)
	}
	manual = true
	for _, action := range []string{"refresh", "balance", "sync_trades"} {
		r = httptest.NewRequest("POST", "/", strings.NewReader(`{"action":"`+action+`"}`))
		r.SetPathValue("id", card.ID)
		w = httptest.NewRecorder()
		s.ejhCardAction(w, r)
		if w.Code != 200 {
			t.Fatal(action, w.Code, w.Body.String())
		}
	}
	s.store.EJHGet("card", card.ID, &rec)
	if rec.Remote.Note != "after" || rec.Balance != "20.00" || !rec.NormalPending || calls < 3 {
		t.Fatal("manual refresh missing", rec, calls)
	}
}

func TestCardPlatformCCNeverAutoRecycled(t *testing.T) {
	card := nexovcc.Card{ID: "cc", Serial: "cc", Platform: "cc", Status: "1", Imported: true}
	if nexovccRecycleCandidate(card, nil, nil) {
		t.Fatal("CC recycled")
	}
	card.Platform = "oai"
	if !nexovccRecycleCandidate(card, nil, nil) {
		t.Fatal("OAI policy changed")
	}
	s, _ := nexFixtureServer(t)
	cfg, _, _ := s.store.NexovccSettings()
	cfg.AutoWithdrawEnabled = true
	if _, err := s.store.SaveNexovccSettings(cfg, nexovcc.Credentials{}, false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.store.SaveNexovccCard(nexovcc.Card{Serial: "cc", Platform: "cc", Status: "1", Balance: "20.00"}, &nexovcc.PrivateCard{Number: "4242424242424242", CVV: "123", Month: 12, Year: 2099}); err != nil {
		t.Fatal(err)
	}
	if err := s.store.SaveNexovccFinance(nexovcc.Finance{At: time.Now()}); err != nil {
		t.Fatal(err)
	}
	s.nexovcc.transport = func(context.Context, nexovcc.Request) (nexovcc.Response, error) {
		t.Fatal("CC automatic operation contacted issuer")
		return nexovcc.Response{}, nil
	}
	if err := s.autoWithdrawNexovccLocked(context.Background()); err != nil {
		t.Fatal(err)
	}
}
