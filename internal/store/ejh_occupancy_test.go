package store

import (
	"chatgpt-space-merge/internal/ejh"
	"chatgpt-space-merge/internal/gptpay"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"chatgpt-space-merge/internal/model"
)

func TestEJHOccupancyConcurrentManualAutomaticAcrossStores(t *testing.T) {
	dir := t.TempDir()
	a, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	card := ejhFixtureCard(t, a)
	capacityCard(t, a, "alias")
	b, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	var accepted atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 24; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			st := []*Store{a, b}[i%2]
			email := fmt.Sprintf("race-%d@example.com", i)
			var e error
			if i%3 == 0 {
				e = st.ReserveProCard(email, "alias", false)
			} else {
				o := transferOrder(email, fmt.Sprint(i), "submitting")
				o.Order.CardID = card.ID
				e = st.CreateGPTPayOrder(o.Order, o.Snapshot)
			}
			if e == nil {
				accepted.Add(1)
			}
		}(i)
	}
	wg.Wait()
	if accepted.Load() != 1 {
		t.Fatalf("admitted %d accounts on one EJH card", accepted.Load())
	}
	for _, id := range []string{card.ID, "alias"} {
		c, _, e := a.GPTPayCard(id)
		if e != nil || c.Available || c.ActiveAccounts != 1 || c.ActiveEmail == "" {
			t.Fatalf("lost occupancy: %+v %v", c, e)
		}
	}
}

