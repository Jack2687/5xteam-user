package store

import (
	"chatgpt-space-merge/internal/model"
	"database/sql"
	"encoding/json"
	"errors"
)

func captureProOrderTimeout(tx *sql.Tx, email string) error {
	var raw string
	err := tx.QueryRow("SELECT profile FROM mail_accounts WHERE email=?", email).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	var p model.MailAccountProfile
	if err = json.Unmarshal([]byte(raw), &p); err != nil {
		return err
	}
	if p.ManagementScope != "pro" || p.ProPushTimeoutEnabled != nil || p.ProPushStartedAt != nil || p.ProAuto.ID != "" {
		return nil
	}
	// Never backfill historical successful/uncertain purchases using today's switch.
	var prior int
	if err = tx.QueryRow("SELECT count(*) FROM gptpay_orders WHERE email=? AND json_extract(profile,'$.status')!='failed'", email).Scan(&prior); err != nil {
		return err
	}
	if prior > 0 {
		return nil
	}
	c := model.DefaultProSettings()
	err = tx.QueryRow("SELECT profile FROM pro_settings WHERE id=1").Scan(&raw)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err == nil {
		if err = json.Unmarshal([]byte(raw), &c); err != nil {
			return err
		}
	}
	p.CaptureProPushTimeout(c)
	_, err = tx.Exec("UPDATE mail_accounts SET profile=? WHERE email=?", mustJSON(p), email)
	return err
}

// Complete profile scan with error propagation: a read failure is never an empty pool.
func (s *Store) ProRestockProfiles() ([]model.MailAccountProfile, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.db.Query("SELECT profile FROM mail_accounts WHERE json_extract(profile,'$.management_scope')='pro'")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.MailAccountProfile{}
	for rows.Next() {
		var raw string
		var p model.MailAccountProfile
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		if err = json.Unmarshal([]byte(raw), &p); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
