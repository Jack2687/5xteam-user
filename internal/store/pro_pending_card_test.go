package store

import (
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"chatgpt-space-merge/internal/gptpay"
)

func TestProNormalPendingBlocksPhysicalAliasesAndNewPayments(t *testing.T) {
	for _, issuer := range []string{"ejh", "nexovcc"} {
		t.Run(issuer, func(t *testing.T) {
			s := transferStore(t)
			var id string
			if issuer == "ejh" {
				id = ejhFixtureCard(t, s).ID
			} else {
				id = nexStoreCard(t, s).ID
			}
			setPending := func(value bool) {
				t.Helper()
				var err error
				if issuer == "ejh" {
					_, err = s.SetEJHCardNormalPending(id, value)
				} else {
					_, err = s.SetNexovccCardNormalPending(id, value)
				}
				if err != nil {
					t.Fatal(err)
				}
			}
			before, secret, err := s.GPTPayCard(id)
			if err != nil || !before.Available {
				t.Fatal("fixture unavailable", err)
			}
			alias, err := s.SaveGPTPayCard(gptpay.Card{ID: "physical-alias", Enabled: true}, secret)
			if err != nil {
				t.Fatal(err)
			}
			email := "pending-test@example.com"
			transferSource(t, s, email)
			if err = s.ReserveProCard(email, id, false); err != nil {
				t.Fatal(err)
			}
			setPending(true)
			// Classification after login reservation must still block order creation.
			order := gptpay.Order{ID: "must-not-submit", CardID: id, Email: email, Status: "created", CreatedAt: time.Now()}
			snapshot := gptpay.Snapshot{Input: gptpay.CreateInput{CardSecret: secret}}
			if err = s.CreateGPTPayOrder(order, snapshot); err == nil || !strings.Contains(err.Error(), "正常待定") {
				t.Fatal("reserved card bypassed category", err)
			}
			if _, _, err = s.GPTPayOrder(order.ID); !errors.Is(err, sql.ErrNoRows) {
				t.Fatal("blocked order persisted", err)
			}
			if err = s.ReleaseProCard(email); err != nil {
				t.Fatal(err)
			}
			for _, cardID := range []string{id, alias.ID} {
				card, _, err := s.GPTPayCard(cardID)
				if err != nil || card.Available || !card.NormalPending || !card.Enabled || card.IssuerStatus != before.IssuerStatus {
					t.Fatal("pending physical card still usable", cardID, err)
				}
				if err = s.ReserveProCard(email, cardID, false); err == nil || !strings.Contains(err.Error(), "正常待定") {
					t.Fatal("alias reservation bypass", cardID, err)
				}
			}
			cards, total, err := s.GPTPayCards(100, 0, true)
			if err != nil || total != 0 || len(cards) != 0 {
				t.Fatal("pending cards appeared in available list", total, err)
			}
			all, total, err := s.GPTPayCards(100, 0, false)
			if err != nil || total != 2 || len(all) != 2 {
				t.Fatal("management lost pending cards", total, err)
			}
			setPending(false)
			for _, cardID := range []string{id, alias.ID} {
				card, _, err := s.GPTPayCard(cardID)
				if err != nil || !card.Available || card.NormalPending {
					t.Fatal("normal did not restore eligibility", cardID, err)
				}
			}
			// An existing submitted order still reconciles after moving its card.
			if err = s.CreateGPTPayOrder(order, snapshot); err != nil {
				t.Fatal(err)
			}
			setPending(true)
			order.Status = "success"
			if err = s.UpdateGPTPayOrder(order); err != nil {
				t.Fatal("category blocked existing order result", err)
			}
			saved, _, err := s.GPTPayOrder(order.ID)
			if err != nil || saved.Status != "success" {
				t.Fatal("existing result lost", err)
			}
		})
	}
}
