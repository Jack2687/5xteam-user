package store

import (
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"chatgpt-space-merge/internal/nexovcc"
)

func purposeFinance(serial string) nexovcc.Finance {
	return nexovcc.Finance{At: time.Now(), Trades: []nexovcc.Trade{{ID: "external-paid", Serial: serial, Authorization: "external-auth", Currency: "USD", Amount: "46.21", MerchantAmount: "2900", MerchantCurrency: "PHP", Status: "PENDING", At: time.Now()}}}
}

func TestNexovccNonProPurposePersistsAndRestoresMatching(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { s.Close() }()
	c := nexStoreCard(t, s)
	f := purposeFinance(c.Serial)
	if err = s.SaveNexovccFinance(f); err != nil {
		t.Fatal(err)
	}
	g, err := s.SetNexovccTradePurpose("external-paid", true, 0)
	if err != nil || !g.NonPro || g.PurposeRevision != 1 {
		t.Fatal(g, err)
	}
	var original string
	if err = s.db.QueryRow(`SELECT profile FROM nexovcc_cache WHERE kind='finance'`).Scan(&original); err != nil {
		t.Fatal(err)
	}
	if original != mustJSON(f) {
		t.Fatal("classification altered raw financial evidence")
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	o := transferOrder("manual@example.test", "pro-order", "success")
	o.Order.CardID = c.ID
	if err = s.CreateGPTPayOrder(o.Order, o.Snapshot); err != nil {
		t.Fatal(err)
	}
	if err = s.SaveNexovccFinance(f); err != nil {
		t.Fatal(err)
	}
	cases, err := s.NexovccCases()
	if err != nil || len(cases) != 1 || cases[0].Match != nil {
		t.Fatal("external payment auto-matched", cases, err)
	}
	if err = s.MatchNexovccCase(o.Order.ID, g.ID, "test manual assignment"); err == nil {
		t.Fatal("manual assignment bypassed classification")
	}
	// A new reversal inherits the authorization purpose across sync/restart.
	r := f.Trades[0]
	r.ID = "external-return"
	r.Status = "REVERSED"
	r.Amount = "46.16"
	r.At = r.At.Add(time.Minute)
	f.Trades = append(f.Trades, r)
	if err = s.SaveNexovccFinance(f); err != nil {
		t.Fatal(err)
	}
	groups, err := s.NexovccTradeGroups(f)
	if err != nil || len(groups) != 1 || !groups[0].NonPro || !groups[0].FullyReturned || groups[0].Difference != "0.05" {
		t.Fatal(groups, err)
	}
	if _, err = s.SetNexovccTradePurpose("external-return", false, 0); err == nil {
		t.Fatal("stale revision accepted")
	}
	if _, err = s.SetNexovccTradePurpose("external-return", false, 1); err != nil {
		t.Fatal(err)
	}
	cases, err = s.NexovccCases()
	if err != nil || cases[0].Match == nil || cases[0].State != "returned" {
		t.Fatal("restore did not rematch", cases, err)
	}
	if _, err = s.SetNexovccTradePurpose("external-paid", true, 2); err == nil {
		t.Fatal("linked account detached by purpose flag")
	}
	var unchanged string
	if err = s.db.QueryRow(`SELECT profile FROM nexovcc_cache WHERE kind='finance'`).Scan(&unchanged); err != nil || unchanged != mustJSON(f) {
		t.Fatal("financial snapshot changed", err)
	}
}

func TestNexovccNonProPurposeResolvesAmbiguityWithoutChangingMoney(t *testing.T) {
	s := transferStore(t)
	c := nexStoreCard(t, s)
	o := transferOrder("pro@example.test", "pro-order", "success")
	o.Order.CardID = c.ID
	if err := s.CreateGPTPayOrder(o.Order, o.Snapshot); err != nil {
		t.Fatal(err)
	}
	f := purposeFinance(c.Serial)
	other := f.Trades[0]
	other.ID = "pro-payment"
	other.Authorization = "pro-auth"
	f.Trades = append(f.Trades, other)
	if err := s.SaveNexovccFinance(f); err != nil {
		t.Fatal(err)
	}
	cases, _ := s.NexovccCases()
	if cases[0].Match != nil {
		t.Fatal("ambiguous matched")
	}
	before := nexovcc.Groups(f)
	if _, err := s.SetNexovccTradePurpose("external-paid", true, 0); err != nil {
		t.Fatal(err)
	}
	cases, err := s.NexovccCases()
	if err != nil || cases[0].Match == nil || cases[0].Match.Group.Authorization != "pro-auth" {
		t.Fatal(cases, err)
	}
	groups, err := s.NexovccTradeGroups(f)
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]nexovcc.TradeGroup{}
	for _, g := range before {
		byID[g.ID] = g
	}
	if len(before) != len(groups) {
		t.Fatal("financial group count changed")
	}
	for _, g := range groups {
		g.NonPro = false
		g.PurposeRevision = 0
		if !reflect.DeepEqual(byID[g.ID], g) {
			t.Fatal("financial amounts changed", g.ID)
		}
	}
	if _, err = s.SetNexovccTradePurpose("missing", true, 0); err == nil {
		t.Fatal("missing trade accepted")
	}
}

func TestNexovccNonProPurposeConcurrentRevision(t *testing.T) {
	dir := t.TempDir()
	a, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	f := purposeFinance("card")
	if err = a.SaveNexovccFinance(f); err != nil {
		t.Fatal(err)
	}
	var accepted atomic.Int32
	var wg sync.WaitGroup
	for _, s := range []*Store{a, b} {
		wg.Add(1)
		go func(s *Store) {
			defer wg.Done()
			if _, err := s.SetNexovccTradePurpose("external-paid", true, 0); err == nil {
				accepted.Add(1)
			}
		}(s)
	}
	wg.Wait()
	if accepted.Load() != 1 {
		t.Fatal("revision race", accepted.Load())
	}
	var audits int
	if err = a.db.QueryRow(`SELECT count(*) FROM nexovcc_audit WHERE json_extract(profile,'$.action')='trade_purpose'`).Scan(&audits); err != nil || audits != 1 {
		t.Fatal("duplicate audit", audits, err)
	}
}
