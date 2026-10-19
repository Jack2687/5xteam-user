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
