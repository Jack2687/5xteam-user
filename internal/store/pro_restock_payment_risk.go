package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"chatgpt-space-merge/internal/gptpay"
	"chatgpt-space-merge/internal/restock"
)

func pendingPaymentRisk(q proCapacityQuery, snapshot restock.Snapshot) (string, error) {
	var id string
	err := q.QueryRow(`SELECT r.task_id FROM pro_restock_payment_risk r JOIN pro_restock_tasks t ON t.id=r.task_id WHERE r.replacement_id IS NULL AND r.paused=0 AND json_extract(t.payload,'$.binding')=? AND json_extract(t.payload,'$.group_id')=? ORDER BY t.created_at,t.id LIMIT 1`, snapshot.Binding, snapshot.GroupID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return id, err
}

// Atomically release only a confirmed failed order and record its one allowed
// replacement. A replacement's risk rejection disables new admission before
// any other dispatcher can claim work. Original account/order data is retained.
func (s *Store) FinishProRestockPaymentRisk(t restock.Task) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var raw string
	if err = tx.QueryRow(`SELECT profile FROM gptpay_orders WHERE id=? AND email=?`, t.OrderID, t.Email).Scan(&raw); err != nil {
		return false, err
	}
	var order gptpay.Order
	if err = json.Unmarshal([]byte(raw), &order); err != nil {
		return false, err
	}
	if !order.PaymentReviewRequired() {
		return false, errors.New("付款结果尚未明确，保留原任务待核实")
	}
	err = tx.QueryRow(`SELECT payload FROM pro_restock_tasks WHERE id=?`, t.ID).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) && t.ManagedExternally {
		t.Status = "failed"
		if _, err = tx.Exec(`INSERT INTO pro_restock_tasks VALUES(?,?,?,?,?,?)`, t.ID, t.Email, t.Status, formatTime(t.CreatedAt), t.Cost, mustJSON(t)); err != nil {
			return false, err
		}
		raw = mustJSON(t)
	} else if err != nil {
		return false, err
	}
	var current restock.Task
	if err = json.Unmarshal([]byte(raw), &current); err != nil {
		return false, err
	}
	if current.StoppedAt != nil || current.Status == "cancelled" || current.Status == "ready" {
		return false, nil
	}
	var source string
	err = tx.QueryRow(`SELECT task_id FROM pro_restock_payment_risk WHERE replacement_id=?`, t.ID).Scan(&source)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	paused := source != ""
	if !paused && order.PaymentReviewBlocksRestock() {
		return false, errors.New("付款结果尚未明确，保留原任务待核实")
	}
	if paused {
		var handled bool
		if err = tx.QueryRow(`SELECT paused FROM pro_restock_payment_risk WHERE task_id=?`, source).Scan(&handled); err != nil {
			return false, err
		}
		if !handled {
			c, err := readRestockConfig(tx)
			if err != nil {
				return false, err
			}
			if c.Enabled {
				c.Enabled, c.Version = false, c.Version+1
				if _, err = tx.Exec(`INSERT INTO pro_restock_config VALUES(1,?) ON CONFLICT(id) DO UPDATE SET payload=excluded.payload`, mustJSON(c)); err != nil {
					return false, err
				}
			}
			if _, err = tx.Exec(`UPDATE pro_restock_payment_risk SET paused=1 WHERE task_id=?`, source); err != nil {
				return false, err
			}
		}
		t.Message = "替补账号再次支付风控，自动补货已暂停；两个账号和原订单保留，请人工核实"
	} else {
		if _, err = tx.Exec(`INSERT OR IGNORE INTO pro_restock_payment_risk(task_id) VALUES(?)`, t.ID); err != nil {
			return false, err
		}
		t.Message = gptpay.PaymentReviewRestockMessage
	}
	t.Status, t.ETA, t.UpdatedAt = "failed", nil, time.Now()
	if order.PaymentReviewBlocksRestock() {
		t.Status = "attention"
	}
	if _, err = tx.Exec(`UPDATE pro_restock_tasks SET status=?,payload=? WHERE id=?`, t.Status, mustJSON(t), t.ID); err != nil {
		return false, err
	}
	return paused, tx.Commit()
}
