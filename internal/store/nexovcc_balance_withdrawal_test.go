package store

import (
	"fmt"
	"testing"
	"time"

	"chatgpt-space-merge/internal/model"
	"chatgpt-space-merge/internal/nexovcc"
)

func balanceWithdrawalFixture(t *testing.T, s *Store) (nexovcc.Card, nexovcc.Finance) {
	t.Helper()
	card := nexStoreCard(t, s)
	card.Balance, card.BalanceAt = "169.39", time.Now()
	card, err := s.SaveNexovccCard(card, nil)
	if err != nil {
		t.Fatal(err)
	}
	key := proCardKey("4242424242424242")
	if _, err = s.db.Exec(`INSERT INTO pro_card_usage VALUES('paid',?,'paid@example.com','success')`, key); err != nil {
		t.Fatal(err)
	}
	requested := card.BalanceAt.Add(-time.Minute)
	if _, err = s.db.Exec(`INSERT INTO pro_card_order_releases VALUES('paid',?)`, formatTime(requested)); err != nil {
		t.Fatal(err)
	}
	f := nexovcc.Finance{At: card.BalanceAt, CardAt: map[string]time.Time{card.Serial: card.BalanceAt},
		Trades: []nexovcc.Trade{{ID: "paid-trade", Serial: card.Serial, Authorization: "UX0VKI", Currency: "USD", Amount: "141.82", MerchantAmount: "8919.64", MerchantCurrency: "PHP", Status: "PENDING", At: requested.Add(-time.Hour)}},
		Funds:  []nexovcc.Fund{{ID: "open-fund", Serial: card.Serial, Kind: 3001, Currency: "USD", Amount: "-170.00", At: requested.Add(-2 * time.Hour)}, {ID: "fee", Serial: card.Serial, Link: "paid-trade", Kind: 2007, Currency: "USD", Amount: "-2.42", At: requested.Add(-time.Hour)}},
	}
	g := nexovcc.Groups(f)[0]
	if _, err = s.db.Exec(`INSERT INTO nexovcc_case_matches VALUES('paid',?,0,?)`, g.ID, mustJSON(NexovccMatch{Group: g})); err != nil {
		t.Fatal(err)
	}
	if err = s.NexovccCache("finance", f); err != nil {
		t.Fatal(err)
	}
	return card, f
}

