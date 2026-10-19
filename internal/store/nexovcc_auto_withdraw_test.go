package store

import (
	"fmt"
	"testing"
	"time"

	"chatgpt-space-merge/internal/nexovcc"
)

func TestNexovccAutoWithdrawFinalAdmissionUsesEffectiveLimit(t *testing.T) {
	for _, tc := range []struct {
		name                   string
		defaultLimit, override int
		autoWithdraw, recycle  bool
		allowed                bool
	}{
		{"default not reached", 2, 0, true, false, false},
		{"default reached", 1, 0, true, false, true},
		{"single card raised", 1, 2, true, false, false},
		{"single card lowered", 2, 1, true, false, true},
		{"manual below limit", 2, 0, false, false, true},
		{"recycle below limit", 2, 0, false, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := transferStore(t)
			card := nexStoreCard(t, s)
			key := proCardKey("4242424242424242")
			// One completed and refunded activation; failed attempts must not
			// satisfy the two-account lifetime limit.
			if _, err := s.db.Exec(`INSERT INTO pro_card_usage VALUES('paid',?,'paid@example.com','success'),('failed',?,'failed@example.com','failed')`, key, key); err != nil {
				t.Fatal(err)
			}
			if _, err := s.db.Exec(`INSERT INTO nexovcc_case_matches VALUES('paid','refund',1,'{}')`); err != nil {
				t.Fatal(err)
			}
			cfg, _, err := s.NexovccSettings()
			if err != nil {
				t.Fatal(err)
			}
			cfg.DefaultMaxAccounts = tc.defaultLimit
			if _, err = s.SaveNexovccSettings(cfg, nexovcc.Credentials{}, false); err != nil {
				t.Fatal(err)
			}
			if tc.override > 0 {
				local, secret, err := s.GPTPayCard(card.ID)
				if err != nil {
					t.Fatal(err)
				}
				local.MaxAccounts = &tc.override
				if _, err = s.SaveGPTPayCard(local, secret); err != nil {
					t.Fatal(err)
				}
			}
			op := nexovcc.Operation{ID: "nex-autowd-limit", Action: "withdraw_all", CardID: card.ID, Serial: card.Serial, Status: "submitted", AutoWithdraw: tc.autoWithdraw, CapacityRecycle: tc.recycle, CreatedAt: time.Now()}
			if tc.recycle {
				op.Action = "withdraw_cancel"
			}
			err = s.BeginNexovccOperation(op)
			if (err == nil) != tc.allowed {
				t.Fatalf("unexpected admission: %v", err)
			}
			if !tc.allowed {
				if _, err = s.NexovccOperation(op.ID); err == nil {
					t.Fatal("blocked withdrawal left an operation lock")
				}
			}
		})
	}
}

func TestNexovccAutomaticFundsStillRequireEveryRefund(t *testing.T) {
	for _, recycle := range []bool{false, true} {
		for _, status := range []string{"success", "submitted"} {
			t.Run(fmt.Sprintf("recycle=%v/status=%s", recycle, status), func(t *testing.T) {
				s := transferStore(t)
				card := nexStoreCard(t, s)
				key := proCardKey("4242424242424242")
				if _, err := s.db.Exec(`INSERT INTO pro_card_usage VALUES('unpaid',?,'unpaid@example.com',?)`, key, status); err != nil {
					t.Fatal(err)
				}
				// Even an operator-marked refund must not replace actual evidence.
				if _, err := s.db.Exec(`INSERT INTO nexovcc_case_states(order_id,state,reason,revision,updated_at) VALUES('unpaid','returned','manual',1,?)`, time.Now().Format(time.RFC3339)); err != nil {
					t.Fatal(err)
				}
				op := nexovcc.Operation{ID: "nex-autowd-refund", Action: "withdraw_all", CardID: card.ID, Status: "submitted", AutoWithdraw: !recycle, CapacityRecycle: recycle}
				if recycle {
					op.Action = "withdraw_cancel"
				}
				if err := s.BeginNexovccOperation(op); err == nil {
					t.Fatal("unrefunded/undecided payment allowed automatic funds operation")
				}
			})
		}
	}
}
