package store

import (
	"chatgpt-space-merge/internal/ejh"
	"chatgpt-space-merge/internal/gptpay"
	"chatgpt-space-merge/internal/model"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func ejhFixtureCard(t *testing.T, s *Store) gptpay.Card {
	t.Helper()
	ensureIssuerFixtureEnabled(t, s, "ejh")
	c, e := s.ImportEJHCard(ejh.Card{OrderNo: "fixture-card", Number: "4242424242424242", Validity: "1299", CVV: "123", Status: "1", CardCurrency: "USD"}, true)
	if e != nil {
		t.Fatal(e)
	}
	return c
}
func TestEJHSingleReturnDifferentAmount(t *testing.T) {
	for _, kind := range []string{"2", "5", "16"} {
		t.Run(kind, func(t *testing.T) {
			s := transferStore(t)
			c := ejhFixtureCard(t, s)
			email := "fixture@example.com"
			transferSource(t, s, email)
			o := transferOrder(email, "payment", "success")
			o.Order.CardID = c.ID
			if e := s.CreateGPTPayOrder(o.Order, o.Snapshot); e != nil {
				t.Fatal(e)
			}
			_, e := s.UpdateProAccount(email, func(p *model.MailAccountProfile) {
				p.TransferStatus = "completed"
				p.ProMergeConfirmedSource = "remote"
				p.RemoveStatus = "failed"
			})
			if e != nil {
				t.Fatal(e)
			}
			cases, e := s.EJHCases()
			if e != nil || len(cases) != 1 || cases[0].State != "waiting" {
				t.Fatalf("merge marker not durable: %+v %v", cases, e)
			}
			orig := ejh.Trade{OrderNo: "fixture-card", TradeNo: "original", Type: "1", Status: "2", TradeAmount: "100", TradeCurrency: "USD"}
			ret := ejh.Trade{OrderNo: orig.OrderNo, TradeNo: "return", Type: kind, Status: "1", RefundAmount: "37.25"}
			for _, v := range []ejh.Trade{orig, ret} {
				if e = s.EJHSaveTrade(c.ID, v); e != nil {
					t.Fatal(e)
				}
			}
			originalID := EJHID("trade", orig.OrderNo+"|original|1")
			returnID := EJHID("trade", orig.OrderNo+"|return|"+kind)
			if e = s.EJHMatchTrade(returnID, o.Order.ID, originalID, "37.25", "EUR", "fixture verified release reference", true); e == nil {
				t.Fatal("pending return was confirmed")
			}
			ret.Status = "2"
			if e = s.EJHSaveTrade(c.ID, ret); e != nil {
				t.Fatal(e)
			}
			for _, currency := range []string{"123", "eur", "E R", "€"} {
				if e = s.EJHMatchTrade(returnID, o.Order.ID, originalID, "37.25", currency, "fixture verified release reference", true); e == nil {
					t.Fatal("invalid currency accepted", currency)
				}
			}
			if e = s.EJHMatchTrade(returnID, o.Order.ID, originalID, "37.25", "EUR", "fixture verified release reference", true); e != nil {
				t.Fatal(e)
			}
			cases, e = s.EJHCases()
			if e != nil || cases[0].State != "returned" || cases[0].Amounts["EUR"] != "37.25" {
				t.Fatalf("different return amount rejected: %+v %v", cases, e)
			}
			ret.TradeNo = "second"
			if e = s.EJHSaveTrade(c.ID, ret); e != nil {
				t.Fatal(e)
			}
			secondID := EJHID("trade", orig.OrderNo+"|second|"+kind)
			if e = s.EJHMatchTrade(secondID, o.Order.ID, originalID, "10", "USD", "duplicate fixture", true); e == nil {
				t.Fatal("duplicate return accepted")
			}
			card, _, e := s.GPTPayCard(c.ID)
			if e != nil || card.OpenedAccounts != 1 {
				t.Fatal("return reset historical card usage")
			}
			if e = s.EJHUnmatch(returnID, "fixture correction"); e != nil {
				t.Fatal(e)
			}
			cases, _ = s.EJHCases()
			if cases[0].State != "waiting" {
				t.Fatal("unmatch did not restore waiting state")
			}
			audit, e := EJHRecords[map[string]any](s, "match_audit")
			if e != nil || len(audit) != 2 {
				t.Fatal("missing audit", e)
			}
		})
	}
}

func TestEJHOperationAdmissionPersistsAcrossStores(t *testing.T) {
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
	var admitted atomic.Int32
	var wg sync.WaitGroup
	for i, st := range []*Store{a, b} {
		wg.Add(1)
		go func(i int, st *Store) {
			defer wg.Done()
			op := ejh.Operation{ID: []string{"one", "two"}[i], Method: "open", Status: "submitting"}
			if st.EJHBeginOperation(op) == nil {
				admitted.Add(1)
			}
		}(i, st)
	}
	wg.Wait()
	if admitted.Load() != 1 {
		t.Fatalf("admitted %d financial operations", admitted.Load())
	}
	ops, err := EJHRecords[ejh.Operation](a, "operation")
	if err != nil || len(ops) != 1 {
		t.Fatal(ops, err)
	}
	op := ops[0]
	op.Status = "unknown"
	if err = a.EJHPut("operation", op.ID, op); err != nil {
		t.Fatal(err)
	}
	c, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	next := ejh.Operation{ID: "next", Method: "open", Status: "submitting"}
	if err = c.EJHBeginOperation(next); err == nil {
		t.Fatal("restart lost unknown operation guard")
	}
	op.Status = "failed"
	if err = c.EJHPut("operation", op.ID, op); err != nil {
		t.Fatal(err)
	}
	if err = c.EJHBeginOperation(next); err != nil {
		t.Fatal(err)
	}
	if err = c.EJHBeginOperation(next); err == nil {
		t.Fatal("same operation submitted twice")
	}
}

func TestEJHMergeMarkerAndTimerRestart(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	email := "restart@example.com"
	transferSource(t, s, email)
	card := ejhFixtureCard(t, s)
	first := transferOrder(email, "first", "success")
	first.Order.CardID = card.ID
	if err = s.CreateGPTPayOrder(first.Order, first.Snapshot); err != nil {
		t.Fatal(err)
	}
	started := time.Now().Add(-65 * time.Minute).UTC()
	if _, err = s.UpdateProAccount(email, func(p *model.MailAccountProfile) {
		p.ProPushStartedAt = &started
		p.PushStatus = "completed"
		p.TransferStatus = "completed"
		p.ProMergeConfirmedSource = "remote"
	}); err != nil {
		t.Fatal(err)
	}
	later := transferOrder(email, "later", "success")
	later.Order.CardID = card.ID
	if err = s.CreateGPTPayOrder(later.Order, later.Snapshot); err != nil {
		t.Fatal(err)
	}
	if _, err = s.UpdateProAccount(email, nil); err != nil {
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
	p, _, err := s.MailAccountCredential(email)
	if err != nil || p.ProPushStartedAt == nil || !p.ProPushStartedAt.Equal(started) {
		t.Fatal("first push timer lost", err)
	}
	cases, err := s.EJHCases()
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		if c.ID == "first" && c.State != "waiting" {
			t.Fatal("lost confirmed merge")
		}
		if c.ID == "later" && c.State != "opened" {
			t.Fatal("unrelated account update marked later payment")
		}
	}
}

func TestEJHMalformedRefreshDisablesPhysicalCard(t *testing.T) {
	s := transferStore(t)
	card := ejhFixtureCard(t, s)
	capacityCard(t, s, "alias")
	var record ejh.CardRecord
	if err := s.EJHGet("card", card.ID, &record); err != nil {
		t.Fatal(err)
	}
	record.Remote.Number = "****4242"
	record.Remote.CVV = ""
	if _, err := s.ImportEJHCard(record.Remote, false); err == nil {
		t.Fatal("masked card accepted")
	}
	if err := s.ReserveProCard("fixture@example.com", "alias", false); err == nil {
		t.Fatal("stale alias remained usable")
	}
}
func TestEJHDefaultLimitAndPhysicalCardStatus(t *testing.T) {
	s := transferStore(t)
	cfg := ejh.DefaultSettings()
	cfg.Enabled, cfg.AppKey = true, "fixture-app"
	cfg.DefaultMaxAccounts = 1
	if _, e := s.SaveEJHSettings(cfg, "fixture-secret"); e != nil {
		t.Fatal(e)
	}
	c := ejhFixtureCard(t, s)
	if c.AccountLimit != 1 {
		t.Fatal("issuer default ignored")
	}
	capacityCard(t, s, "duplicate")
	d, _, e := s.GPTPayCard("duplicate")
	if e != nil || d.AccountLimit != 1 || d.Source != "ejh" {
		t.Fatal("physical duplicate bypassed issuer settings")
	}
	var r ejh.CardRecord
	if e = s.EJHGet("card", c.ID, &r); e != nil {
		t.Fatal(e)
	}
	r.Remote.Locked = "1"
	if _, e = s.ImportEJHCard(r.Remote, false); e != nil {
		t.Fatal(e)
	}
	if e = s.ReserveProCard("fixture@example.com", "duplicate", false); e == nil {
		t.Fatal("locked physical card admitted")
	}
	o := transferOrder("fixture@example.com", "blocked", "submitting")
	if e = s.CreateGPTPayOrder(o.Order, o.Snapshot); e == nil {
		t.Fatal("direct purchase bypassed lock")
	}
}
func TestEJHSettingsEncryptedAndRestart(t *testing.T) {
	dir := t.TempDir()
	s, e := Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	cfg := ejh.DefaultSettings()
	cfg.AppKey = "fixture"
	cfg.Enabled = true
	if _, e = s.SaveEJHSettings(cfg, "fixture-secret"); e != nil {
		t.Fatal(e)
	}
	var sealed string
	s.db.QueryRow(`SELECT secret FROM ejh_settings`).Scan(&sealed)
	if sealed == "fixture-secret" {
		t.Fatal("secret plaintext")
	}
	s.Close()
	s, e = Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	got, key, e := s.EJHSettings()
	if e != nil || !got.Enabled || key != "fixture-secret" {
		t.Fatal("restart lost configuration", e)
	}
}
