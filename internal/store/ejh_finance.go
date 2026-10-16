package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"time"

	"chapt-space-user/internal/ejh"
	"chapt-space-user/internal/model"
)

var financeZone = time.FixedZone("Asia/Shanghai", 8*3600)

type EJHRechargeEvent struct {
	ID       string    `json:"id"`
	At       time.Time `json:"at"`
	Before   string    `json:"before"`
	After    string    `json:"after"`
	Delta    string    `json:"delta"`
	Baseline bool      `json:"baseline"`
}

type EJHConsumption struct {
	ID       string    `json:"id"`
	Email    string    `json:"email"`
	Total    float64   `json:"total"`
	Baseline float64   `json:"baseline"`
	At       time.Time `json:"at"`
}
type EJHConsumptionDay struct {
	ID     string    `json:"id"`
	Email  string    `json:"email"`
	Day    string    `json:"day"`
	Amount float64   `json:"amount"`
	At     time.Time `json:"at"`
}
type EJHFinancialSnapshot struct {
	At      time.Time         `json:"at"`
	Ledger  EJHRechargeLedger `json:"recharge_ledger"`
	Balance json.RawMessage   `json:"balance"`
	Cards   []ejh.CardRecord  `json:"cards"`
	Cases   []ejh.Case        `json:"cases"`
}

func (s *Store) ejhGetTx(tx *sql.Tx, kind, id string, out any) error {
	var sealed string
	if err := tx.QueryRow(`SELECT sealed FROM ejh_records WHERE kind=? AND id=?`, kind, id).Scan(&sealed); err != nil {
		return err
	}
	raw, err := s.decrypt(sealed)
	if err != nil {
		return err
	}
	return json.Unmarshal([]byte(raw), out)
}

// Save alongside the Pro snapshot. Repeated samples and counter resets cannot
// inflate usage, and deletion of credentials cannot delete financial history.
func (s *Store) recordEJHConsumptionTx(tx *sql.Tx, p model.MailAccountProfile, now time.Time) error {
	if p.UserCostDownstreamIdentity == "" || math.IsNaN(p.UserCostDownstreamSnapshot) || math.IsInf(p.UserCostDownstreamSnapshot, 0) || p.UserCostDownstreamSnapshot < 0 {
		return nil
	}
	var eligible bool
	if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM ejh_cases c JOIN pro_card_identities i ON i.card_id=c.card_id JOIN pro_card_types k ON k.card_key=i.card_key WHERE c.email=? AND k.card_type='ejh' AND json_extract(c.profile,'$.payment_status')='success')`, p.Email).Scan(&eligible); err != nil {
		return err
	}
	if !eligible {
		return nil
	}
	binding := p.CostConnectionIdentity
	if binding == "" {
		binding = strings.ToLower(p.Email)
	}
	id := EJHID("usage", binding+"|"+p.UserCostDownstreamIdentity)
	var previous EJHConsumption
	err := s.ejhGetTx(tx, "finance_usage", id, &previous)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if errors.Is(err, sql.ErrNoRows) {
		previous = EJHConsumption{ID: id, Email: p.Email, Total: p.UserCostDownstreamSnapshot, Baseline: p.UserCostDownstreamSnapshot, At: now}
		return s.ejhPutTx(tx, "finance_usage", id, previous)
	}
	if p.UserCostDownstreamSnapshot <= previous.Total {
		return nil
	}
	day := now.In(financeZone).Format("2006-01-02")
	dayID := id + ":" + day
	var entry EJHConsumptionDay
	if err = s.ejhGetTx(tx, "finance_usage_day", dayID, &entry); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	entry.ID, entry.Email, entry.Day, entry.At = dayID, p.Email, day, now
	entry.Amount += p.UserCostDownstreamSnapshot - previous.Total
	previous.Total, previous.At = p.UserCostDownstreamSnapshot, now
	if err = s.ejhPutTx(tx, "finance_usage_day", dayID, entry); err != nil {
		return err
	}
	return s.ejhPutTx(tx, "finance_usage", id, previous)
}

// Retain first and last saved balance observations per Beijing date. No issuer
// calls, and no credentials/card secrets are copied into these snapshots.
func (s *Store) CaptureEJHFinance(now time.Time) error {
	cards, err := EJHRecords[ejh.CardRecord](s, "card")
	if err != nil {
		return err
	}
	cases, err := s.EJHCases()
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	rows, err := tx.Query(`SELECT DISTINCT m.profile FROM mail_accounts m JOIN ejh_cases c ON c.email=m.email`)
	if err != nil {
		return err
	}
	var profiles []model.MailAccountProfile
	for rows.Next() {
		var raw string
		if err = rows.Scan(&raw); err != nil {
			rows.Close()
			return err
		}
		var p model.MailAccountProfile
		if err = json.Unmarshal([]byte(raw), &p); err != nil {
			rows.Close()
			return err
		}
		profiles = append(profiles, p)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, p := range profiles {
		if err = s.recordEJHConsumptionTx(tx, p, now); err != nil {
			return err
		}
	}
	snap := EJHFinancialSnapshot{At: now, Cards: []ejh.CardRecord{}, Cases: []ejh.Case{}}
	if err = s.ejhGetTx(tx, "recharge_ledger", "manual-usd", &snap.Ledger); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err = s.ejhGetTx(tx, "balance", "latest", &snap.Balance); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	for _, c := range cards {
		if c.Remote.Status == "2" {
			continue
		}
		var visible bool
		if err = tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM ejh_card_links l JOIN pro_card_types k ON k.card_key=l.card_key WHERE l.card_id=? AND k.card_type='ejh')`, c.ID).Scan(&visible); err != nil {
			return err
		}
		if !visible {
			continue
		}
		c.Remote = ejh.Card{OrderNo: c.Remote.OrderNo, Status: c.Remote.Status, CardCurrency: c.Remote.CardCurrency}
		snap.Cards = append(snap.Cards, c)
	}
	for _, c := range cases {
		if c.CardType == "ejh" && (c.State == "opened" || c.State == "waiting") {
			snap.Cases = append(snap.Cases, ejh.Case{ID: c.ID, CardID: c.CardID, EJHCardID: c.EJHCardID, State: c.State, Charge: c.Charge})
		}
	}
	day := now.In(financeZone).Format("2006-01-02")
	var first EJHFinancialSnapshot
	if err = s.ejhGetTx(tx, "finance_snapshot", day+":first", &first); errors.Is(err, sql.ErrNoRows) {
		if err = s.ejhPutTx(tx, "finance_snapshot", day+":first", snap); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	if err = s.ejhPutTx(tx, "finance_snapshot", day+":last", snap); err != nil {
		return err
	}
	return tx.Commit()
}
