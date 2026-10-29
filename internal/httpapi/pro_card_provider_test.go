package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"testing"
	"time"

	"chatgpt-space-merge/internal/gptpay"
	"chatgpt-space-merge/internal/model"
	"chatgpt-space-merge/internal/nexovcc"
)

func TestProConfiguredCardListFiltersBeforePagination(t *testing.T) {
	s, f := nexFixtureServer(t)
	if _, err := s.store.SaveGPTPayCard(gptpay.Card{ID: "earliest-other", Enabled: true, CreatedAt: time.Now().Add(-72 * time.Hour)}, gptpay.CardSecret{Number: "4242424242424242", CVV: "123", Month: 12, Year: 2099}); err != nil {
		t.Fatal(err)
	}
	want := []string{}
	for i := 0; i < 11; i++ {
		number := fmt.Sprintf("555555555555%04d", i)
		c, err := s.store.SaveNexovccCard(nexovcc.Card{Serial: number, Status: "1", Balance: "10.00", CreatedAt: time.Now().Add(time.Duration(i-12) * time.Hour)}, &nexovcc.PrivateCard{Number: number, CVV: "123", Month: 12, Year: 2099})
		if err != nil {
			t.Fatal(err)
		}
		want = append(want, c.ID)
	}
	cfg := model.DefaultProSettings()
	cfg.CardProvider, cfg.NexovccAutoOpen = "nexovcc", false
	if _, err := s.store.SaveProSettings(cfg, "", ""); err != nil {
		t.Fatal(err)
	}
	list := func(query string, expectedTotal int, expectedIDs []string) {
		t.Helper()
		w := httptest.NewRecorder()
		s.listGPTPayCards(w, httptest.NewRequest("GET", "/api/gptpay/cards?"+query, nil))
		var reply struct {
			Data struct {
				Items []gptpay.Card `json:"items"`
				Total int           `json:"total"`
			} `json:"data"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &reply); err != nil || w.Code != 200 || reply.Data.Total != expectedTotal || len(reply.Data.Items) != len(expectedIDs) {
			t.Fatal("issuer pagination/default incorrect", w.Code, w.Body.String(), err)
		}
		for i, id := range expectedIDs {
			if reply.Data.Items[i].ID != id {
				t.Fatal("wrong issuer or chronological ordering", reply.Data.Items[i].ID, id)
			}
		}
	}
	list("enabled=true&card_provider=configured&page_size=10&page=1", 11, want[:10])
	list("enabled=true&card_provider=configured&page_size=10&page=2", 11, want[10:])
	list("enabled=true&card_provider=ejh&page_size=10", 1, []string{"earliest-other"})
	list("enabled=true&card_provider=nexovcc&page_size=10&page=2", 11, want[10:])
	list("enabled=true&page_size=10", 12, append([]string{"earliest-other"}, want[:9]...)) // General bank management still includes all issuers.
	allocationAccount(t, s, "stale-dialog@example.com")
	if _, _, err := s.reserveProStartCard(context.Background(), "stale-dialog@example.com", "earliest-other", false, false); err == nil {
		t.Fatal("stale dialog bypassed configured Nexovcc issuer")
	}
	allocationAccount(t, s, "busy@example.com")
	if err := s.store.ReserveProCard("busy@example.com", want[0], false); err != nil {
		t.Fatal(err)
	}
	list("enabled=true&card_provider=configured&page_size=10", 10, want[1:])
	cfg.CardProvider = "ejh"
	if _, err := s.store.SaveProSettings(cfg, "", ""); err != nil {
		t.Fatal(err)
	}
	list("enabled=true&card_provider=configured&page_size=10", 1, []string{"earliest-other"})
	list("enabled=true&card_provider=nexovcc&page_size=10", 10, want[1:])
	if _, err := s.store.SetNexovccCardNormalPending(want[1], true); err != nil {
		t.Fatal(err)
	}
	list("enabled=true&card_provider=nexovcc&page_size=10", 9, want[2:])
	automatic, err := s.automaticProCards(model.ProSettings{CardProvider: "nexovcc"}, true)
	if err != nil || len(automatic) != 9 {
		t.Fatal("automatic pending exclusion", len(automatic), err)
	}
	for _, card := range automatic {
		if card.ID == want[1] {
			t.Fatal("pending card selected by automation")
		}
	}
	if _, err = s.store.SetNexovccCardNormalPending(want[1], false); err != nil {
		t.Fatal(err)
	}
	list("enabled=true&card_provider=nexovcc&page_size=10", 10, want[1:])
	manual := context.WithValue(context.Background(), proManualCardProviderKey{}, "nexovcc")
	if _, _, err := s.reserveProStartCard(manual, "stale-dialog@example.com", "earliest-other", false, false); err == nil {
		t.Fatal("manual issuer allowed a mismatched card")
	}
	if f.opens.Load() != 0 {
		t.Fatal("listing caused issuance")
	}
}
