package store

import (
	"chatgpt-space-merge/internal/ejh"
	"chatgpt-space-merge/internal/gptpay"
	"chatgpt-space-merge/internal/nexovcc"
	"testing"
)

func TestCardPlatformSnapshotsAndProIsolation(t *testing.T) {
	for _, issuer := range []string{"ejh", "nexovcc"} {
		t.Run(issuer, func(t *testing.T) {
			s := transferStore(t)
			ensureIssuerFixtureEnabled(t, s, issuer)
			var id string
			if issuer == "ejh" {
				remote := ejh.Card{OrderNo: "cc", Number: "4242424242424242", Validity: "1299", CVV: "123", Status: "1", CardCurrency: "USD"}
				card, err := s.ImportEJHCard(remote, true, &ejh.Operation{Method: "open", Platform: "cc", TemplateID: "t", TemplateName: "CC original"})
				if err != nil {
					t.Fatal(err)
				}
				id = card.ID
				if _, err = s.ImportEJHCard(remote, false); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := s.BeginNexovccOperation(nexovcc.Operation{ID: "opening", Action: "open", Serial: "cc", Platform: "cc", TemplateID: "t", TemplateName: "CC original"}); err != nil {
					t.Fatal(err)
				}
				card, err := s.SaveNexovccCard(nexovcc.Card{Serial: "cc", Status: "1"}, &nexovcc.PrivateCard{Number: "4242424242424242", CVV: "123", Month: 12, Year: 2099})
				if err != nil {
					t.Fatal(err)
				}
				id = card.ID
				if _, err = s.SaveNexovccCard(nexovcc.Card{Serial: "cc", Status: "1", Platform: "oai", TemplateName: "changed"}, nil); err != nil {
					t.Fatal(err)
				}
			}
			card, secret, err := s.GPTPayCard(id)
			if err != nil || card.Platform != "cc" || card.TemplateName != "CC original" || card.Available {
				t.Fatal("classification/availability", card, err)
			}
			alias, err := s.SaveGPTPayCard(gptpay.Card{ID: "cc-alias", Name: "manual alias", Enabled: true}, secret)
			if err != nil {
				t.Fatal(err)
			}
			if err = s.ReserveProCard("other@example.com", alias.ID, false); err == nil {
				t.Fatal("CC physical alias admitted")
			}
			items, total, err := s.GPTPayCards(1, 0, false, true)
			if err != nil || total != 0 || len(items) != 0 {
				t.Fatal("CC visible in Pro list", total, err)
			}
		})
	}
}

func TestEJHNormalPendingPreservesIssuerAndClearsOnClosure(t *testing.T) {
	s := transferStore(t)
	c := ejhFixtureCard(t, s)
	before, _, _ := s.GPTPayCard(c.ID)
	rec, err := s.SetEJHCardNormalPending(c.ID, true)
	if err != nil || !rec.NormalPending {
		t.Fatal(rec, err)
	}
	remote := rec.Remote
	remote.Note = "refreshed"
	if _, err = s.ImportEJHCard(remote, false); err != nil {
		t.Fatal(err)
	}
	if err = s.EJHGet("card", c.ID, &rec); err != nil || !rec.NormalPending || rec.NormalPendingAt == nil {
		t.Fatal(rec, err)
	}
	after, _, _ := s.GPTPayCard(c.ID)
	if !before.Available || after.Available || !after.NormalPending || after.IssuerStatus != before.IssuerStatus {
		t.Fatal("pending category must block admission while preserving issuer status")
	}
	if rec, err = s.SetEJHCardNormalPending(c.ID, false); err != nil || rec.NormalPending {
		t.Fatal(rec, err)
	}
	if _, err = s.SetEJHCardNormalPending(c.ID, true); err != nil {
		t.Fatal(err)
	}
	remote.Status = "2"
	if _, err = s.ImportEJHCard(remote, false); err != nil {
		t.Fatal(err)
	}
	if err = s.EJHGet("card", c.ID, &rec); err != nil || rec.NormalPending {
		t.Fatal(rec, err)
	}
	if _, err = s.SetEJHCardNormalPending(c.ID, true); err != ErrEJHClosedCategory {
		t.Fatal("closed card categorized", err)
	}
}
