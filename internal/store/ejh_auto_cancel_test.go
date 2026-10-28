package store

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"chatgpt-space-merge/internal/ejh"
)

func ejhAutoCancelFixture(t *testing.T) (*Store, ejh.CardRecord, ejh.Operation) {
	t.Helper()
	s := transferStore(t)
	local := ejhFixtureCard(t, s)
	cfg, _, _ := s.EJHSettings()
	cfg.AutoCancelEnabled = true
	cfg.DefaultMaxAccounts = 1
	if _, err := s.SaveEJHSettings(cfg, ""); err != nil {
		t.Fatal(err)
	}
	email := "auto-cancel@example.com"
	transferSource(t, s, email)
	order := transferOrder(email, "auto-cancel-payment", "success")
	order.Order.CardID = local.ID
	if err := s.CreateGPTPayOrder(order.Order, order.Snapshot); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	var card ejh.CardRecord
	if err := s.EJHGet("card", local.ID, &card); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ImportEJHCard(card.Remote, false); err != nil {
		t.Fatal(err)
	}
	p := ejh.Trade{OrderNo: card.Remote.OrderNo, TradeNo: "paid", Type: "1", Status: "2", CardAmount: "20", CardCurrency: "USD"}
	r := p
	r.TradeNo = "refunded"
	r.Type = "5"
	r.BillAmount = "18.80"
	r.BillCurrency = "USD"
	if err := s.EJHSaveTrades(card.ID, []ejh.Trade{p, r}); err != nil {
		t.Fatal(err)
	}
	if err := s.EJHMatchTrade(EJHID("trade", r.OrderNo+"|refunded|5"), order.Order.ID, EJHID("trade", p.OrderNo+"|paid|1"), "20", "USD", "fixture receipt", true); err != nil {
		t.Fatal(err)
	}
	op := ejh.Operation{ID: EJHID("auto-cancel", card.ID), Method: "cancel", CardID: card.ID, OrderNo: card.Remote.OrderNo, Automatic: true, AutoCheckStartedAt: &started, Status: "submitting", CreatedAt: time.Now()}
	return s, card, op
}

func TestEJHAutoCancelAtomicAdmission(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*Store, ejh.CardRecord, *ejh.Operation) error
	}{
		{"below_limit", func(s *Store, c ejh.CardRecord, _ *ejh.Operation) error {
			_, err := s.db.Exec(`INSERT INTO pro_card_limits VALUES(?,2)`, proCardKey(c.Remote.Number))
			return err
		}},
		{"normal_pending", func(s *Store, c ejh.CardRecord, _ *ejh.Operation) error {
			_, err := s.SetEJHCardNormalPending(c.ID, true)
			return err
		}},
		{"closed", func(s *Store, c ejh.CardRecord, _ *ejh.Operation) error {
			c.Remote.Status = "2"
			return s.EJHPut("card", c.ID, c)
		}},
		{"closing", func(s *Store, c ejh.CardRecord, _ *ejh.Operation) error {
			c.Remote.Status = "8"
			return s.EJHPut("card", c.ID, c)
		}},
		{"reservation", func(s *Store, c ejh.CardRecord, _ *ejh.Operation) error {
			_, err := s.db.Exec(`INSERT INTO pro_card_reservations VALUES('same@example.com',?)`, proCardKey(c.Remote.Number))
			return err
		}},
		{"unknown_payment", func(s *Store, c ejh.CardRecord, _ *ejh.Operation) error {
			_, err := s.db.Exec(`INSERT INTO pro_card_usage VALUES('unknown',?,'unknown@example.com','submission_unknown')`, proCardKey(c.Remote.Number))
			return err
		}},
		{"missing_case", func(s *Store, c ejh.CardRecord, _ *ejh.Operation) error {
			_, err := s.db.Exec(`DELETE FROM ejh_cases`)
			return err
		}},
		{"manual_return_without_money", func(s *Store, c ejh.CardRecord, _ *ejh.Operation) error {
			_, err := s.db.Exec(`DELETE FROM ejh_records WHERE kind='trade'`)
			return err
		}},
		{"disabled", func(s *Store, c ejh.CardRecord, _ *ejh.Operation) error {
			cfg, _, err := s.EJHSettings()
			if err != nil {
				return err
			}
			cfg.AutoCancelEnabled = false
			_, err = s.SaveEJHSettings(cfg, "")
			return err
		}},
		{"no_verification", func(_ *Store, _ ejh.CardRecord, op *ejh.Operation) error { op.AutoCheckStartedAt = nil; return nil }},
		{"stale_evidence", func(_ *Store, _ ejh.CardRecord, op *ejh.Operation) error {
			at := time.Now()
			op.AutoCheckStartedAt = &at
			return nil
		}},
		{"expired_verification", func(_ *Store, _ ejh.CardRecord, op *ejh.Operation) error {
			at := time.Now().Add(-3 * time.Minute)
			op.AutoCheckStartedAt = &at
			return nil
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, c, op := ejhAutoCancelFixture(t)
			if err := s.EJHAutoCancelCheck(c.ID); err != nil {
				t.Fatal("invalid fixture", err)
			}
			if err := tc.change(s, c, &op); err != nil {
				t.Fatal(err)
			}
			if err := s.EJHBeginOperation(op); err == nil {
				t.Fatal("unsafe automatic cancellation admitted")
			}
			var claims int
			if err := s.db.QueryRow(`SELECT count(*) FROM ejh_operation_claims`).Scan(&claims); err != nil || claims != 0 {
				t.Fatal("rejected operation left claim", claims, err)
			}
		})
	}
	s, c, op := ejhAutoCancelFixture(t)
	var admitted atomic.Int32
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if s.EJHBeginOperation(op) == nil {
				admitted.Add(1)
			}
		}()
	}
	wg.Wait()
	if admitted.Load() != 1 {
		t.Fatal("duplicate cancellation", admitted.Load())
	}
	op.Status = "failed"
	if err := s.EJHPut("operation", op.ID, op); err != nil {
		t.Fatal(err)
	}
	if err := s.EJHAutoCancelCheck(c.ID); !errors.Is(err, ErrEJHAutoCancelIneligible) {
		t.Fatal("failed cancellation must not auto-repeat", err)
	}
}
