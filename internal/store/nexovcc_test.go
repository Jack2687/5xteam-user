package store

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"chatgpt-space-merge/internal/nexovcc"
)

func nexStoreCard(t *testing.T, s *Store) nexovcc.Card {
	t.Helper()
	c, e := s.SaveNexovccCard(nexovcc.Card{Serial: "fixture-global-1", Status: "1", Balance: "10.00"}, &nexovcc.PrivateCard{Number: "4242424242424242", CVV: "123", Month: 12, Year: 2099})
	if e != nil {
		t.Fatal(e)
	}
	return c
}
func TestNexovccCredentialsEncryptedAndMerchantLocked(t *testing.T) {
	s := transferStore(t)
	cfg := nexovcc.Defaults()
	cfg.Enabled = true
	k := nexovcc.Credentials{Login: "fixture-global-login", Password: "fixture-pass", FundPassword: "fixture-fund", TOTPSecret: "GEZDGNBVGY3TQOJQ"}
	if _, e := s.SaveNexovccSettings(cfg, k, false); e != nil {
		t.Fatal(e)
	}
	var raw, sealed string
	if e := s.db.QueryRow(`SELECT profile,secret FROM nexovcc_settings`).Scan(&raw, &sealed); e != nil {
		t.Fatal(e)
	}
	for _, secret := range []string{k.Login, k.Password, k.FundPassword, k.TOTPSecret} {
		if strings.Contains(raw+sealed, secret) {
			t.Fatal("plaintext credential")
		}
	}
	pub, got, e := s.NexovccSettings()
	if e != nil || got != k || !pub.LoginPresent || !pub.TOTPPresent {
		t.Fatal(pub, e)
	}
	nexStoreCard(t, s)
	if _, e = s.SaveNexovccSettings(cfg, nexovcc.Credentials{Login: "different"}, false); e == nil {
		t.Fatal("merchant switch mixed records")
	}
	if _, e = s.SaveNexovccSettings(cfg, nexovcc.Credentials{}, false); e != nil {
		t.Fatal(e)
	}
	_, got, _ = s.NexovccSettings()
	if got != k {
		t.Fatal("blank reset credentials")
	}
}
func TestNexovccOccupancyReturnsAndLifetimeLimit(t *testing.T) {
	s := transferStore(t)
	c := nexStoreCard(t, s)
	local, secret, e := s.GPTPayCard(c.ID)
	if e != nil || !local.Available || local.CardType != "nexovcc" || local.AccountLimit != 1 {
		t.Fatal(local, e)
	}
	limit := 3
	local.MaxAccounts = &limit
	if _, e = s.SaveGPTPayCard(local, secret); e != nil {
		t.Fatal(e)
	}
	transferSource(t, s, "first@example.com")
	o := transferOrder("first@example.com", "global-payment", "success")
	o.Order.CardID = c.ID
	if e = s.CreateGPTPayOrder(o.Order, o.Snapshot); e != nil {
		t.Fatal(e)
	}
	if e = s.ReserveProCard("second@example.com", c.ID, false); e == nil {
		t.Fatal("single active bypass")
	}
	cases, e := s.NexovccCases()
	if e != nil || len(cases) != 1 || cases[0].State != "opened" {
		t.Fatal(cases, e)
	}
	if e = s.SetNexovccCase(o.Order.ID, "waiting", "已申请退款，尚未到账", 0); e != nil {
		t.Fatal(e)
	}
	if e = s.ReserveProCard("second@example.com", c.ID, false); e == nil {
		t.Fatal("waiting return released card")
	}
	if e = s.SetNexovccCase(o.Order.ID, "returned", "已在平台确认实际到账", 1); e != nil {
		t.Fatal(e)
	}
	if e = s.ReserveProCard("second@example.com", c.ID, false); e != nil {
		t.Fatal(e)
	}
	_ = s.ReleaseProCard("second@example.com")
	local, secret, _ = s.GPTPayCard(c.ID)
	if local.OpenedAccounts != 1 {
		t.Fatal("return cleared historical usage")
	}
	one := 1
	local.MaxAccounts = &one
	if _, e = s.SaveGPTPayCard(local, secret); e != nil {
		t.Fatal(e)
	}
	if e = s.ReserveProCard("second@example.com", c.ID, false); e == nil {
		t.Fatal("lifetime usage bypass")
	}
}
func TestNexovccUnknownCannotBeReleasedAndCancellationBlocksPayment(t *testing.T) {
	s := transferStore(t)
	c := nexStoreCard(t, s)
	transferSource(t, s, "pending@example.com")
	o := transferOrder("pending@example.com", "global-unknown", "submission_unknown")
	o.Order.CardID = c.ID
	if e := s.CreateGPTPayOrder(o.Order, o.Snapshot); e != nil {
		t.Fatal(e)
	}
	if e := s.SetNexovccCase(o.Order.ID, "returned", "manual alleged return", 0); e == nil {
		t.Fatal("unknown released")
	}
	op := nexovcc.Operation{ID: "nex-cancel1", Action: "cancel", CardID: c.ID, Status: "submitted"}
	if e := s.BeginNexovccOperation(op); e == nil {
		t.Fatal("cancel admitted unresolved payment")
	}
	// Even turning off single-active never permits funds operations during payment.
	cfg := nexovcc.Defaults()
	cfg.SingleActiveAccount = false
	cfg.DefaultMaxAccounts = 10
	if _, e := s.SaveNexovccSettings(cfg, nexovcc.Credentials{}, false); e != nil {
		t.Fatal(e)
	}
	op.Action = "withdraw"
	if e := s.BeginNexovccOperation(op); e == nil {
		t.Fatal("withdraw raced unresolved payment")
	}
	if e := s.ReserveProCard("new@example.com", c.ID, false); e == nil {
		t.Fatal("unknown payment bypassed by disabling single active")
	}
}

