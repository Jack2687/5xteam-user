package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Classification is independent of issuer provenance and belongs to the
// physical card. Keep it after deletion so aliases/history cannot lose it.
func (s *Store) initializeProCardTypes() error {
	_, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS pro_card_types(card_key TEXT PRIMARY KEY,card_type TEXT NOT NULL CHECK(card_type IN ('ejh','other','nexovcc')));
 CREATE TABLE IF NOT EXISTS pro_card_identities(card_id TEXT PRIMARY KEY,card_key TEXT NOT NULL);
 CREATE INDEX IF NOT EXISTS pro_card_identity_key ON pro_card_identities(card_key);
 INSERT OR IGNORE INTO pro_card_types SELECT DISTINCT card_key,'other' FROM pro_card_usage;`)
	if err != nil {
		return err
	}
	var schema string
	if err = s.db.QueryRow(`SELECT sql FROM sqlite_master WHERE type='table' AND name='pro_card_types'`).Scan(&schema); err != nil {
		return err
	}
	if !strings.Contains(schema, "nexovcc") {
		tx, e := s.db.Begin()
		if e != nil {
			return e
		}
		defer tx.Rollback()
		if _, e = tx.Exec(`CREATE TABLE pro_card_types_next(card_key TEXT PRIMARY KEY,card_type TEXT NOT NULL CHECK(card_type IN ('ejh','other','nexovcc')));
   INSERT INTO pro_card_types_next SELECT * FROM pro_card_types;
   DROP TABLE pro_card_types; ALTER TABLE pro_card_types_next RENAME TO pro_card_types;`); e != nil {
			return e
		}
		if e = tx.Commit(); e != nil {
			return e
		}
	}
	rows, err := s.db.Query(`SELECT id,encrypted_secret FROM gptpay_cards`)
	if err != nil {
		return err
	}
	type identity struct{ id, key string }
	items := []identity{}
	for rows.Next() {
		var id, sealed string
		if err = rows.Scan(&id, &sealed); err != nil {
			break
		}
		var plain string
		plain, err = s.decrypt(sealed)
		if err != nil {
			break
		}
		var secret struct {
			Number string `json:"cardNumber"`
		}
		if err = json.Unmarshal([]byte(plain), &secret); err != nil {
			break
		}
		items = append(items, identity{id, proCardKey(secret.Number)})
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, item := range items {
		// All pre-feature data defaults to Other, even if its source was EJH.
		if _, err = tx.Exec(`INSERT OR IGNORE INTO pro_card_types VALUES(?,'other')`, item.key); err != nil {
			return err
		}
		if _, err = tx.Exec(`INSERT OR IGNORE INTO pro_card_identities VALUES(?,?)`, item.id, item.key); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func proCardType(q proCapacityQuery, key string) (string, error) {
	var kind string
	err := q.QueryRow(`SELECT card_type FROM pro_card_types WHERE card_key=?`, key).Scan(&kind)
	if errors.Is(err, sql.ErrNoRows) {
		return "other", nil
	}
	return kind, err
}
func (s *Store) ProCardTypeForNumber(number string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return proCardType(s.db, proCardKey(number))
}
func saveProCardTypeTx(tx *sql.Tx, id, key, kind string) error {
	if kind != "" && kind != "ejh" && kind != "other" && kind != "nexovcc" {
		return fmt.Errorf("银行卡类型只能是 EJH、环球或其他")
	}
	old, err := proCardType(tx, key)
	if err != nil {
		return err
	}
	var oldKey string
	e := tx.QueryRow(`SELECT card_key FROM pro_card_identities WHERE card_id=?`, id).Scan(&oldKey)
	if e != nil && !errors.Is(e, sql.ErrNoRows) {
		return e
	}
	if old == "nexovcc" && kind != "" && kind != "nexovcc" {
		return fmt.Errorf("环球绑定卡不能更改卡台类型")
	}
	if oldKey != "" && oldKey != key {
		prior, e := proCardType(tx, oldKey)
		if e != nil {
			return e
		}
		if prior == "nexovcc" {
			return fmt.Errorf("环球绑定卡不能替换卡号")
		}
		if prior == "ejh" {
			n, _, e := ejhCardOccupancy(tx, oldKey, "")
			if e != nil {
				return e
			}
			if n > 0 {
				return fmt.Errorf("EJH 卡仍有运行账号，不能替换卡号")
			}
		}
	}
	if old == "ejh" && kind == "other" {
		n, _, e := ejhCardOccupancy(tx, key, "")
		if e != nil {
			return e
		}
		if n > 0 {
			return fmt.Errorf("EJH 卡仍有占用账号，请回款确认到账后再修改类型")
		}
	}
	if kind == "" {
		kind = old
	}
	if _, err = tx.Exec(`INSERT INTO pro_card_types VALUES(?,?) ON CONFLICT(card_key) DO UPDATE SET card_type=excluded.card_type`, key, kind); err != nil {
		return err
	}
	_, err = tx.Exec(`INSERT INTO pro_card_identities VALUES(?,?) ON CONFLICT(card_id) DO UPDATE SET card_key=excluded.card_key`, id, key)
	return err
}
