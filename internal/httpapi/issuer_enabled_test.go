package httpapi

import (
	"context"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

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
