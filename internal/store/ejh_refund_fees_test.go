package store

import (
	"chatgpt-space-merge/internal/ejh"
	"errors"
	"slices"
	"testing"
	"time"
)

func TestEJHRefundFeeTradeLinks(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { s.Close() }()
	for _, row := range []struct{ id, kind, status, currency, amount string }{
		{"a", "5", "2", "USD", "100"}, {"b", "16", "2", "USD", "50"},
		{"pending", "5", "1", "USD", "50"}, {"clear", "4", "2", "USD", "50"},
		{"php", "5", "2", "PHP", "50"}, {"zero", "5", "2", "USD", "0"},
	} {
		err = s.EJHPut("trade", row.id, ejh.TradeRecord{ID: row.id, Trade: ejh.Trade{Type: row.kind, Status: row.status, BillCurrency: row.currency, BillAmount: row.amount}})
		if err != nil {
			t.Fatal(err)
		}
	}
	in := EJHRefundFee{ID: "linked", At: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), Amount: "9", Source: "manual", Note: "verified fee", TradeIDs: []string{"b", "a"}}
	saved, err := s.SaveEJHRefundFee(in)
	if err != nil || saved.Revision != 1 || !slices.Equal(saved.TradeIDs, []string{"a", "b"}) {
		t.Fatal(saved, err)
	}
	again, err := s.SaveEJHRefundFee(in)
	if err != nil || again.Revision != 1 || !again.UpdatedAt.Equal(saved.UpdatedAt) {
		t.Fatal("idempotent links", again, err)
	}
	for _, ids := range [][]string{{"a"}, {"pending"}, {"clear"}, {"php"}, {"zero"}, {"missing"}, {"a", "a"}} {
		other := in
		other.ID = "invalid"
		other.TradeIDs = ids
		if _, err = s.SaveEJHRefundFee(other); err == nil {
			t.Fatal("accepted invalid/occupied link", ids)
		}
	}
	saved.Voided = true
	voided, err := s.SaveEJHRefundFee(saved)
	if err != nil {
		t.Fatal(err)
	}
	other := in
	other.ID = "replacement"
	other.TradeIDs = []string{"a"}
	replacement, err := s.SaveEJHRefundFee(other)
	if err != nil {
		t.Fatal("void must release links", err)
	}
	voided.Voided = false
	if _, err = s.SaveEJHRefundFee(voided); err == nil {
		t.Fatal("restore must reject occupied links")
	}
	replacement.TradeIDs = []string{"b"}
	replacement, err = s.SaveEJHRefundFee(replacement)
	if err != nil || replacement.Revision != 2 {
		t.Fatal("link edit", replacement, err)
	}
	s.Close()
	s, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := s.EJHRefundFees()
	if err != nil || len(rows) != 2 {
		t.Fatal(rows, err)
	}
	for _, row := range rows {
		if row.ID == "replacement" && !slices.Equal(row.TradeIDs, []string{"b"}) {
			t.Fatal("links lost after restart", row)
		}
	}
	audit, err := EJHRecords[map[string]any](s, "finance_refund_fee_audit")
	if err != nil || len(audit) != 4 {
		t.Fatal("link edits must be audited", len(audit), err)
	}
}

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
