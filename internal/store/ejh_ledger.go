package store

import (
	"database/sql"
	"errors"
	"fmt"
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
	return out, s.EJHPut("recharge_ledger", "manual-usd", out)
}
