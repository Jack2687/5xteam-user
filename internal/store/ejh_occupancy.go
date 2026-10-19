package store

import (
	"database/sql"
	"fmt"

	"chatgpt-space-merge/internal/gptpay"
)

// Orders and pre-payment reservations are the durable occupancy source. A
// successful order stays occupied independently of account/card deletion,
// worker lifetime, removal state, or whether the feature is currently enabled.
// Confirmed returns or explicit audited manual states release successful orders.
// Legacy removal releases
// remain historical records and are never used for EJH admission.
func (s *Store) initializeEJHOccupancy() error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Legacy mailbox deletion could leave a pre-payment reservation without an
	// account. Only remove these orphan reservations; submitted orders retain
	// their independent usage records, including uncertain payment outcomes.
	if _, err = tx.Exec(`DELETE FROM pro_card_reservations WHERE NOT EXISTS
 (SELECT 1 FROM mail_accounts m WHERE m.email=pro_card_reservations.email)`); err != nil {
		return err
	}
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

// Unknown payments always hold occupancy. Manual corrections override only
// terminal orders; real return evidence is retained for restoring auto.
const ejhOrderOccupiedSQL = `(CASE
 WHEN u.status NOT IN ('success','failed') THEN 1
 WHEN EXISTS(SELECT 1 FROM ejh_case_states s WHERE s.order_id=u.order_id AND s.state IN ('opened','waiting')) THEN 1
 WHEN EXISTS(SELECT 1 FROM ejh_case_states s WHERE s.order_id=u.order_id AND s.state IN ('returned','unopened')) THEN 0
 ELSE u.status<>'failed' AND NOT EXISTS(SELECT 1 FROM ejh_received_returns r WHERE r.order_id=u.order_id)
 END)`

func ejhCardOccupancy(q proCapacityQuery, key, exceptEmail string) (count int, email string, err error) {
	err = q.QueryRow(`SELECT count(*),coalesce(min(email),'') FROM (
  SELECT lower(email) email FROM pro_card_reservations WHERE card_key=?
  UNION SELECT lower(u.email) FROM pro_card_usage u WHERE u.card_key=? AND `+ejhOrderOccupiedSQL+`
 ) WHERE email<>? COLLATE NOCASE`, key, key, exceptEmail).Scan(&count, &email)
	return
}

func validateEJHOccupancy(q proCapacityQuery, key, email string) error {
	kind, e := proCardType(q, key)
	if e != nil {
		return e
	}
	if kind == "nexovcc" {
		return validateNexovccOccupancy(q, key, email)
	}
	enabled, err := ejhSingleActive(q, key)
	if err != nil || !enabled {
		return err
	}
	count, owner, err := ejhCardOccupancy(q, key, email)
	if err != nil {
		return err
	}
	if count > 0 {
		return fmt.Errorf("EJH 银行卡正在被 %s 占用（一卡同时一个号），须回款确认到账后才可分配其他账号", owner)
	}
	return nil
}

func ejhCardOccupants(q ejhCaseQuery, key string) ([]gptpay.CardOccupant, error) {
	rows, err := q.Query(`SELECT a.email,a.order_id,a.status,m.email IS NULL FROM (
 SELECT lower(email) email,'' order_id,'reserved' status FROM pro_card_reservations WHERE card_key=?
 UNION ALL SELECT lower(u.email),u.order_id,u.status FROM pro_card_usage u WHERE u.card_key=? AND `+ejhOrderOccupiedSQL+`
 ) a LEFT JOIN mail_accounts m ON m.email=a.email ORDER BY a.email,a.order_id`, key, key)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []gptpay.CardOccupant{}
	for rows.Next() {
		var item gptpay.CardOccupant
		if err = rows.Scan(&item.Email, &item.OrderID, &item.Status, &item.Deleted); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// Called in the same transaction as the successful removal state transition.
// Payment uncertainty is deliberately not released by a space status update.
func releaseProCardOrdersTx(tx *sql.Tx, email, at string) error {
	_, err := tx.Exec(`INSERT OR IGNORE INTO pro_card_order_releases(order_id,released_at)
 SELECT order_id,? FROM pro_card_usage WHERE email=? COLLATE NOCASE AND status='success'`, at, email)
	return err
}
