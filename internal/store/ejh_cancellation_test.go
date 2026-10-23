package store

import (
	"testing"
	"time"

	"chatgpt-space-merge/internal/ejh"
)

func TestEJHCancellationRecoveredByCardSyncAfterRestart(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	remote := ejh.Card{OrderNo: "restart-cancel", Number: "4242424242424242", CVV: "123", Validity: "1299", Status: "3"}
	card, err := s.ImportEJHCard(remote, true)
	if err != nil {
		t.Fatal(err)
	}
	op := ejh.Operation{ID: "old-unknown-cancel", Method: "cancel", CardID: card.ID, OrderNo: remote.OrderNo, Status: "unknown", Error: "EJH 300002", CreatedAt: time.Now()}
	if err = s.EJHBeginOperation(op); err != nil {
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
	pending, err := s.EJHPendingCancellations(time.Now(), 10)
	if err != nil || len(pending) != 1 {
		t.Fatal("restart lost claim", pending, err)
	}
	for _, state := range []string{"8", "2"} {
		remote.Status = state
		if _, err = s.ImportEJHCard(remote, false); err != nil {
			t.Fatal(err)
		}
		if err = s.EJHGet("operation", op.ID, &op); err != nil {
			t.Fatal(err)
		}
		want := "processing"
		if state == "2" {
			want = "completed"
		}
		if op.Status != want || op.Error != "" {
			t.Fatal("sync did not reconcile old operation", op)
		}
	}
	pending, err = s.EJHPendingCancellations(time.Now(), 10)
	if err != nil || len(pending) != 0 {
		t.Fatal("terminal claim retained", pending, err)
	}
}

func TestEJHRejectedIncreaseRecoveryAndExternalCancellation(t *testing.T) {
	for _, tc := range []struct {
		name, failure string
		release       bool
	}{
		{"documented_rejection", "EJH 800006: old message", true},
		{"ambiguous_failure", "EJH 300002: unknown result", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, err := Open(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			cfg := ejh.DefaultSettings()
			cfg.Enabled = true
			cfg.AppKey = "fixture"
			if _, err = s.SaveEJHSettings(cfg, "fixture-secret"); err != nil {
				t.Fatal(err)
			}
			remote := ejh.Card{OrderNo: "external-cancel", Number: "4242424242424242", CVV: "123", Validity: "1299", Status: "3", Locked: "0"}
			card, err := s.ImportEJHCard(remote, true)
			if err != nil {
				t.Fatal(err)
			}
			op := ejh.Operation{ID: "failed-increase", Method: "increase", CardID: card.ID, OrderNo: remote.OrderNo, Status: "unknown", Error: tc.failure, CreatedAt: time.Now()}
			if err = s.EJHBeginOperation(op); err != nil {
				t.Fatal(err)
			}
			before, _, err := s.GPTPayCard(card.ID)
			if err != nil || !before.IssuerLocked || before.IssuerFrozen {
				t.Fatal("pending operation is not an issuer freeze", before, err)
			}
			remote.Status = "8"
			for i := 0; i < 2; i++ {
				if _, err = s.ImportEJHCard(remote, false); err != nil {
					t.Fatal(err)
				}
			}
			if err = s.EJHGet("operation", op.ID, &op); err != nil {
				t.Fatal(err)
			}
			want := "unknown"
			if tc.release {
				want = "failed"
			}
			if op.Status != want {
				t.Fatalf("increase status=%s want=%s", op.Status, want)
			}
			pending, err := s.EJHPendingCancellations(time.Now(), 10)
			if err != nil {
				t.Fatal(err)
			}
			if !tc.release {
				if len(pending) != 0 {
					t.Fatal("ambiguous operation claim overwritten")
				}
				return
			}
			if len(pending) != 1 || pending[0].OrderNo != remote.OrderNo || pending[0].Status != "processing" {
				t.Fatal("external cancellation not adopted once", pending)
			}
			local, _, err := s.GPTPayCard(card.ID)
			if err != nil || local.IssuerCancelStatus != "processing" || local.IssuerFrozen {
				t.Fatal("incorrect cancellation state", local, err)
			}
			remote.Status = "2"
			if _, err = s.ImportEJHCard(remote, false); err != nil {
				t.Fatal(err)
			}
			var cancel ejh.Operation
			if err = s.EJHGet("operation", pending[0].ID, &cancel); err != nil || cancel.Status != "completed" {
				t.Fatal(cancel, err)
			}
			pending, err = s.EJHPendingCancellations(time.Now(), 10)
			if err != nil || len(pending) != 0 {
				t.Fatal("completed cancellation still scheduled", pending, err)
			}
		})
	}
}