func TestNexovccBalanceWithdrawalAdmission(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*testing.T, *Store, *nexovcc.Card, *nexovcc.Finance)
		allow  bool
	}{
		{"returned balance before reversal", nil, true},
		{"only old remaining balance", func(t *testing.T, s *Store, c *nexovcc.Card, f *nexovcc.Finance) { c.Balance = "28.18" }, false},
		{"one cent rounding", func(t *testing.T, s *Store, c *nexovcc.Card, f *nexovcc.Finance) { c.Balance = "28.19" }, false},
		{"one dollar", func(t *testing.T, s *Store, c *nexovcc.Card, f *nexovcc.Finance) { c.Balance = "1.00" }, false},
		{"not requested", func(t *testing.T, s *Store, c *nexovcc.Card, f *nexovcc.Finance) {
			_, err := s.db.Exec(`DELETE FROM pro_card_order_releases`)
			if err != nil {
				t.Fatal(err)
			}
		}, false},
		{"manual returned is not proof", func(t *testing.T, s *Store, c *nexovcc.Card, f *nexovcc.Finance) {
			if err := s.SetNexovccCase("paid", "returned", "manual refund", 0); err != nil {
				t.Fatal(err)
			}
		}, false},
		{"limit raised", func(t *testing.T, s *Store, c *nexovcc.Card, f *nexovcc.Finance) {
			cfg, _, _ := s.NexovccSettings()
			cfg.DefaultMaxAccounts = 2
			if _, err := s.SaveNexovccSettings(cfg, nexovcc.Credentials{}, false); err != nil {
				t.Fatal(err)
			}
		}, false},
		{"reservation", func(t *testing.T, s *Store, c *nexovcc.Card, f *nexovcc.Finance) {
			_, err := s.db.Exec(`INSERT INTO pro_card_reservations VALUES('reserved@example.com',?)`, proCardKey("4242424242424242"))
			if err != nil {
				t.Fatal(err)
			}
		}, false},
		{"payment undecided", func(t *testing.T, s *Store, c *nexovcc.Card, f *nexovcc.Finance) {
			_, err := s.db.Exec(`INSERT INTO pro_card_usage VALUES('unknown',?,'unknown@example.com','submission_unknown')`, proCardKey("4242424242424242"))
			if err != nil {
				t.Fatal(err)
			}
		}, false},
		{"unknown money operation", func(t *testing.T, s *Store, c *nexovcc.Card, f *nexovcc.Finance) {
			op := nexovcc.Operation{ID: "old-unknown", Action: "withdraw_all", CardID: c.ID, Status: "unknown"}
			_, err := s.db.Exec(`INSERT INTO nexovcc_operations VALUES(?,?,?,'unknown')`, op.ID, "card:"+c.ID, mustJSON(op))
			if err != nil {
				t.Fatal(err)
			}
		}, false},
		{"stale card ledger", func(t *testing.T, s *Store, c *nexovcc.Card, f *nexovcc.Finance) {
			f.CardAt[c.Serial] = c.BalanceAt.Add(-2 * time.Hour)
		}, false},
		{"stale balance", func(t *testing.T, s *Store, c *nexovcc.Card, f *nexovcc.Finance) {
			c.BalanceAt = c.BalanceAt.Add(-2 * time.Hour)
		}, false},
		{"missing opening", func(t *testing.T, s *Store, c *nexovcc.Card, f *nexovcc.Finance) { f.Funds = f.Funds[1:] }, false},
		{"topup explains balance", func(t *testing.T, s *Store, c *nexovcc.Card, f *nexovcc.Finance) {
			f.Funds = append(f.Funds, nexovcc.Fund{ID: "topup", Serial: c.Serial, Kind: 4001, Currency: "USD", Amount: "-141.21", At: c.BalanceAt.Add(-10 * time.Second)})
		}, false},
		{"unmatched payment", func(t *testing.T, s *Store, c *nexovcc.Card, f *nexovcc.Finance) {
			tr := f.Trades[0]
			tr.ID = "unmatched"
			tr.Authorization = "OTHER"
			f.Trades = append(f.Trades, tr)
		}, false},
		{"unknown fund", func(t *testing.T, s *Store, c *nexovcc.Card, f *nexovcc.Finance) { f.Funds[1].Kind = 99999 }, false},
		{"settled awaiting refund", func(t *testing.T, s *Store, c *nexovcc.Card, f *nexovcc.Finance) {
			tr := f.Trades[0]
			tr.ID = "settled"
			tr.Status = "COMPLETE"
			f.Trades = append(f.Trades, tr)
		}, false},
		{"invalid currency", func(t *testing.T, s *Store, c *nexovcc.Card, f *nexovcc.Finance) { f.Funds[0].Currency = "PHP" }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := transferStore(t)
			card, f := balanceWithdrawalFixture(t, s)
			if tc.mutate != nil {
				tc.mutate(t, s, &card, &f)
			}
			if _, err := s.SaveNexovccCard(card, nil); err != nil {
				t.Fatal(err)
			}
			if err := s.NexovccCache("finance", f); err != nil {
				t.Fatal(err)
			}
			proof, err := s.NexovccBalanceWithdrawal(card.ID)
			if err != nil || (proof != nil) != tc.allow {
				t.Fatalf("candidate: proof=%+v err=%v", proof, err)
			}
			n, _ := nexovcc.Cents(card.Balance)
			op := nexovcc.Operation{ID: "nex-recovered1", Action: "withdraw_all", CardID: card.ID, Serial: card.Serial, Amount: nexovcc.Money(max(int64(0), n-100)), BeforeBalance: card.Balance, AutoWithdraw: true, Status: "submitted", CreatedAt: time.Now()}
			err = s.BeginNexovccOperation(op)
			if (err == nil) != tc.allow {
				t.Fatalf("final admission: %v", err)
			}
			if !tc.allow {
				return
			}
			op, err = s.NexovccOperation(op.ID)
			if err != nil || op.BalanceWithdrawal == nil || op.BalanceWithdrawal.LedgerBalance != "28.18" {
				t.Fatal(op, err)
			}
			op.Status = "succeeded"
			if err = s.SaveNexovccOperation(op); err != nil {
				t.Fatal(err)
			}
			local, _, err := s.GPTPayCard(card.ID)
			if err != nil || local.ActiveAccounts != 1 || local.Available {
				t.Fatal("withdrawal released occupancy", local, err)
			}
			for _, action := range []string{"cancel", "withdraw_cancel", "withdraw_all"} {
				other := nexovcc.Operation{ID: "nex-other-" + action, Action: action, CardID: card.ID, Status: "submitted", CapacityRecycle: action != "withdraw_all", AutoWithdraw: action == "withdraw_all"}
				if err = s.BeginNexovccOperation(other); err == nil {
					t.Fatal("missing refund allowed closing/duplicate", action)
				}
			}
		})
	}
}

