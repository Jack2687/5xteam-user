package store

import (
	"chatgpt-space-merge/internal/nexovcc"
	"testing"
	"time"
)

func TestNexovccTradeReviewExpenseLifecycle(t *testing.T) {
	s, e := Open(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	card := nexStoreCard(t, s)
	f := nexovcc.Finance{At: time.Now(), Trades: []nexovcc.Trade{{ID: "unclassified", Serial: card.Serial, Currency: "USD", Amount: "2.30", Status: "OTHER", At: time.Now()}}}
	if e = s.SaveNexovccFinance(f); e != nil {
		t.Fatal(e)
	}
	g := nexovcc.Groups(f)[0]
	in := NexovccReviewInput{Kind: "other_fee", Amount: "2.30", Reason: "已核对为其他卡片费用", Fingerprint: nexovcc.ReviewFingerprint(f, g)}
	bad := in
	bad.Reason = ""
	if _, e = s.SaveNexovccTradeReview(g.ID, bad); e == nil {
		t.Fatal("missing reason accepted")
	}
	bad = in
	bad.Amount = "2.31"
	if _, e = s.SaveNexovccTradeReview(g.ID, bad); e == nil {
		t.Fatal("expense beyond source accepted")
	}
	r, e := s.SaveNexovccTradeReview(g.ID, in)
	if e != nil || r.Revision != 1 {
		t.Fatal(r, e)
	}
	if _, e = s.SaveNexovccTradeReview(g.ID, in); e == nil {
		t.Fatal("stale duplicate save accepted")
	}
	groups, e := s.NexovccTradeGroups(f)
	if e != nil || len(groups) != 1 || groups[0].OtherExpense == nil || !groups[0].OtherExpense.Applied || groups[0].Valid || groups[0].FullyReturned {
		t.Fatal("manual cost changed refund evidence", groups, e)
	}
	var raw string
	s.db.QueryRow(`SELECT profile FROM nexovcc_cache WHERE kind='finance'`).Scan(&raw)
	if raw != mustJSON(f) {
		t.Fatal("raw supplier ledger overwritten")
	}
	if e = s.SaveNexovccFinance(f); e != nil {
		t.Fatal(e)
	}
	_, records, e := s.NexovccReviewData(f)
	if e != nil || len(records) != 1 || !records[0].Applied {
		t.Fatal(records, e)
	}
	f.Trades[0].Amount = "2.40"
	if e = s.SaveNexovccFinance(f); e != nil {
		t.Fatal(e)
	}
	groups, _ = s.NexovccTradeGroups(f)
	if groups[0].OtherExpense != nil {
		t.Fatal("stale amount stayed in costs")
	}
	in.Revision = 1
	in.Voided = true
	in.Reason = "撤销原费用重新核实"
	if _, e = s.SaveNexovccTradeReview(g.ID, in); e != nil {
		t.Fatal(e)
	}
	var audits int
	s.db.QueryRow(`SELECT count(*) FROM nexovcc_audit WHERE json_extract(profile,'$.action')='trade_review'`).Scan(&audits)
	if audits != 2 {
		t.Fatal(audits)
	}
}

func TestNexovccTradeReviewRefundLinkAndUndo(t *testing.T) {
	s, e := Open(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	c := nexStoreCard(t, s)
	now := time.Now()
	f := nexovcc.Finance{At: now, Trades: []nexovcc.Trade{
		{ID: "paid", Serial: c.Serial, Authorization: "original", Status: "COMPLETE", Amount: "142.12", Currency: "USD", MerchantAmount: "142.12", MerchantCurrency: "USD", At: now},
		{ID: "refund", Serial: c.Serial, Authorization: "new-code", Status: "REFUNDED", Amount: "141.57", Currency: "USD", MerchantAmount: "141.57", MerchantCurrency: "USD", At: now.Add(time.Minute)},
	}}
	if e = s.SaveNexovccFinance(f); e != nil {
		t.Fatal(e)
	}
	var source, target nexovcc.TradeGroup
	for _, g := range nexovcc.Groups(f) {
		if g.Valid {
			target = g
		} else {
			source = g
		}
	}
	in := NexovccReviewInput{Kind: "refund_link", TargetID: target.ID, Reason: "核对同卡原付款对应退款", Fingerprint: nexovcc.ReviewFingerprint(f, source), TargetFingerprint: nexovcc.ReviewFingerprint(f, target)}
	r, e := s.SaveNexovccTradeReview(source.ID, in)
	if e != nil {
		t.Fatal(e)
	}
	groups, e := s.NexovccTradeGroups(f)
	if e != nil || len(groups) != 1 || groups[0].Cost != "0.55" {
		t.Fatal(groups, e)
	}
	in.Revision = r.Revision
	in.Voided = true
	in.Reason = "撤销错误的退款关联"
	if _, e = s.SaveNexovccTradeReview(source.ID, in); e != nil {
		t.Fatal(e)
	}
	groups, _ = s.NexovccTradeGroups(f)
	if len(groups) != 2 {
		t.Fatal("undo did not restore raw grouping")
	}
}
