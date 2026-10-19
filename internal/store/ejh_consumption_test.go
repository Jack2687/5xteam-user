package store

import (
	"testing"

	"chatgpt-space-merge/internal/model"
)

func TestEJHConsumptionUsesLatestUserSnapshot(t *testing.T) {
	s := transferStore(t)
	card := ejhFixtureCard(t, s)
	const email = "consumption@example.com"
	transferSource(t, s, email)
	o := transferOrder(email, "consumption-order", "success")
	o.Order.CardID = card.ID
	if err := s.CreateGPTPayOrder(o.Order, o.Snapshot); err != nil {
		t.Fatal(err)
	}
	check := func(want *float64) {
		t.Helper()
		cases, err := s.EJHCases()
		if err != nil || len(cases) != 1 {
			t.Fatalf("read cases: %v, %v", cases, err)
		}
		got := cases[0].ConsumptionTotal
		if (got == nil) != (want == nil) || want != nil && *got != *want {
			t.Fatalf("consumption: got %v want %v", got, want)
		}
	}
	check(nil)
	for _, value := range []float64{67.0766, 72.5, 0} {
		if _, err := s.UpdateProAccount(email, func(p *model.MailAccountProfile) {
			p.CostDownstreamSnapshot = 335.3832
			p.UserCostDownstreamIdentity = "42"
			p.UserCostDownstreamSnapshot = value
		}); err != nil {
			t.Fatal(err)
		}
		check(&value)
	}
	if _, err := s.UpdateProAccount(email, func(p *model.MailAccountProfile) {
		p.ClearProCostSnapshot()
	}); err != nil {
		t.Fatal(err)
	}
	check(nil)
	if _, err := s.db.Exec("DELETE FROM mail_accounts WHERE email=?", email); err != nil {
		t.Fatal(err)
	}
	check(nil)
}
