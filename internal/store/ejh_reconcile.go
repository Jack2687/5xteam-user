package store

import (
	"chatgpt-space-merge/internal/ejh"
	"encoding/json"
	"reflect"
	"strings"
	"time"
)

func (s *Store) EJHReconcile() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ejhReconcileLocked()
}

func (s *Store) ejhReconcileLocked() error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = lockProCardAdmission(tx); err != nil {
		return err
	}
	cases, err := s.ejhCasesQuery(tx, nil)
	if err != nil {
		return err
	}
	rows, err := tx.Query("SELECT sealed FROM ejh_records WHERE kind='trade'")
	if err != nil {
		return err
	}
	trades := []ejh.TradeRecord{}
	for rows.Next() {
		var sealed string
		if err = rows.Scan(&sealed); err != nil {
			break
		}
		var raw string
		raw, err = s.decrypt(sealed)
		if err != nil {
			break
		}
		var t ejh.TradeRecord
		if err = json.Unmarshal([]byte(raw), &t); err != nil {
			break
		}
		trades = append(trades, t)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return err
	}
	oldCases, oldTrades := append([]ejh.Case(nil), cases...), append([]ejh.TradeRecord(nil), trades...)
	now := time.Now()
	ejh.AutoReconcile(cases, trades, now)
	for i, c := range cases {
		if c.OriginalTradeID == oldCases[i].OriginalTradeID && c.ChargeEvidence == oldCases[i].ChargeEvidence {
			continue
		}
		// Update only attribution fields, retaining the original lifecycle record.
		var raw string
		if err = tx.QueryRow("SELECT profile FROM ejh_cases WHERE id=?", c.ID).Scan(&raw); err != nil {
			return err
		}
		var saved ejh.Case
		if err = json.Unmarshal([]byte(raw), &saved); err != nil {
			return err
		}
		saved.OriginalTradeID, saved.ChargeEvidence, saved.ChargeLinkedAt = c.OriginalTradeID, c.ChargeEvidence, c.ChargeLinkedAt
		if _, err = tx.Exec("UPDATE ejh_cases SET profile=? WHERE id=?", mustJSON(saved), c.ID); err != nil {
			return err
		}
		if err = s.ejhPutTx(tx, "match_audit", EJHID("audit", c.ID+now.String()), map[string]any{"action": "automatic_charge", "before": oldCases[i], "after": saved, "at": now}); err != nil {
			return err
		}
	}
	for i, t := range trades {
		// Also backfill unchanged historical confirmations during startup.
		if err = saveEJHReceivedReturnTx(tx, t); err != nil {
			return err
		}
		if reflect.DeepEqual(t, oldTrades[i]) {
			continue
		}
		if !strings.HasPrefix(t.Source, "auto_") && !strings.HasPrefix(oldTrades[i].Source, "auto_") {
			continue
		}
		if err = s.ejhPutTx(tx, "trade", t.ID, t); err != nil {
			return err
		}
		if _, err = tx.Exec("DELETE FROM ejh_return_links WHERE trade_id=?", t.ID); err != nil {
			return err
		}
		if t.OrderID != "" {
			if _, err = tx.Exec("INSERT INTO ejh_return_links VALUES(?,?)", t.ID, t.OrderID); err != nil {
				return err
			}
		}
		if err = s.ejhPutTx(tx, "match_audit", EJHID("audit", t.ID+now.String()), map[string]any{"action": "automatic_return", "before": oldTrades[i], "after": t, "at": now}); err != nil {
			return err
		}
	}
	return tx.Commit()
}
