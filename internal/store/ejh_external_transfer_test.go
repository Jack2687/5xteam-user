package store

import (
	"strings"
	"testing"
	"time"
)

func TestEJHExternalTransferLedger(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { s.Close() }()
	original, err := s.SaveEJHRechargeLedger("3000")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SaveEJHRechargeHistoryMonth("2026-10"); err != nil {
		t.Fatal(err)
	}
	saved, err := s.SaveEJHExternalTransfer(" 120.5 ", " 外部消费 ")
	if err != nil || saved.ExternalTransferAmount != "120.50" || saved.ExternalTransferNote != "外部消费" || saved.ExternalTransferUpdatedAt == nil {
		t.Fatal(saved, err)
	}
	if saved.Amount != "3000.00" || saved.HistoryMonth != "2026-10" || !saved.UpdatedAt.Equal(*original.UpdatedAt) {
		t.Fatal("transfer changed recharge", saved)
	}
	if _, err = s.SaveEJHExternalTransfer("120.50", "外部消费"); err != nil {
		t.Fatal(err)
	}
	events, err := EJHRecords[EJHExternalTransferEvent](s, "finance_external_transfer")
	if err != nil || len(events) != 1 || events[0].Delta != "120.50" || !events[0].Baseline {
		t.Fatal(events, err)
	}
	for _, bad := range []string{"-1", "1.001", "1e3", "NaN", "1,000", "1000000000000", "1 USD"} {
		if _, err = s.SaveEJHExternalTransfer(bad, "invalid"); err == nil {
			t.Fatal("accepted", bad)
		}
	}
	if _, err = s.SaveEJHExternalTransfer("1", strings.Repeat("中", 501)); err == nil {
		t.Fatal("accepted oversized note")
	}
	got, err := s.EJHRechargeLedger()
	if err != nil || got.Amount != saved.Amount || got.ExternalTransferAmount != saved.ExternalTransferAmount || got.ExternalTransferNote != saved.ExternalTransferNote || !got.UpdatedAt.Equal(*saved.UpdatedAt) || !got.ExternalTransferUpdatedAt.Equal(*saved.ExternalTransferUpdatedAt) {
		t.Fatal("invalid request mutated ledger", got, err)
	}
	got, err = s.SaveEJHRechargeLedger("3100")
	if err != nil || got.ExternalTransferAmount != saved.ExternalTransferAmount || got.ExternalTransferNote != saved.ExternalTransferNote || !got.ExternalTransferUpdatedAt.Equal(*saved.ExternalTransferUpdatedAt) {
		t.Fatal("recharge overwrote transfer", got, err)
	}
	if _, err = s.SaveEJHRechargeHistoryMonth("2026-09"); err != nil {
		t.Fatal(err)
	}
	if err = s.CaptureEJHFinance(time.Now()); err != nil {
		t.Fatal(err)
	}
	snapshots, err := EJHRecords[EJHFinancialSnapshot](s, "finance_snapshot")
	if err != nil || len(snapshots) == 0 || snapshots[0].Ledger.ExternalTransferAmount != "120.50" {
		t.Fatal("snapshot lost transfer", err)
	}
	s.Close()
	s, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	got, err = s.EJHRechargeLedger()
	if err != nil || got.ExternalTransferAmount != "120.50" || got.HistoryMonth != "2026-09" {
		t.Fatal("lost on restart", got, err)
	}
	if _, err = s.SaveEJHExternalTransfer("100", "修正外部消费"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.SaveEJHExternalTransfer("", "清除扣减"); err != nil {
		t.Fatal(err)
	}
	events, err = EJHRecords[EJHExternalTransferEvent](s, "finance_external_transfer")
	if err != nil || len(events) != 3 {
		t.Fatal(events, err)
	}
	deltas := map[string]bool{}
	for _, event := range events {
		deltas[event.Delta] = true
	}
	if !deltas["120.50"] || !deltas["-20.50"] || !deltas["-100.00"] {
		t.Fatal(events)
	}
	recharges, err := EJHRecords[EJHRechargeEvent](s, "finance_recharge")
	if err != nil || len(recharges) != 2 {
		t.Fatal("transfer changed recharge events", recharges, err)
	}
	got, err = s.EJHRechargeLedger()
	if err != nil || got.ExternalTransferAmount != "0.00" || got.Amount != "3100.00" {
		t.Fatal(got, err)
	}
}
