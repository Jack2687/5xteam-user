package store

import (
	"chatgpt-space-merge/internal/model"
	"database/sql"
	"encoding/json"
)

func proCardDead(q proCapacityQuery, key string) (bool, error) {
	var dead bool
	err := q.QueryRow(`SELECT EXISTS(SELECT 1 FROM pro_dead_cards WHERE card_key=?)`, key).Scan(&dead)
	return dead, err
}

// Card bans survive deletion, reimport and issuer synchronization. A dead
// account never releases an unknown payment; successful orders may be closed.
func recordProDeadTx(tx *sql.Tx, p, previous model.MailAccountProfile) error {
	if p.ProDead == nil {
		return nil
	}
	if _, err := tx.Exec(`INSERT INTO pro_dead_accounts(email,profile) VALUES(?,?) ON CONFLICT(email) DO UPDATE SET profile=excluded.profile`, p.Email, mustJSON(p.ProDead)); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT OR IGNORE INTO pro_dead_cards(card_key,email,profile)
 SELECT card_key,?,? FROM pro_card_usage WHERE email=?
 UNION SELECT card_key,?,? FROM pro_card_reservations WHERE email=?
 UNION SELECT card_key,?,? FROM pro_card_identities WHERE card_id=?`, p.Email, mustJSON(p.ProDead), p.Email, p.Email, mustJSON(p.ProDead), p.Email, p.Email, mustJSON(p.ProDead), p.ProAuto.CardID); err != nil {
		return err
	}
	if previous.ProDead == nil {
		c, err := readRestockConfig(tx)
		if err != nil {
			return err
		}
		if c.DeadAccountPause && c.Enabled {
			c.Enabled = false
			c.Version++
			if _, err = tx.Exec(`INSERT INTO pro_restock_config VALUES(1,?) ON CONFLICT(id) DO UPDATE SET payload=excluded.payload`, mustJSON(c)); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Store) ProDeadPending() ([]model.MailAccountProfile, error) {
	rows, err := s.db.Query(`SELECT profile FROM mail_accounts WHERE json_extract(profile,'$.pro_dead') IS NOT NULL AND coalesce(json_extract(profile,'$.pro_post_merge_groups.status'),'')<>'completed'`)
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
