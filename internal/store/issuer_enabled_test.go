package store

import (
	"strings"
	"testing"

	"chatgpt-space-merge/internal/ejh"
	"chatgpt-space-merge/internal/gptpay"
	"chatgpt-space-merge/internal/nexovcc"
)

// Existing finance fixtures model imported cards from a configured merchant.
// Preserve tests that have already saved an explicit merchant configuration.
func ensureIssuerFixtureEnabled(t *testing.T, s *Store, provider string) {
	t.Helper()
	var count int
	if err := s.db.QueryRow(`SELECT count(*) FROM ` + provider + `_settings`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		return
	}
	if provider == "ejh" {
		cfg := ejh.DefaultSettings()
		cfg.Enabled, cfg.AppKey = true, "fixture-app"
		if _, err := s.SaveEJHSettings(cfg, "fixture-secret"); err != nil {
			t.Fatal(err)
		}
	} else {
		cfg := nexovcc.Defaults()
		cfg.Enabled = true
		if _, err := s.SaveNexovccSettings(cfg, nexovcc.Credentials{Login: "fixture-global-login", Password: "fixture-pass", FundPassword: "fixture-fund"}, false); err != nil {
			t.Fatal(err)
		}
	}
}

func setIssuerFixtureEnabled(t *testing.T, s *Store, provider string, enabled bool) {
	t.Helper()
	if provider == "ejh" {
		cfg, _, err := s.EJHSettings()
		if err != nil {
			t.Fatal(err)
		}
		cfg.Enabled = enabled
		if _, err = s.SaveEJHSettings(cfg, ""); err != nil {
			t.Fatal(err)
		}
	} else {
		cfg, _, err := s.NexovccSettings()
		if err != nil {
			t.Fatal(err)
		}
		cfg.Enabled = enabled
		if _, err = s.SaveNexovccSettings(cfg, nexovcc.Credentials{}, false); err != nil {
			t.Fatal(err)
		}
	}
}

func TestIssuerDisabledBlocksExistingCardsAndPaymentAdmission(t *testing.T) {
	for _, provider := range []string{"ejh", "nexovcc"} {
		t.Run(provider, func(t *testing.T) {
			s := transferStore(t)
			var id string
			if provider == "ejh" {
				id = ejhFixtureCard(t, s).ID
			} else {
				id = nexStoreCard(t, s).ID
			}
			capacityCard(t, s, "alias")
			unrelated := gptpay.CardSecret{Number: "5555555555554444", Month: 12, Year: 2099, CVV: "123"}
			if _, err := s.SaveGPTPayCard(gptpay.Card{ID: "manual", Enabled: true}, unrelated); err != nil {
				t.Fatal(err)
			}
			for _, cardID := range []string{id, "alias"} {
				card, _, err := s.GPTPayCard(cardID)
				if err != nil || !card.Available || card.IssuerDisabled {
					t.Fatalf("enabled card unavailable: %+v %v", card, err)
				}
			}
			// Disabling after the automatic flow reserved a card must still stop
			// the payment. The direct/manual path is checked in the same loop.
			if err := s.ReserveProCard("reserved@example.com", id, false); err != nil {
				t.Fatal(err)
			}
			setIssuerFixtureEnabled(t, s, provider, false)
			for _, cardID := range []string{id, "alias"} {
				card, secret, err := s.GPTPayCard(cardID)
				if err != nil || !card.Enabled || card.Available || !card.IssuerDisabled {
					t.Fatalf("disabled merchant card still selectable: %+v %v", card, err)
				}
				// Editing a disabled card must not persist a stale merchant flag.
				if _, err = s.SaveGPTPayCard(card, secret); err != nil {
					t.Fatal(err)
				}
				if err = s.ReserveProCard("next@example.com", cardID, false); err == nil || !strings.Contains(err.Error(), "卡台已停用") {
					t.Fatalf("reservation did not reject merchant: %v", err)
				}
				for _, email := range []string{"reserved@example.com", "manual@example.com"} {
					o := transferOrder(email, "blocked-"+cardID+email, "submitting")
					o.Order.CardID = cardID
					if err = s.CreateGPTPayOrder(o.Order, o.Snapshot); err == nil || !strings.Contains(err.Error(), "卡台已停用") {
						t.Fatalf("payment bypassed merchant switch: %v", err)
					}
				}
			}
			rows, total, err := s.GPTPayCards(20, 0, true)
			if err != nil || total != 1 || len(rows) != 1 || rows[0].ID != "manual" {
				t.Fatalf("available filter incorrect: %+v %d %v", rows, total, err)
			}
			if err = s.ReleaseProCard("reserved@example.com"); err != nil {
				t.Fatal(err)
			}
			// Preserve a user's individual disabled card when the merchant returns.
			alias, secret, _ := s.GPTPayCard("alias")
			alias.Enabled = false
			if _, err = s.SaveGPTPayCard(alias, secret); err != nil {
				t.Fatal(err)
			}
			setIssuerFixtureEnabled(t, s, provider, true)
			card, _, err := s.GPTPayCard(id)
			if err != nil || !card.Available || card.IssuerDisabled {
				t.Fatalf("reenabling did not restore eligibility: %+v %v", card, err)
			}
			alias, _, err = s.GPTPayCard("alias")
			if err != nil || alias.Enabled || alias.Available || alias.IssuerDisabled {
				t.Fatalf("individual switch changed: %+v %v", alias, err)
			}
			o := transferOrder("submitted@example.com", "submitted", "processing")
			o.Order.CardID = id
			if err = s.CreateGPTPayOrder(o.Order, o.Snapshot); err != nil {
				t.Fatal(err)
			}
			setIssuerFixtureEnabled(t, s, provider, false)
			o.Order.Status = "success"
			if err = s.UpdateGPTPayOrder(o.Order); err != nil {
				t.Fatalf("merchant switch prevented submitted order reconciliation: %v", err)
			}
			// Missing merchant configuration also fails closed for bound cards.
			if _, err = s.db.Exec(`DELETE FROM ` + provider + `_settings`); err != nil {
				t.Fatal(err)
			}
			card, _, err = s.GPTPayCard(id)
			if err != nil || !card.IssuerDisabled || card.Available {
				t.Fatalf("missing settings allowed merchant: %+v %v", card, err)
			}
		})
	}
}
