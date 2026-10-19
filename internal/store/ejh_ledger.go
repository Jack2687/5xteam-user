package store

import (
	"database/sql"
	"errors"
	"fmt"
	"math/big"
	"regexp"
	"strings"
	"time"

	"chatgpt-space-merge/internal/ejh"
)

type EJHRechargeLedger struct {
	HistoryMonthRecordedAt *time.Time `json:"history_month_recorded_at,omitempty"`
	HistoryMonth           string     `json:"history_month,omitempty"`
	Amount                 string     `json:"amount"`
	UpdatedAt              *time.Time `json:"updated_at,omitempty"`
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
	out.HistoryMonth = previous.HistoryMonth
	out.HistoryMonthRecordedAt = previous.HistoryMonthRecordedAt
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

// Assign only the undated historical recharge to a known month. The balance,
// dated recharge events and their audit timestamps are never rewritten.
func (s *Store) SaveEJHRechargeHistoryMonth(month string) (EJHRechargeLedger, error) {
	month = strings.TrimSpace(month)
	if month != "" {
		parsed, err := time.Parse("2006-01", month)
		if err != nil || parsed.Year() < 2000 || parsed.Year() > 2100 {
			return EJHRechargeLedger{}, fmt.Errorf("请输入有效的充值归属月份")
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return EJHRechargeLedger{}, err
	}
	defer tx.Rollback()
	var out EJHRechargeLedger
	if err = s.ejhGetTx(tx, "recharge_ledger", "manual-usd", &out); err != nil {
		return out, err
	}
	out.HistoryMonth = month
	now := time.Now()
	out.HistoryMonthRecordedAt = &now
	if err = s.ejhPutTx(tx, "recharge_ledger", "manual-usd", out); err != nil {
		return out, err
	}
	return out, tx.Commit()
}
