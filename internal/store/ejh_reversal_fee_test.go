package store

import (
	"chatgpt-space-merge/internal/ejh"
	"testing"
	"time"
)

func TestEJHReversalFeeSettingsPersistence(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	cfg, _, err := s.EJHSettings()
	if err != nil || cfg.ReversalFeePercent != 6 {
		t.Fatalf("default: %+v %v", cfg, err)
	}
	cfg.ReversalFeePercent = 6.25
	if _, err = s.SaveEJHSettings(cfg, ""); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	cfg, _, err = s.EJHSettings()
	if err != nil || cfg.ReversalFeePercent != 6.25 {
		t.Fatalf("restart: %+v %v", cfg, err)
	}
	cfg.ReversalFeePercent = 0
	if _, err = s.SaveEJHSettings(cfg, ""); err != nil {
		t.Fatal(err)
	}
	got, _, err := s.EJHSettings()
	if err != nil || got.ReversalFeePercent != 0 {
		t.Fatalf("zero: %+v %v", got, err)
	}
	// Older saved profiles have no percentage field and receive the default.
	if _, err = s.db.Exec(`UPDATE ejh_settings SET profile=json_remove(profile,'$.reversal_fee_percent')`); err != nil {
		t.Fatal(err)
	}
	got, _, err = s.EJHSettings()
	if err != nil || got.ReversalFeePercent != ejh.DefaultSettings().ReversalFeePercent {
		t.Fatalf("legacy: %+v %v", got, err)
	}
}

func TestEJHReversalFeeLocksOnlyCompletedReturns(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { s.Close() }()
	card := ejhFixtureCard(t, s)
	cfg, _, _ := s.EJHSettings()
	cfg.SingleActiveAccount = false
	if _, err = s.SaveEJHSettings(cfg, ""); err != nil {
		t.Fatal(err)
	}
	for i, id := range []string{"completed", "requested", "opened"} {
		email := id + "@example.com"
		transferSource(t, s, email)
		o := transferOrder(email, id, "success")
		o.Order.CardID = card.ID
		o.Order.CreatedAt = time.Now().Add(time.Duration(i-10) * time.Hour)
		o.Order.UpdatedAt = o.Order.CreatedAt
		if err = s.CreateGPTPayOrder(o.Order, o.Snapshot); err != nil {
			t.Fatal(err)
		}
	}
	if err = s.EJHMarkRequested("requested@example.com", "test", time.Now()); err != nil {
		t.Fatal(err)
	}
	ejhReceiveForOrder(t, s, card.ID, "completed")
	check := func(id string, want *float64, state string) {
		t.Helper()
		cases, e := s.EJHCases()
		if e != nil {
			t.Fatal(e)
		}
		for _, c := range cases {
			if c.ID != id {
				continue
			}
			if c.State != state || (c.ReversalFee == nil) != (want == nil) {
				t.Fatalf("%s: state=%s fee=%+v", id, c.State, c.ReversalFee)
			}
			if want != nil && (c.ReversalFee.Percent != *want || c.ReversalFee.LockedAt.IsZero()) {
				t.Fatalf("%s: fee=%+v want=%v", id, c.ReversalFee, *want)
			}
			for _, r := range c.Returns {
				if (r.ReversalFee == nil) != (want == nil) || want != nil && r.ReversalFee.Percent != *want {
					t.Fatal("case trade rate inconsistent", r.ReversalFee)
				}
			}
			trades, e := EJHRecords[ejh.TradeRecord](s, "trade")
			if e != nil {
				t.Fatal(e)
			}
			ejh.ApplyReversalFeeRates(trades, cases)
			for _, r := range trades {
				if r.OrderID == id && ((r.ReversalFee == nil) != (want == nil) || want != nil && r.ReversalFee.Percent != *want) {
					t.Fatal("card/finance rate inconsistent", r.ReversalFee)
				}
			}
			return
		}
		t.Fatal("case missing", id)
	}
	six, three, zero := 6.0, 3.25, 0.0
	check("completed", &six, "returned")
	check("requested", nil, "waiting")
	check("opened", nil, "opened")
	cfg.ReversalFeePercent = three
	if _, err = s.SaveEJHSettings(cfg, ""); err != nil {
		t.Fatal(err)
	}
	check("completed", &six, "returned")
	check("requested", nil, "waiting")
	ejhReceiveForOrder(t, s, card.ID, "requested")
	check("requested", &three, "returned")
	// Supplier status 1 -> 2 briefly invalidates matching before reconciliation.
	// A settings save in that window must not erase the completed order's rate.
	trades, err := EJHRecords[ejh.TradeRecord](s, "trade")
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range trades {
		if r.OrderID == "completed" {
			r.Trade.Status = "2"
			if err = s.EJHSaveTrade(card.ID, r.Trade); err != nil {
				t.Fatal(err)
			}
		}
	}
	cfg.ReversalFeePercent = zero
	if _, err = s.SaveEJHSettings(cfg, ""); err != nil {
		t.Fatal(err)
	}
	check("requested", &three, "returned")
	if err = s.EJHReconcile(); err != nil {
		t.Fatal(err)
	}
	check("completed", &six, "returned")
	ejhReceiveForOrder(t, s, card.ID, "opened")
	check("opened", &zero, "returned")
	// Repeated evidence ingestion and startup must not reprice a completed order.
	ejhReceiveForOrder(t, s, card.ID, "completed")
	s.Close()
	s, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	check("completed", &six, "returned")
	check("requested", &three, "returned")
	check("opened", &zero, "returned")
	// An explicit correction to pending resumes the current configurable rate.
	if _, err = s.SetEJHCaseState("completed", caseStateInput(t, s, "completed", "waiting")); err != nil {
		t.Fatal(err)
	}
	check("completed", nil, "waiting")
	if _, err = s.SetEJHCaseState("completed", caseStateInput(t, s, "completed", "auto")); err != nil {
		t.Fatal(err)
	}
	check("completed", &zero, "returned")
}

