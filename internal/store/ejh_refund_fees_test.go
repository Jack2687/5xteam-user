package store

import (
	"errors"
	"testing"
	"time"
)

func TestEJHRefundFeeLedger(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { s.Close() }()
	in := EJHRefundFee{ID: "refund-window-1", At: time.Date(2026, 1, 1, 12, 24, 46, 0, time.FixedZone("CST", 8*3600)), Amount: "24.84", Source: "balance_reconciliation", Note: "余额核对，4笔退款"}
	saved, err := s.SaveEJHRefundFee(in)
	if err != nil || saved.Revision != 1 {
		t.Fatal(saved, err)
	}
	again, err := s.SaveEJHRefundFee(in)
	if err != nil || again.Revision != saved.Revision || again.Amount != saved.Amount || !again.At.Equal(saved.At) || !again.UpdatedAt.Equal(saved.UpdatedAt) {
		t.Fatal("retry must not duplicate expense", again, err)
	}
	for _, value := range []string{"0", "-1", "24.841", "1e2", "NaN", ""} {
		bad := saved
		bad.Amount = value
		if _, err = s.SaveEJHRefundFee(bad); err == nil {
			t.Fatal("invalid amount", value)
		}
	}
	bad := saved
	bad.At = time.Now().Add(time.Hour)
	if _, err = s.SaveEJHRefundFee(bad); err == nil {
		t.Fatal("future date")
	}
	bad = saved
	bad.Source = "issuer_reported"
	if _, err = s.SaveEJHRefundFee(bad); err == nil {
		t.Fatal("unverified issuer source")
	}
	stale := in
	stale.Amount = "25"
	if _, err = s.SaveEJHRefundFee(stale); !errors.Is(err, ErrEJHRefundFeeConflict) {
		t.Fatal("stale update", err)
	}
	saved.Voided = true
	voided, err := s.SaveEJHRefundFee(saved)
	if err != nil || voided.Revision != 2 {
		t.Fatal(voided, err)
	}
	if _, err = s.SaveEJHRefundFee(in); !errors.Is(err, ErrEJHRefundFeeConflict) {
		t.Fatal("old retry resurrected voided fee", err)
	}
	s.Close()
	s, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := s.EJHRefundFees()
	if err != nil || len(rows) != 1 || !rows[0].Voided || rows[0].Amount != "24.84" {
		t.Fatal(rows, err)
	}
	audit, err := EJHRecords[map[string]any](s, "finance_refund_fee_audit")
	if err != nil || len(audit) != 2 {
		t.Fatal("audit lost", audit, err)
	}
	rows[0].Voided = false
	if _, err = s.SaveEJHRefundFee(rows[0]); err != nil {
		t.Fatal(err)
	}
	ledger, err := s.EJHRechargeLedger()
	if err != nil || ledger.Amount != "" {
		t.Fatal("expense changed recharge", ledger, err)
	}
}
