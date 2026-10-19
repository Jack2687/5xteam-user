package store

import (
	"testing"
	"time"

	"chatgpt-space-merge/internal/gptpay"
	"chatgpt-space-merge/internal/model"
	"chatgpt-space-merge/internal/nexovcc"
)

func TestProAutoFailedPaymentKeepsCardThroughRetry(t *testing.T) {
	for _, provider := range []string{"nexovcc", "ejh", "other"} {
		for _, outcome := range []string{"success", "failed", "submission_unknown"} {
			t.Run(provider+"/"+outcome, func(t *testing.T) {
				s := transferStore(t)
				email := "retry@example.com"
				transferSource(t, s, email)
				o := transferOrder(email, "unused", "submitting")
				var cardID string
				if provider == "nexovcc" {
					ensureIssuerFixtureEnabled(t, s, "nexovcc")
					c, err := s.SaveNexovccCard(nexovcc.Card{Serial: "retry-card", Status: "1", Balance: "10.00"}, &nexovcc.PrivateCard{Number: o.Snapshot.Input.Number, CVV: "123", Month: 12, Year: 2099})
					if err != nil {
						t.Fatal(err)
					}
					cardID = c.ID
				} else {
					limit := 1
					c, err := s.SaveGPTPayCard(gptpay.Card{ID: "retry-card", Enabled: true, CardType: provider, MaxAccounts: &limit}, o.Snapshot.Input.CardSecret)
					if err != nil {
						t.Fatal(err)
					}
					cardID = c.ID
				}
				a := model.ProAutoState{ID: "retry-flow", CardID: cardID, Status: "running", Stage: "recharge", Steps: map[string]string{"login": "completed", "recharge": "running"}}
				o.Order.ID, o.Order.CardID = a.PaymentOrderID(), cardID
				a.OrderID = o.Order.ID
				if _, err := s.UpdateProAccount(email, func(p *model.MailAccountProfile) { p.ProAuto = a }); err != nil {
					t.Fatal(err)
				}
				if err := s.ReserveProCard(email, cardID, false); err != nil {
					t.Fatal(err)
				}
				if err := s.CreateGPTPayOrder(o.Order, o.Snapshot); err != nil {
					t.Fatal(err)
				}
				o.Order.Status = "failed"
				if err := s.UpdateGPTPayOrder(o.Order); err != nil {
					t.Fatal(err)
				}
				checkHeld := func() {
					t.Helper()
					card, _, err := s.GPTPayCard(cardID)
					if err != nil || card.Available || card.PendingAccounts != 1 || card.OpenedAccounts != 0 {
						t.Fatalf("retry lost/doubled its place: %+v %v", card, err)
					}
					if err := s.ReserveProCard("competitor@example.com", cardID, false); err == nil {
						t.Fatal("another account stole the card before retry")
					}
				}
				checkHeld() // Even before retry preparation, the failed response cannot free the card.
				retried, err := s.PrepareProAutoRetry(email, a, 1, time.Now().Add(time.Minute), "retry")
				if err != nil {
					t.Fatal(err)
				}
				checkHeld() // During backoff and the next login, retain the original card.
				o.Order.ID, o.Order.Status = retried.ProAuto.PaymentOrderID(), "submitting"
				if _, err = s.UpdateProAccount(email, func(p *model.MailAccountProfile) {
					p.ProAuto.OrderID, p.ProAuto.Stage = o.Order.ID, "recharge"
				}); err != nil {
					t.Fatal(err)
				}
				if err = s.CreateGPTPayOrder(o.Order, o.Snapshot); err != nil {
					t.Fatal(err)
				}
				checkHeld() // The next payment replaces, rather than adds to, that place.
				o.Order.Status = outcome
				if err = s.UpdateGPTPayOrder(o.Order); err != nil {
					t.Fatal(err)
				}
				if outcome == "failed" {
					checkHeld()
				}
				if _, err = s.UpdateProAccount(email, func(p *model.MailAccountProfile) { p.ProAuto.Status = "failed" }); err != nil {
					t.Fatal(err)
				}
				if err = s.ReleaseProCard(email); err != nil { // Normal worker exit.
					t.Fatal(err)
				}
				// A late status read must not resurrect a stopped workflow's reservation.
				if err = s.UpdateGPTPayOrder(o.Order); err != nil {
					t.Fatal(err)
				}
				if err = s.ReserveProCard("competitor@example.com", cardID, false); (err == nil) != (outcome == "failed") {
					t.Fatalf("cleanup released uncertain/paid occupancy or retained a failure: %v", err)
				}
			})
		}
	}
}

func TestProAutoCardHoldDoesNotCaptureManualOrOlderOrder(t *testing.T) {
	for _, mode := range []string{"manual", "older-attempt", "stopped", "different-card"} {
		t.Run(mode, func(t *testing.T) {
			s := transferStore(t)
			email := "retry@example.com"
			transferSource(t, s, email)
			capacityCard(t, s, "card")
			a := model.ProAutoState{ID: "flow", CardID: "card", Status: "running"}
			o := transferOrder(email, a.PaymentOrderID(), "submitting")
			o.Order.CardID = "card"
			switch mode {
			case "manual":
				o.Order.ID = "manual-order"
			case "older-attempt":
				a.RetryAttempts = 1
			case "stopped":
				a.Status = "interrupted"
			case "different-card":
				a.CardID = "another-card"
			}
			if _, err := s.UpdateProAccount(email, func(p *model.MailAccountProfile) { p.ProAuto = a }); err != nil {
				t.Fatal(err)
			}
			if err := s.CreateGPTPayOrder(o.Order, o.Snapshot); err != nil {
				t.Fatal(err)
			}
			o.Order.Status = "failed"
			if err := s.UpdateGPTPayOrder(o.Order); err != nil {
				t.Fatal(err)
			}
			card, _, err := s.GPTPayCard("card")
			if err != nil || card.PendingAccounts != 0 || !card.Available {
				t.Fatal("unrelated failed order acquired a reservation", card, err)
			}
		})
	}
}
