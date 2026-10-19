package store

import (
	"testing"

	"chatgpt-space-merge/internal/model"
)

func TestEJHDeletedOccupancyDetailsAndRecovery(t *testing.T) {
	for _, status := range []string{"reserved", "failed", "processing", "manual_review", "submission_unknown", "success"} {
		t.Run(status, func(t *testing.T) {
			dir := t.TempDir()
			s, err := Open(dir)
			if err != nil {
				t.Fatal(err)
			}
			card := ejhFixtureCard(t, s)
			const email = "deleted-card@example.com"
			transferSource(t, s, email)
			if status == "reserved" {
				if err = s.ReserveProCard(email, card.ID, false); err != nil {
					t.Fatal(err)
				}
			} else {
				o := transferOrder(email, "deleted-payment", status)
				o.Order.CardID = card.ID
				if err = s.CreateGPTPayOrder(o.Order, o.Snapshot); err != nil {
					t.Fatal(err)
				}
			}
			// Reproduce legacy deletion without rewriting financial records.
			if _, err = s.db.Exec(`DELETE FROM mail_accounts WHERE email=?`, email); err != nil {
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
			got, _, err := s.GPTPayCard(card.ID)
			if err != nil {
				t.Fatal(err)
			}
			free := status == "reserved" || status == "failed"
			if got.Available != free {
				t.Fatalf("available=%v, want %v: %+v", got.Available, free, got)
			}
			if free {
				if got.ActiveAccounts != 0 || got.PendingAccounts != 0 || len(got.Occupants) != 0 {
					t.Fatalf("stale occupancy: %+v", got)
				}
			} else {
				if got.ActiveAccounts != 1 || len(got.Occupants) != 1 {
					t.Fatalf("missing occupancy: %+v", got)
				}
				occupant := got.Occupants[0]
				if !occupant.Deleted || occupant.Status != status || occupant.Email != email || occupant.OrderID != "deleted-payment" {
					t.Fatal(occupant)
				}
			}
			if status != "reserved" {
				if _, _, err = s.GPTPayOrder("deleted-payment"); err != nil {
					t.Fatal("history lost", err)
				}
			}
		})
	}
}

func TestProDeleteReleasesOnlyStoppedPrepaymentReservation(t *testing.T) {
	s := transferStore(t)
	card := ejhFixtureCard(t, s)
	const email = "stopped-reservation@example.com"
	transferSource(t, s, email)
	if err := s.ReserveProCard(email, card.ID, false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateProAccount(email, func(p *model.MailAccountProfile) { p.ProAuto.Status = "running" }); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteProAccount(email); err == nil {
		t.Fatal("deleted running reservation")
	}
	if err := s.DeleteMailAccount(email); err == nil {
		t.Fatal("mail deletion bypassed task guard")
	}
	if _, err := s.UpdateProAccount(email, func(p *model.MailAccountProfile) { p.ProAuto.Status = "interrupted" }); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteProAccount(email); err != nil {
		t.Fatal(err)
	}
	got, _, err := s.GPTPayCard(card.ID)
	if err != nil || !got.Available || got.PendingAccounts != 0 || got.ActiveAccounts != 0 {
		t.Fatal(got, err)
	}
	if err := s.ReserveProCard("next@example.com", card.ID, false); err != nil {
		t.Fatal(err)
	}
}

func TestProDeleteCannotBypassUnresolvedUsage(t *testing.T) {
	for _, scope := range []string{"pro", "mail"} {
		t.Run(scope, func(t *testing.T) {
			s := transferStore(t)
			card := ejhFixtureCard(t, s)
			const email = "unresolved-delete@example.com"
			transferSource(t, s, email)
			o := transferOrder(email, "unknown-delete", "manual_review")
			o.Order.CardID = card.ID
			if err := s.CreateGPTPayOrder(o.Order, o.Snapshot); err != nil {
				t.Fatal(err)
			}
			// Legacy/inconsistent active flags must not hide the durable usage hold.
			if _, err := s.db.Exec(`UPDATE gptpay_orders SET active=0 WHERE id=?`, o.Order.ID); err != nil {
				t.Fatal(err)
			}
			if scope == "mail" {
				if _, err := s.db.Exec(`UPDATE mail_accounts SET profile=json_set(profile,'$.management_scope','mail') WHERE email=?`, email); err != nil {
					t.Fatal(err)
				}
			} else if err := s.DeleteProAccount(email); err == nil {
				t.Fatal("deleted unresolved usage")
			}
			if err := s.DeleteMailAccount(email); err == nil {
				t.Fatal("mail deletion bypassed payment guard")
			}
			if _, _, err := s.MailAccountCredential(email); err != nil {
				t.Fatal(err)
			}
		})
	}
}