func TestEJHOccupancyUntilReturnAndLifetimeLimit(t *testing.T) {
	s := transferStore(t)
	card := ejhFixtureCard(t, s)
	limit := 2
	card.MaxAccounts = &limit
	_, secret, err := s.GPTPayCard(card.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SaveGPTPayCard(card, secret); err != nil {
		t.Fatal(err)
	}
	for n := 1; n <= 2; n++ {
		email := fmt.Sprintf("cycle%d@example.com", n)
		transferSource(t, s, email)
		if err = s.ReserveProCard(email, card.ID, false); err != nil {
			t.Fatal(err)
		}
		o := transferOrder(email, fmt.Sprintf("order%d", n), "submitting")
		o.Order.CardID = card.ID
		if err = s.CreateGPTPayOrder(o.Order, o.Snapshot); err != nil {
			t.Fatal(err)
		}
		for _, status := range []string{"submission_unknown", "processing", "success"} {
			o.Order.Status = status
			if err = s.UpdateGPTPayOrder(o.Order); err != nil {
				t.Fatal(err)
			}
			if err = s.ReleaseProCard(email); err != nil {
				t.Fatal(err)
			}
			if err = s.RecoverProSchedule(); err != nil {
				t.Fatal(err)
			}
			if err = s.ReserveProCard("other@example.com", card.ID, false); err == nil {
				t.Fatalf("released at payment %s", status)
			}
		}
		for _, remove := range []string{"pending", "running", "failed"} {
			if _, err = s.UpdateProAccount(email, func(p *model.MailAccountProfile) {
				p.TransferStatus = "completed"
				p.ProMergeConfirmedSource = "remote"
				p.RemoveStatus = remove
				p.ProAuto.Status = "interrupted"
			}); err != nil {
				t.Fatal(err)
			}
			c, _, e := s.GPTPayCard(card.ID)
			if e != nil || c.ActiveAccounts != 1 || c.Available {
				t.Fatalf("released before removal: %+v %v", c, e)
			}
		}
		// Removal alone cannot free a card while the return is outstanding.
		if _, err = s.UpdateProAccount(email, func(p *model.MailAccountProfile) { p.RemoveStatus = "team_removed" }); err != nil {
			t.Fatal(err)
		}
		c, _, e := s.GPTPayCard(card.ID)
		if e != nil || c.ActiveAccounts != 1 || c.Available {
			t.Fatalf("removal released occupancy: %+v %v", c, e)
		}
		ejhReceiveForOrder(t, s, card.ID, o.Order.ID)
		c, _, e = s.GPTPayCard(card.ID)
		if e != nil || c.ActiveAccounts != 0 || c.OpenedAccounts != n || c.RemainingAccounts != 2-n || c.Available != (n < 2) {
			t.Fatalf("release changed lifetime usage: %+v %v", c, e)
		}
	}
	if err = s.ReserveProCard("third@example.com", card.ID, false); err == nil || !strings.Contains(err.Error(), "上限") {
		t.Fatalf("limit not enforced: %v", err)
	}
}

func TestEJHOccupancyFailedPaymentAndPrepaymentRelease(t *testing.T) {
	s := transferStore(t)
	c := ejhFixtureCard(t, s)
	if e := s.ReserveProCard("login@example.com", c.ID, false); e != nil {
		t.Fatal(e)
	}
	if e := s.ReleaseProCard("login@example.com"); e != nil {
		t.Fatal(e)
	}
	o := transferOrder("failed@example.com", "failed", "submitting")
	o.Order.CardID = c.ID
	if e := s.CreateGPTPayOrder(o.Order, o.Snapshot); e != nil {
		t.Fatal(e)
	}
	o.Order.Status = "failed"
	if e := s.UpdateGPTPayOrder(o.Order); e != nil {
		t.Fatal(e)
	}
	if e := s.ReserveProCard("next@example.com", c.ID, false); e != nil {
		t.Fatal(e)
	}
}

func TestEJHOccupancyRestartDeleteReimport(t *testing.T) {
	dir := t.TempDir()
	s, e := Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	card := ejhFixtureCard(t, s)
	email := "durable@example.com"
	transferSource(t, s, email)
	o := transferOrder(email, "durable", "success")
	o.Order.CardID = card.ID
	if e = s.CreateGPTPayOrder(o.Order, o.Snapshot); e != nil {
		t.Fatal(e)
	}
	if e = s.DeleteProAccount(email); e != nil {
		t.Fatal(e)
	}
	if e = s.DeleteGPTPayCard(card.ID); e != nil {
		t.Fatal(e)
	}
	if e = s.Close(); e != nil {
		t.Fatal(e)
	}
	s, e = Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	card = ejhFixtureCard(t, s)
	if e = s.ReserveProCard("next@example.com", card.ID, false); e == nil {
		t.Fatal("deletion/restart released paid card")
	}
	transferSource(t, s, email)
	if _, e = s.UpdateProAccount(email, func(p *model.MailAccountProfile) { p.RemoveStatus = "completed" }); e != nil {
		t.Fatal(e)
	}
	if e = s.DeleteProAccount(email); e != nil {
		t.Fatal(e)
	}
	got, _, e := s.GPTPayCard(card.ID)
	if e != nil || got.Available {
		t.Fatal("deletion/removal freed card without return", got, e)
	}
	ejhReceiveForOrder(t, s, card.ID, o.Order.ID)
	got, _, e = s.GPTPayCard(card.ID)
	if e != nil || !got.Available || got.OpenedAccounts != 1 {
		t.Fatalf("return release not durable: %+v %v", got, e)
	}
}

func TestEJHOccupancySwitchAndOrdinaryCards(t *testing.T) {
	s := transferStore(t)
	cfg, _, err := s.EJHSettings()
	if err != nil || !cfg.SingleActiveAccount {
		t.Fatal("default policy", cfg, err)
	}
	card := ejhFixtureCard(t, s)
	cfg, _, err = s.EJHSettings()
	if err != nil {
		t.Fatal(err)
	}
	cfg.SingleActiveAccount = false
	if _, err = s.SaveEJHSettings(cfg, ""); err != nil {
		t.Fatal(err)
	}
	for _, email := range []string{"one@example.com", "two@example.com"} {
		transferSource(t, s, email)
		if err = s.ReserveProCard(email, card.ID, false); err != nil {
			t.Fatal(err)
		}
	}
	cfg.SingleActiveAccount = true
	if _, err = s.SaveEJHSettings(cfg, ""); err != nil {
		t.Fatal(err)
	}
	if err = s.ReserveProCard("three@example.com", card.ID, false); err == nil {
		t.Fatal("re-enable admitted new work")
	}
	for _, email := range []string{"one@example.com", "two@example.com"} {
		o := transferOrder(email, email, "success")
		o.Order.CardID = card.ID
		if err = s.CreateGPTPayOrder(o.Order, o.Snapshot); err != nil {
			t.Fatal("existing reservation must continue", err)
		}
		if err = s.ReleaseProCard(email); err != nil {
			t.Fatal(err)
		}
	}
	c, _, err := s.GPTPayCard(card.ID)
	if err != nil || c.ActiveAccounts != 2 || c.Available {
		t.Fatal(c, err)
	}
	if _, err = s.UpdateProAccount("one@example.com", func(p *model.MailAccountProfile) { p.RemoveStatus = "completed" }); err != nil {
		t.Fatal(err)
	}
	if err = s.ReserveProCard("three@example.com", card.ID, false); err == nil {
		t.Fatal("second occupant lost")
	}
	if _, err = s.UpdateProAccount("two@example.com", func(p *model.MailAccountProfile) { p.RemoveStatus = "completed" }); err != nil {
		t.Fatal(err)
	}
	ejhReceiveForOrder(t, s, card.ID, "one@example.com")
	if err = s.ReserveProCard("three@example.com", card.ID, false); err == nil {
		t.Fatal("second unreturned account lost")
	}
	ejhReceiveForOrder(t, s, card.ID, "two@example.com")
	if err = s.ReserveProCard("three@example.com", card.ID, false); err != nil {
		t.Fatal(err)
	}
	ordinary := gptpay.Card{ID: "ordinary", CardType: "other", Enabled: true}
	secret := gptpay.CardSecret{Number: "5555555555554444", Month: 12, Year: 2099, CVV: "123"}
	if _, err = s.SaveGPTPayCard(ordinary, secret); err != nil {
		t.Fatal(err)
	}
	for _, email := range []string{"regular1@example.com", "regular2@example.com", "regular3@example.com"} {
		if err = s.ReserveProCard(email, ordinary.ID, false); err != nil {
			t.Fatal("ordinary card restricted", err)
		}
	}
	c, _, err = s.GPTPayCard(ordinary.ID)
	if err != nil || c.SingleActiveAccount || c.PendingAccounts != 3 {
		t.Fatal(c, err)
	}
}

func TestEJHOccupancySettingPersistsAndLegacyDefault(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	cfg := ejh.DefaultSettings()
	cfg.SingleActiveAccount = false
	if _, err = s.SaveEJHSettings(cfg, ""); err != nil {
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
	cfg, _, err = s.EJHSettings()
	if err != nil || cfg.SingleActiveAccount {
		t.Fatal("false lost on restart", err)
	}
	if _, err = s.db.Exec(`UPDATE ejh_settings SET profile=json_remove(profile,'$.single_active_account')`); err != nil {
		t.Fatal(err)
	}
	cfg, _, err = s.EJHSettings()
	if err != nil || !cfg.SingleActiveAccount {
		t.Fatal("legacy default", err)
	}
	card := ejhFixtureCard(t, s)
	c, _, err := s.GPTPayCard(card.ID)
	if err != nil || !c.SingleActiveAccount {
		t.Fatal("SQL and settings disagree", c, err)
	}
}

func TestEJHOccupancyNewOrderCannotInheritOldRemoval(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	card := ejhFixtureCard(t, s)
	email := "repeat@example.com"
	transferSource(t, s, email)
	o := transferOrder(email, "old", "success")
	o.Order.CardID = card.ID
	if err = s.CreateGPTPayOrder(o.Order, o.Snapshot); err != nil {
		t.Fatal(err)
	}
	if _, err = s.UpdateProAccount(email, func(p *model.MailAccountProfile) { p.RemoveStatus = "completed" }); err != nil {
		t.Fatal(err)
	}
	o = transferOrder(email, "new", "success")
	o.Order.CardID = card.ID
	if err = s.CreateGPTPayOrder(o.Order, o.Snapshot); err != nil {
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
	if err = s.ReserveProCard("next@example.com", card.ID, false); err == nil {
		t.Fatal("new order inherited stale removal")
	}
}

func TestEJHOccupancyTransferPreservesPerOrderRelease(t *testing.T) {
	src, dst := transferStore(t), transferStore(t)
	card := ejhFixtureCard(t, src)
	email := "migration@example.com"
	transferSource(t, src, email)
	old := transferOrder(email, "old-paid", "success")
	old.Order.CardID = card.ID
	if err := src.CreateGPTPayOrder(old.Order, old.Snapshot); err != nil {
		t.Fatal(err)
	}
	if _, err := src.UpdateProAccount(email, func(p *model.MailAccountProfile) { p.RemoveStatus = "completed" }); err != nil {
		t.Fatal(err)
	}
	ejhReceiveForOrder(t, src, card.ID, old.Order.ID)
	current := transferOrder(email, "new-paid", "success")
	current.Order.CardID = card.ID
	if err := src.CreateGPTPayOrder(current.Order, current.Snapshot); err != nil {
		t.Fatal(err)
	}
	file, err := src.ExportProAccounts([]string{email}, model.ProDownstreamRef{})
	if err != nil {
		t.Fatal(err)
	}
	file = transferWire(t, file)
	if len(file.Accounts[0].Orders) != 2 {
		t.Fatal("missing orders")
	}
	for _, o := range file.Accounts[0].Orders {
		if o.CardType != "ejh" || o.OccupancyReleased == nil || *o.OccupancyReleased != (o.Order.ID == old.Order.ID) {
			t.Fatal("release snapshot mismatch", o.Order.ID)
		}
	}
	if _, err = dst.ImportProAccount(file.Accounts[0], file.ExportedAt, false, model.ProDownstreamRef{}); err != nil {
		t.Fatal(err)
	}
	capacityCard(t, dst, "imported-alias")
	c, _, err := dst.GPTPayCard("imported-alias")
	if err != nil || c.CardType != "ejh" || c.ActiveAccounts != 1 || c.OpenedAccounts != 1 || c.Available {
		t.Fatal(c, err)
	}
	if err = dst.ReserveProCard("next@example.com", c.ID, false); err == nil {
		t.Fatal("stale removal released new order on import")
	}
}
