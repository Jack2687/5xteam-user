package store

import (
	"chatgpt-space-merge/internal/ejh"
	"testing"
	"time"
)

func TestEJHTemplateCurrencyRoundTrip(t *testing.T) {
	s := transferStore(t)
	cfg := ejh.DefaultSettings()
	cfg.Templates = []ejh.Template{{ID: "currency-fixture", Name: "currency fixture", ValidDays: 30,
		OpenInput: ejh.OpenInput{CardType: "fixture", CardAmount: "20", CardCurrency: "CNH", BillCurrency: "EUR", UseTime: 1, MCC: "ecom"}}}
	for _, code := range []string{"CNH", "HRK", "ZZZ"} {
		cfg.Templates[0].CardCurrency = code
		if _, err := s.SaveEJHSettings(cfg, ""); err != nil {
			t.Fatal(err)
		}
		got, _, err := s.EJHSettings()
		if err != nil {
			t.Fatal(err)
		}
		if len(got.Templates) != 1 || got.Templates[0].CardCurrency != code || got.Templates[0].BillCurrency != "EUR" {
			t.Fatalf("currency changed on save/read: %+v", got.Templates)
		}
		input := got.Templates[0].Input(time.Now())
		if input.CardCurrency != code || input.BillCurrency != "EUR" {
			t.Fatalf("currency changed in open input: %+v", input)
		}
	}
}
