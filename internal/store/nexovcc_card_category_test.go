package store

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"chatgpt-space-merge/internal/nexovcc"
)

func TestNexovccCardCategorySurvivesSyncAndRestart(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { s.Close() }()
	c := nexStoreCard(t, s)
	before, secret, err := s.GPTPayCard(c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if c, err = s.SetNexovccCardNormalPending(c.ID, true); err != nil || !c.NormalPending || c.NormalPendingAt == nil {
		t.Fatal(c, err)
	}
	marked := *c.NormalPendingAt
	// Supplier responses contain no local category. Refresh a stale snapshot and
	// preserve the category while still accepting new status/balance facts.
	c, err = s.SaveNexovccCard(nexovcc.Card{Serial: c.Serial, Status: "1", Balance: "15.50", BalanceAt: time.Now()}, nil)
	if err != nil || !c.NormalPending || !c.NormalPendingAt.Equal(marked) || c.Balance != "15.50" {
		t.Fatal("sync overwrote classification or balance", c, err)
	}
	after, afterSecret, err := s.GPTPayCard(c.ID)
	if err != nil || !reflect.DeepEqual(secret, afterSecret) || before.Available != after.Available || before.Enabled != after.Enabled || before.IssuerStatus != after.IssuerStatus {
		t.Fatal("classification altered card admission or credentials", err)
	}
	s.Close()
	s, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	c, err = s.NexovccCard(c.ID)
	if err != nil || !c.NormalPending || !c.NormalPendingAt.Equal(marked) {
		t.Fatal("restart lost category", err)
	}
	if c, err = s.SetNexovccCardNormalPending(c.ID, false); err != nil || c.NormalPending {
		t.Fatal("restore failed", err)
	}
	if c, err = s.SetNexovccCardNormalPending(c.ID, true); err != nil {
		t.Fatal(err)
	}
	c.Status = "4"
	if c, err = s.SaveNexovccCard(c, nil); err != nil || c.NormalPending || c.NormalPendingAt != nil {
		t.Fatal("closed card retained active category", err)
	}
	if _, err = s.SetNexovccCardNormalPending(c.ID, true); !errors.Is(err, ErrNexovccClosedCategory) {
		t.Fatal("closed card reclassified as normal", err)
	}
}

func TestNexovccClosedAtIsRecordedOnceAndSurvivesSync(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	c := nexStoreCard(t, s)
	closedAt := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	c, err = s.SaveNexovccCard(nexovcc.Card{Serial: c.Serial, Status: "4", SyncedAt: closedAt}, nil)
	if err != nil || !c.ClosedAt.Equal(closedAt) {
		t.Fatal("closed status timestamp was not captured", c.ClosedAt, err)
	}
	laterSync := closedAt.Add(time.Hour)
	c, err = s.SaveNexovccCard(nexovcc.Card{Serial: c.Serial, Status: "4", SyncedAt: laterSync}, nil)
	if err != nil || !c.ClosedAt.Equal(closedAt) {
		t.Fatal("later card sync changed the closed status timestamp", c.ClosedAt, err)
	}
}

func TestNexovccPendingCategoryBlocksAutomaticWithdrawalAdmission(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	c := nexStoreCard(t, s)
	if _, err = s.SetNexovccCardNormalPending(c.ID, true); err != nil {
		t.Fatal(err)
	}
	op := nexovcc.Operation{ID: "nex-pending-auto1", CardID: c.ID, Serial: c.Serial, Action: "withdraw_all", Amount: "9.00", AutoWithdraw: true, Status: "submitted", CreatedAt: time.Now()}
	if err = s.BeginNexovccOperation(op); err == nil || !strings.Contains(err.Error(), "正常待定") {
		t.Fatal("pending admitted", err)
	}
	ops, err := s.NexovccOperations()
	if err != nil || len(ops) != 0 {
		t.Fatal("failed admission persisted operation", ops, err)
	}
	op.ID, op.Action, op.AutoWithdraw = "nex-manual-freeze1", "freeze", false
	if err = s.BeginNexovccOperation(op); err != nil {
		t.Fatal("manual operation blocked", err)
	}
}
