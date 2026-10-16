package store

import (
	"database/sql"
	"errors"
	"fmt"
	"math/big"
	"regexp"
	"strings"
	"time"

	"chapt-space-user/internal/ejh"
)

type EJHRechargeLedger struct {
	Amount    string     `json:"amount"`
	UpdatedAt *time.Time `json:"updated_at,omitempty"`
}

var ejhLedgerAmount = regexp.MustCompile(`^\d{1,12}(\.\d{1,2})?$`)

func (s *Store) EJHRechargeLedger() (EJHRechargeLedger, error) {
	var out EJHRechargeLedger
	err := s.EJHGet("recharge_ledger", "manual-usd", &out)
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	}
	return out, err
}

func (s *Store) SaveEJHRechargeLedger(amount string) (EJHRechargeLedger, error) {
	var out EJHRechargeLedger
	amount = strings.TrimSpace(amount)
	if amount != "" {
		if !ejhLedgerAmount.MatchString(amount) {
			return out, fmt.Errorf("充值总账须为非负 USD 金额，最多 12 位整数和 2 位小数")
		}
		value, err := ejh.Money(amount)
		if err != nil {
			return out, err
		}
		amount = value.FloatString(2)
	}
	now := time.Now()
	out = EJHRechargeLedger{Amount: amount, UpdatedAt: &now}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	var previous EJHRechargeLedger
	if err = s.ejhGetTx(tx, "recharge_ledger", "manual-usd", &previous); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return out, err
	}
	if previous.Amount == amount {
		return previous, nil
	}
	if err = s.ejhPutTx(tx, "recharge_ledger", "manual-usd", out); err != nil {
		return out, err
	}
	before, after := new(big.Rat), new(big.Rat)
	if previous.Amount != "" {
		before.SetString(previous.Amount)
	}
	if amount != "" {
		after.SetString(amount)
	}
	event := EJHRechargeEvent{ID: EJHID("recharge", now.String()), At: now, Before: previous.Amount, After: amount, Delta: new(big.Rat).Sub(after, before).FloatString(2), Baseline: previous.Amount == ""}
	if err = s.ejhPutTx(tx, "finance_recharge", event.ID, event); err != nil {
		return out, err
	}
	return out, tx.Commit()
}