func TestEJHReversalFeeLegacyBackfillAndAtomicSettings(t *testing.T) {
	s := transferStore(t)
	card := ejhFixtureCard(t, s)
	transferSource(t, s, "legacy-fee@example.com")
	o := transferOrder("legacy-fee@example.com", "legacy-fee", "success")
	o.Order.CardID = card.ID
	if err := s.CreateGPTPayOrder(o.Order, o.Snapshot); err != nil {
		t.Fatal(err)
	}
	ejhReceiveForOrder(t, s, card.ID, o.Order.ID)
	// Simulate a returned order saved by the previous release without a snapshot.
	if _, err := s.db.Exec("DELETE FROM ejh_reversal_fee_rates"); err != nil {
		t.Fatal(err)
	}
	cfg, _, _ := s.EJHSettings()
	cfg.ReversalFeePercent = 2.5
	if _, err := s.SaveEJHSettings(cfg, "short"); err == nil {
		t.Fatal("invalid secret accepted")
	}
	var n int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM ejh_reversal_fee_rates").Scan(&n); err != nil || n != 0 {
		t.Fatal("invalid save changed snapshots", n, err)
	}
	if _, err := s.SaveEJHSettings(cfg, ""); err != nil {
		t.Fatal(err)
	}
	rows, err := s.EJHCases()
	if err != nil || len(rows) != 1 || rows[0].ReversalFee == nil || rows[0].ReversalFee.Percent != 6 {
		t.Fatal("legacy order repriced", rows, err)
	}
	// Failed settings write rolls the backfill back with it.
	if _, err = s.db.Exec("DELETE FROM ejh_reversal_fee_rates; CREATE TRIGGER reject_fee_update BEFORE UPDATE ON ejh_settings BEGIN SELECT RAISE(ABORT,'fixture'); END"); err != nil {
		t.Fatal(err)
	}
	cfg.ReversalFeePercent = 1
	if _, err = s.SaveEJHSettings(cfg, ""); err == nil {
		t.Fatal("trigger did not reject")
	}
	if err = s.db.QueryRow("SELECT COUNT(*) FROM ejh_reversal_fee_rates").Scan(&n); err != nil || n != 0 {
		t.Fatal("failed write left snapshots", n, err)
	}
	got, _, err := s.EJHSettings()
	if err != nil || got.ReversalFeePercent != 2.5 {
		t.Fatal("failed write changed rate", got.ReversalFeePercent, err)
	}
}
