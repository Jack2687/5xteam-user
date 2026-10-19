package store

import (
	"chatgpt-space-merge/internal/ejh"
	"chatgpt-space-merge/internal/gptpay"
	"chatgpt-space-merge/internal/model"
	"testing"
)

func TestProCardTypeLegacyAndIssuerRefresh(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	c := ejhFixtureCard(t, s)
	if c.CardType != "ejh" {
		t.Fatal("new issuer card must be EJH", c)
	}
	// Emulate pre-feature data: source exists, classification tables do not.
	if _, err = s.db.Exec(`DROP TABLE pro_card_types; DROP TABLE pro_card_identities;`); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	c = ejhFixtureCard(t, s)
	if c.CardType != "other" || c.SingleActiveAccount {
		t.Fatal("refresh must preserve legacy Other", c)
	}
	c, secret, err := s.GPTPayCard(c.ID)
	if err != nil {
		t.Fatal(err)
	}
	c.CardType = "ejh"
	if _, err = s.SaveGPTPayCard(c, secret); err != nil {
		t.Fatal(err)
	}
	c = ejhFixtureCard(t, s)
	if c.CardType != "ejh" || !c.SingleActiveAccount {
		t.Fatal("explicit EJH lost", c)
	}
}

func TestProCardTypeManualAliasesAndRemoval(t *testing.T) {
	s := transferStore(t)
	capacityCard(t, s, "manual")
	c, secret, err := s.GPTPayCard("manual")
	if err != nil || c.CardType != "other" {
		t.Fatal(c, err)
	}
	c.CardType = "ejh"
	if _, err = s.SaveGPTPayCard(c, secret); err != nil {
		t.Fatal(err)
	}
	capacityCard(t, s, "alias")
	c, _, err = s.GPTPayCard("alias")
	if err != nil || c.CardType != "ejh" || !c.SingleActiveAccount || !c.Available {
		t.Fatal(c, err)
	}
	email := "manual@example.com"
	transferSource(t, s, email)
	o := transferOrder(email, "manual-order", "success")
	o.Order.CardID = "manual"
	if err = s.CreateGPTPayOrder(o.Order, o.Snapshot); err != nil {
		t.Fatal(err)
	}
	if err = s.ReserveProCard("next@example.com", "alias", false); err == nil {
		t.Fatal("manual EJH alias bypass")
	}
	c.CardType = "other"
	if _, err = s.SaveGPTPayCard(c, secret); err == nil {
		t.Fatal("active card downgraded")
	}
	cases, err := s.EJHCases()
	if err != nil || len(cases) != 1 || cases[0].CardType != "ejh" || cases[0].EJHCardID != "manual" {
		t.Fatal(cases, err)
	}
	if _, err = s.UpdateProAccount(email, func(p *model.MailAccountProfile) { p.RemoveStatus = "completed" }); err != nil {
		t.Fatal(err)
	}
	if _, err = s.SaveGPTPayCard(c, secret); err == nil {
		t.Fatal("removal allowed type downgrade before return")
	}
	ejhReceiveForOrder(t, s, "manual", o.Order.ID)
	if _, err = s.SaveGPTPayCard(c, secret); err != nil {
		t.Fatal(err)
	}
	cases, err = s.EJHCases()
	if err != nil || cases[0].CardType != "other" || cases[0].EJHCardID != "" {
		t.Fatal(cases, err)
	}
	if err = s.DeleteGPTPayCard("manual"); err != nil {
		t.Fatal(err)
	}
	if err = s.DeleteGPTPayCard("alias"); err != nil {
		t.Fatal(err)
	}
	capacityCard(t, s, "readded")
	c, _, err = s.GPTPayCard("readded")
	if err != nil || c.CardType != "other" || c.OpenedAccounts != 1 {
		t.Fatal(c, err)
	}
	if _, err = s.SaveGPTPayCard(gptpay.Card{ID: "invalid", CardType: "bogus"}, secret); err == nil {
		t.Fatal("invalid type accepted")
	}
	cfg := ejh.DefaultSettings()
	cfg.SingleActiveAccount = false
	if _, err = s.SaveEJHSettings(cfg, ""); err != nil {
		t.Fatal(err)
	}
}
