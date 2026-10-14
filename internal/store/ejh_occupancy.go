package store

import (
	"database/sql"
	"fmt"
)

// Orders and pre-payment reservations are the durable occupancy source. A
// successful order stays occupied independently of account/card deletion,
// worker lifetime, refund state, or whether the feature is currently enabled.
// Releases are per order, so a later purchase cannot inherit an old removal.
func (s *Store) initializeEJHOccupancy() error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`CREATE TABLE IF NOT EXISTS pro_card_admission_lock(id INTEGER PRIMARY KEY CHECK(id=1));
 INSERT OR IGNORE INTO pro_card_admission_lock VALUES(1);
 CREATE TABLE IF NOT EXISTS pro_card_order_releases(order_id TEXT PRIMARY KEY, released_at TEXT NOT NULL);
 CREATE TABLE IF NOT EXISTS ejh_occupancy_migrations(id INTEGER PRIMARY KEY);`); err != nil {
		return err
	}
	var initialized bool
	if err = tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM ejh_occupancy_migrations WHERE id=1)`).Scan(&initialized); err != nil {
		return err
	}
	if !initialized {
		// One-time upgrade only: never repeatedly infer release from a stale account
		// status on startup. Missing accounts and unresolved payments remain held.
		if _, err = tx.Exec(`INSERT OR IGNORE INTO pro_card_order_releases
   SELECT u.order_id,m.updated_at FROM pro_card_usage u JOIN mail_accounts m ON m.email=u.email
   WHERE u.status='success' AND json_extract(m.profile,'$.pro_remove_status') IN ('completed','team_removed')`); err != nil {
			return err
		}
		if _, err = tx.Exec(`INSERT INTO ejh_occupancy_migrations VALUES(1)`); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Acquire SQLite's write lock BEFORE reading availability, including across
// Store instances/processes. No supplier request happens in this transaction.
func lockProCardAdmission(tx *sql.Tx) error {
	_, err := tx.Exec(`UPDATE pro_card_admission_lock SET id=id WHERE id=1`)
	return err
}

func ejhSingleActive(q proCapacityQuery, key string) (bool, error) {
	var enabled bool
	err := q.QueryRow(`SELECT EXISTS(SELECT 1 FROM pro_card_types WHERE card_key=? AND card_type='ejh')
 AND COALESCE((SELECT json_extract(profile,'$.single_active_account') FROM ejh_settings WHERE id=1),1)`, key).Scan(&enabled)
	return enabled, err
}

func ejhCardOccupancy(q proCapacityQuery, key, exceptEmail string) (count int, email string, err error) {
	err = q.QueryRow(`SELECT count(*),coalesce(min(email),'') FROM (
  SELECT lower(email) email FROM pro_card_reservations WHERE card_key=?
  UNION SELECT lower(u.email) FROM pro_card_usage u WHERE u.card_key=? AND u.status<>'failed'
  AND (u.status<>'success' OR NOT EXISTS(SELECT 1 FROM pro_card_order_releases r WHERE r.order_id=u.order_id))
 ) WHERE email<>? COLLATE NOCASE`, key, key, exceptEmail).Scan(&count, &email)
	return
}

func validateEJHOccupancy(q proCapacityQuery, key, email string) error {
	enabled, err := ejhSingleActive(q, key)
	if err != nil || !enabled {
		return err
	}
	count, owner, err := ejhCardOccupancy(q, key, email)
	if err != nil {
		return err
	}
	if count > 0 {
		return fmt.Errorf("EJH 银行卡正在被 %s 占用（一卡同时一个号），须移出成功后才可分配其他账号", owner)
	}
	return nil
}

// Called in the same transaction as the successful removal state transition.
// Payment uncertainty is deliberately not released by a space status update.
func releaseProCardOrdersTx(tx *sql.Tx, email, at string) error {
	_, err := tx.Exec(`INSERT OR IGNORE INTO pro_card_order_releases(order_id,released_at)
 SELECT order_id,? FROM pro_card_usage WHERE email=? COLLATE NOCASE AND status='success'`, at, email)
	return err
}