func TestNexovccBalanceWithdrawalCannotAuthorizeClosing(t *testing.T) {
	for _, action := range []string{"cancel", "withdraw_cancel", "withdraw"} {
		t.Run(action, func(t *testing.T) {
			s := transferStore(t)
			card, _ := balanceWithdrawalFixture(t, s)
			proof, err := s.NexovccBalanceWithdrawal(card.ID)
			if err != nil || proof == nil {
				t.Fatal(err)
			}
			op := nexovcc.Operation{ID: fmt.Sprintf("nex-close-%s", action), Action: action, CardID: card.ID, AutoWithdraw: true, BalanceWithdrawal: proof, Status: "submitted"}
			if err = s.BeginNexovccOperation(op); err == nil {
				t.Fatal("balance exception authorized close or arbitrary withdrawal")
			}
		})
	}
}

func TestNexovccBalanceWithdrawalBlocksRunningWork(t *testing.T) {
	for _, stage := range []string{"running", "waiting_quota", "awaiting_push", "merge", "manual"} {
		t.Run(stage, func(t *testing.T) {
			s := transferStore(t)
			card, _ := balanceWithdrawalFixture(t, s)
			transferSource(t, s, "paid@example.com")
			if _, err := s.UpdateProAccount("paid@example.com", func(p *model.MailAccountProfile) {
				p.ProAuto.CardID = card.ID
				switch stage {
				case "merge":
					p.ProWorkflowRunning = true
				case "manual":
					p.ProManualStages = map[string]model.ProStageProgress{"recharge": {Status: "running"}}
				default:
					p.ProAuto.Status = stage
				}
			}); err != nil {
				t.Fatal(err)
			}
			if proof, err := s.NexovccBalanceWithdrawal(card.ID); err != nil || proof != nil {
				t.Fatal("running work allowed candidate", proof, err)
			}
			if err := s.BeginNexovccOperation(nexovcc.Operation{ID: "nex-running1", Action: "withdraw_all", AutoWithdraw: true, CardID: card.ID, Amount: "168.39", BeforeBalance: card.Balance, Status: "submitted"}); err == nil {
				t.Fatal("running work allowed transfer")
			}
		})
	}
}

func TestNexovccBalanceWithdrawalRechecksCandidate(t *testing.T) {
	s := transferStore(t)
	card, _ := balanceWithdrawalFixture(t, s)
	if proof, err := s.NexovccBalanceWithdrawal(card.ID); err != nil || proof == nil {
		t.Fatal(proof, err)
	}
	if _, err := s.db.Exec(`INSERT INTO pro_card_reservations VALUES('new-reservation@example.com',?)`, proCardKey("4242424242424242")); err != nil {
		t.Fatal(err)
	}
	if err := s.BeginNexovccOperation(nexovcc.Operation{ID: "nex-reserved1", Action: "withdraw_all", AutoWithdraw: true, CardID: card.ID, Amount: "168.39", BeforeBalance: card.Balance, Status: "submitted"}); err == nil {
		t.Fatal("new reservation bypassed final admission")
	}
}
