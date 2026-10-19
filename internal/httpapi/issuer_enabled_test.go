package httpapi

import (
	"context"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"chatgpt-space-merge/internal/ejh"
	"chatgpt-space-merge/internal/nexovcc"
	"chatgpt-space-merge/internal/store"
)

func enableEJHPaymentFixture(t *testing.T, st *store.Store) {
	t.Helper()
	cfg, _, err := st.EJHSettings()
	if err != nil {
		t.Fatal(err)
	}
	cfg.Enabled, cfg.AppKey = true, "fixture-app"
	cfg.URL = "https://127.0.0.1:1"
	if _, err = st.SaveEJHSettings(cfg, "fixture-secret"); err != nil {
		t.Fatal(err)
	}
}

func TestDisabledIssuerAllowsManualCardAdministration(t *testing.T) {
	t.Run("ejh", func(t *testing.T) {
		s, calls := ejhTestServer(t, 200)
		cfg, _, err := s.store.EJHSettings()
		if err != nil {
			t.Fatal(err)
		}
		cfg.Enabled = false
		if _, err = s.store.SaveEJHSettings(cfg, ""); err != nil {
			t.Fatal(err)
		}
		input := ejh.OpenInput{CardType: "1", CardAmount: "10", CardCurrency: "USD", BillCurrency: "USD", UseTime: 1, MCC: "ecom", ActiveDate: time.Now().AddDate(0, 0, 30).Format("2006-01-02")}
		if _, err = s.ejhOpenLocked(context.Background(), "disabled-auto-request-123", input, true); err == nil {
			t.Fatal("disabled auto issued card")
		}
		if calls.Load() != 0 {
			t.Fatal("automatic request reached supplier")
		}
		op, err := s.ejhOpenLocked(context.Background(), "disabled-manual-request-123", input, false)
		if err != nil || op.Status != "completed" || calls.Load() != 1 {
			t.Fatal("manual open blocked", op.Status, err, calls.Load())
		}
		card, _, err := s.store.GPTPayCard(op.CardID)
		if err != nil || card.Available {
			t.Fatal("manual administration made disabled issuer available for Pro", err)
		}
	})
	t.Run("nexovcc", func(t *testing.T) {
		s, f := nexFixtureServer(t)
		cfg, _, err := s.store.NexovccSettings()
		if err != nil {
			t.Fatal(err)
		}
		cfg.Enabled = false
		if _, err = s.store.SaveNexovccSettings(cfg, nexovcc.Credentials{}, false); err != nil {
			t.Fatal(err)
		}
		if _, err = s.openNexovccLocked(context.Background(), "disabled-auto-request-123", "template", true); err == nil || f.opens.Load() != 0 {
			t.Fatal("disabled auto issued card", err)
		}
		op, err := s.openNexovccLocked(context.Background(), "disabled-manual-request-123", "template", false)
		if err != nil || op.Status != "succeeded" || f.opens.Load() != 1 {
			t.Fatal("disabled manual open blocked", op.Status, err)
		}
		if err = s.syncNexovccLocked(context.Background()); err != nil {
			t.Fatal("disabled manual sync blocked", err)
		}
		card, _, err := s.store.GPTPayCard(op.CardID)
		if err != nil || card.Available {
			t.Fatal("manual administration made disabled issuer available for Pro", err)
		}
	})
}

func enableNexovccPaymentFixture(t *testing.T, st *store.Store) {
	t.Helper()
	cfg := nexovcc.Defaults()
	cfg.Enabled = true
	if _, err := st.SaveNexovccSettings(cfg, nexovcc.Credentials{Login: "fixture", Password: "fixture", FundPassword: "fixture"}, false); err != nil {
		t.Fatal(err)
	}
}

func TestDisabledIssuerRejectsManualAndAutomaticPurchases(t *testing.T) {
	for _, provider := range []string{"ejh", "nexovcc"} {
		t.Run(provider, func(t *testing.T) {
			var payments atomic.Int32
			s, st, email := gptPayFixture(t, func(w http.ResponseWriter, r *http.Request) {
				payments.Add(1)
				w.WriteHeader(500)
			})
			// No configured/enabled merchant: imported data alone cannot authorize
			// a new payment. The fixture's test-card is a physical alias.
			if provider == "ejh" {
				if _, err := st.ImportEJHCard(ejh.Card{OrderNo: "disabled-fixture", Number: "4242424242424242", Validity: "1299", CVV: "123", Status: "1"}, true); err != nil {
					t.Fatal(err)
				}
			} else {
				if _, err := st.SaveNexovccCard(nexovcc.Card{Serial: "disabled-fixture", Status: "1", Balance: "10.00"}, &nexovcc.PrivateCard{Number: "4242424242424242", Month: 12, Year: 2099, CVV: "123"}); err != nil {
					t.Fatal(err)
				}
			}
			cfg, _, _, err := st.ProSettings()
			if err != nil {
				t.Fatal(err)
			}
			cfg.CardProvider = provider
			if _, err = st.SaveProSettings(cfg, "", ""); err != nil {
				t.Fatal(err)
			}
			for _, strict := range []bool{false, true} {
				cards, err := s.automaticProCards(cfg, strict)
				if err != nil || len(cards) != 0 {
					t.Fatalf("disabled issuer admitted for replenishment: %+v %v", cards, err)
				}
			}
			for _, auto := range []bool{false, true} {
				if _, _, err = s.reserveProStartCard(context.Background(), email, "test-card", auto, false); err == nil || !strings.Contains(err.Error(), "卡台已停用") {
					t.Fatalf("stale automatic selection not rejected: %v", err)
				}
			}
			rec := payCall(t, s.createGPTPayOrder, "email", email, payInput("disabled-issuer-request"))
			if rec.Code != 409 || !strings.Contains(rec.Body.String(), "卡台已停用") || payments.Load() != 0 {
				t.Fatalf("manual purchase reached supplier: %d %s calls=%d", rec.Code, rec.Body.String(), payments.Load())
			}
		})
	}
}
