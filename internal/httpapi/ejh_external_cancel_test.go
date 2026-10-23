package httpapi

import (
	"context"
	"strings"
	"testing"
	"time"

	"chatgpt-space-merge/internal/ejh"
)

func TestEJHIncreaseRejectedReleasesClaimWithoutRetry(t *testing.T) {
	for _, code := range []string{"800006", "300002"} {
		t.Run(code, func(t *testing.T) {
			s, _ := ejhTestServer(t, 200)
			remote := ejh.Card{OrderNo: "rejected-increase", Number: "4242424242424242", CVV: "123", Validity: "1299", Status: "3"}
			card, err := s.store.ImportEJHCard(remote, true)
			if err != nil {
				t.Fatal(err)
			}
			calls := 0
			s.ejh.client.HTTP = ejhFixtureTransport(t, func(method string, data map[string]any) any {
				if method != "card.change.info" || data["orderNo"] != remote.OrderNo {
					t.Fatal(method, data)
				}
				calls++
				return &ejh.APIError{Code: code}
			})
			for i := 0; i < 2; i++ {
				w := cancelFixtureRequest(s, card.ID, "rejected-increase-request", "increase")
				want := "unknown"
				if code == "800006" {
					want = "failed"
				}
				if w.Code != 200 || !strings.Contains(w.Body.String(), `"status":"`+want+`"`) {
					t.Fatal(w.Code, w.Body.String())
				}
			}
			if calls != 1 {
				t.Fatal("mutation replayed", calls)
			}
			local, _, err := s.store.GPTPayCard(card.ID)
			if err != nil || local.IssuerFrozen || local.IssuerLocked != (code != "800006") {
				t.Fatal("wrong claim/freeze state", local, err)
			}
		})
	}
}

func TestEJHExternalCancellationQueriesOnlyAndStops(t *testing.T) {
	s, _ := ejhTestServer(t, 200)
	remote := ejh.Card{OrderNo: "website-cancel", Number: "4242424242424242", CVV: "123", Validity: "1299", Status: "8"}
	card, err := s.store.ImportEJHCard(remote, true)
	if err != nil {
		t.Fatal(err)
	}
	var record ejh.CardRecord
	if err = s.store.EJHGet("card", card.ID, &record); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	record.Balance = "123.45"
	record.BalanceAt = &now
	if err = s.store.EJHPut("card", card.ID, record); err != nil {
		t.Fatal(err)
	}
	queries := 0
	s.ejh.client.HTTP = ejhFixtureTransport(t, func(method string, data map[string]any) any {
		if method != "card.query.info" || data["orderNo"] != remote.OrderNo {
			t.Fatal("unexpected upstream operation", method, data)
		}
		queries++
		return remote
	})
	w := cancelFixtureRequest(s, card.ID, "balance-during-cancel", "balance")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"processing":true`) || !strings.Contains(w.Body.String(), `"balance":"123.45"`) || queries != 0 {
		t.Fatal(w.Code, w.Body.String(), queries)
	}
	s.tickEJHCancellations(context.Background())
	if queries != 1 {
		t.Fatal(queries)
	}
	remote.Status = "2"
	pending, err := s.store.EJHPendingCancellations(time.Now().Add(time.Minute), 10)
	if err != nil || len(pending) != 1 {
		t.Fatal(pending, err)
	}
	op := pending[0]
	op.NextCheckAt = nil
	if err = s.store.EJHPut("operation", op.ID, op); err != nil {
		t.Fatal(err)
	}
	s.tickEJHCancellations(context.Background())
	s.tickEJHCancellations(context.Background())
	if queries != 2 {
		t.Fatal("completed card polled again", queries)
	}
	if err = s.store.EJHGet("operation", op.ID, &op); err != nil || op.Status != "completed" {
		t.Fatal(op, err)
	}
}