func TestNexovccRestoreAutomaticReturnFollowsFutureRemoval(t *testing.T) {
	s := transferStore(t)
	c := nexStoreCard(t, s)
	transferSource(t, s, "return@example.com")
	o := transferOrder("return@example.com", "global-return", "success")
	o.Order.CardID = c.ID
	if e := s.CreateGPTPayOrder(o.Order, o.Snapshot); e != nil {
		t.Fatal(e)
	}
	if e := s.SetNexovccCase(o.Order.ID, "returned", "已核实回款到账", 0); e != nil {
		t.Fatal(e)
	}
	if e := s.SetNexovccCase(o.Order.ID, "auto", "恢复自动按合并状态判断", 1); e != nil {
		t.Fatal(e)
	}
	cases, e := s.NexovccCases()
	if e != nil || len(cases) != 1 || cases[0].State != "opened" {
		t.Fatal(cases, e)
	}
	at := formatTime(time.Now())
	if _, e = s.db.Exec(`INSERT INTO pro_card_order_releases VALUES(?,?)`, o.Order.ID, at); e != nil {
		t.Fatal(e)
	}
	cases, e = s.NexovccCases()
	if e != nil || cases[0].State != "waiting" || cases[0].RequestedAt != at || cases[0].Revision != 2 {
		t.Fatal("restored automatic state stopped following removal", cases, e)
	}
	if e = s.SetNexovccCase(o.Order.ID, "returned", "使用旧版本不应覆盖", 1); e == nil {
		t.Fatal("stale manual update accepted")
	}
}

func TestNexovccListSyncDoesNotClearPrivateImportError(t *testing.T) {
	s := transferStore(t)
	c := nexStoreCard(t, s)
	c.Error = "卡密未完整导入，请重新同步"
	if _, e := s.SaveNexovccCard(c, nil); e != nil {
		t.Fatal(e)
	}
	c.Error, c.Last4 = "", ""
	saved, e := s.SaveNexovccCard(c, nil)
	if e != nil || saved.Error == "" || saved.Last4 != "4242" {
		t.Fatal(saved, e)
	}
	local, _, e := s.GPTPayCard(c.ID)
	if e != nil || local.Available {
		t.Fatal("list-only refresh unlocked incomplete card", local, e)
	}
}
func TestNexovccPhysicalCardClaimsAcrossStoresAndClosedState(t *testing.T) {
	dir := t.TempDir()
	a, e := Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer a.Close()
	c := nexStoreCard(t, a)
	b, e := Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer b.Close()
	var admitted atomic.Int32
	var wg sync.WaitGroup
	for i, s := range []*Store{a, b} {
		wg.Add(1)
		go func(i int, s *Store) {
			defer wg.Done()
			if s.ReserveProCard(fmt.Sprintf("account%d@example.com", i), c.ID, false) == nil {
				admitted.Add(1)
			}
		}(i, s)
	}
	wg.Wait()
	if admitted.Load() != 1 {
		t.Fatal("double reserve", admitted.Load())
	}
	_ = a.ReleaseProCard("account0@example.com")
	_ = a.ReleaseProCard("account1@example.com")
	op := nexovcc.Operation{ID: "nex-cancel1", Action: "cancel", CardID: c.ID, Status: "processing"}
	if e = a.BeginNexovccOperation(op); e != nil {
		t.Fatal(e)
	}
	if e = b.ReserveProCard("later@example.com", c.ID, false); e == nil {
		t.Fatal("reservation during card mutation")
	}
	c.Status = "4"
	if _, e = a.SaveNexovccCard(c, nil); e != nil {
		t.Fatal(e)
	}
	op.Status = "succeeded"
	if e = a.SaveNexovccOperation(op); e != nil {
		t.Fatal(e)
	}
	local, _, e := a.GPTPayCard(c.ID)
	if e != nil || local.Available || local.IssuerStatus != "4" {
		t.Fatal(local, e)
	}
	op.ID = "nex-cancel2"
	op.Status = "submitted"
	if e = a.BeginNexovccOperation(op); e == nil {
		t.Fatal("deleted card cancelled twice")
	}
}
func TestNexovccLegacyTypeMigrationAndSnapshotPrivacy(t *testing.T) {
	dir := t.TempDir()
	s, e := Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.db.Exec(`DROP TABLE pro_card_types;CREATE TABLE pro_card_types(card_key TEXT PRIMARY KEY,card_type TEXT CHECK(card_type IN ('ejh','other')));INSERT INTO pro_card_types VALUES('historical','ejh')`); e != nil {
		t.Fatal(e)
	}
	s.Close()
	s, e = Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	kind, e := proCardType(s.db, "historical")
	if e != nil || kind != "ejh" {
		t.Fatal("migration lost EJH", kind, e)
	}
	c := nexStoreCard(t, s)
	raw, _ := json.Marshal(c)
	if strings.Contains(string(raw), "4242424242424242") || strings.Contains(string(raw), "123") {
		t.Fatal("sensitive snapshot")
	}
	before, _, _ := s.GPTPayCard(c.ID)
	time.Sleep(time.Millisecond)
	if _, e = s.SaveNexovccCard(c, &nexovcc.PrivateCard{Number: "4242424242424242", CVV: "123", Month: 12, Year: 2099}); e != nil {
		t.Fatal(e)
	}
	after, _, _ := s.GPTPayCard(c.ID)
	if !before.CreatedAt.Equal(after.CreatedAt) {
		t.Fatal("sync reset selection age")
	}
}
