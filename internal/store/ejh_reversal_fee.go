package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"chatgpt-space-merge/internal/ejh"
)

// Freeze the configured estimate at the effective return state, including
// audited manual confirmations. This never changes issuer evidence or state.
// Call before changing settings so legacy completed returns keep the old rate.
func (s *Store) snapshotEJHReversalFeesTx(tx *sql.Tx) error {
	cfg := ejh.DefaultSettings()
	var raw string
	err := tx.QueryRow("SELECT profile FROM ejh_settings WHERE id=1").Scan(&raw)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if raw != "" {
		if err = json.Unmarshal([]byte(raw), &cfg); err != nil {
			return err
		}
	}
	cases, err := s.ejhCasesQuery(tx, nil)
	if err != nil {
		return err
	}
	for _, c := range cases {
		if c.CardType != "ejh" {
			continue
		}
		if c.State == "returned" {
			_, err = tx.Exec(`INSERT INTO ejh_reversal_fee_rates(order_id,percent,locked_at) VALUES(?,?,?) ON CONFLICT(order_id) DO NOTHING`, c.ID, cfg.ReversalFeePercent, formatTime(time.Now()))
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func ejhReversalFeeRates(q ejhCaseQuery) (map[string]*ejh.ReversalFeeRate, error) {
	rows, err := q.Query("SELECT order_id,percent,locked_at FROM ejh_reversal_fee_rates")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]*ejh.ReversalFeeRate{}
	for rows.Next() {
		var id, at string
		v := new(ejh.ReversalFeeRate)
		if err = rows.Scan(&id, &v.Percent, &at); err != nil {
			return nil, err
		}
		if err = v.LockedAt.UnmarshalText([]byte(at)); err != nil {
			return nil, err
		}
		out[id] = v
	}
	return out, rows.Err()
}
