package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"chatgpt-space-merge/internal/ejh"
)

// Only a matching issuer card snapshot confirms cancellation. Business error
// codes alone never establish acceptance or success; keep admission blocked.
func (s *Store) reconcileEJHCancellation(remote ejh.Card) error {
	if remote.Status != "8" && remote.Status != "2" && remote.Status != "6" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var sealed string
	id := EJHID("card", remote.OrderNo)
	err = tx.QueryRow(`SELECT r.sealed FROM ejh_operation_claims c JOIN ejh_records r ON r.kind='operation' AND r.id=c.operation_id WHERE c.scope=?`, "card:"+id).Scan(&sealed)
	if errors.Is(err, sql.ErrNoRows) {
		if remote.Status != "8" {
			return nil
		}
		// A cancellation made on the issuer's website also needs query-only
		// tracking. Never submit a second cancellation to adopt it locally.
		now := time.Now()
		op := ejh.Operation{ID: EJHID("external-cancel", remote.OrderNo), Method: "cancel", CardID: id, OrderNo: remote.OrderNo, Currency: remote.CardCurrency, Status: "processing", Note: "EJH 页面已提交销卡，后台仅查询处理结果", CreatedAt: now, UpdatedAt: now}
		if err = s.ejhPutTx(tx, "operation", op.ID, op); err != nil {
			return err
		}
		if _, err = tx.Exec(`INSERT INTO ejh_operation_claims VALUES(?,?)`, "card:"+id, op.ID); err != nil {
			return err
		}
		return tx.Commit()
	}
	if err != nil {
		return err
	}
	plain, err := s.decrypt(sealed)
	if err != nil {
		return err
	}
	var op ejh.Operation
	if err = json.Unmarshal([]byte(plain), &op); err != nil {
		return err
	}
	if op.Method != "cancel" || op.CardID != id || op.OrderNo != remote.OrderNo ||
		(op.Status != "submitting" && op.Status != "unknown" && op.Status != "processing") {
		return nil
	}
	op.Status, op.Error, op.CheckError, op.UpdatedAt = "processing", "", "", time.Now()
	op.Note = "卡台已确认销卡处理中，后台自动查询结果，无需重复提交"
	if remote.Status == "2" {
		op.Status, op.Note, op.NextCheckAt = "completed", "卡台已确认销卡成功", nil
	} else if remote.Status == "6" {
		op.Status, op.Error, op.Note, op.NextCheckAt = "failed", "卡台确认销卡失败，请核实后处理", "", nil
	}
	if err = s.ejhPutTx(tx, "operation", op.ID, op); err != nil {
		return err
	}
	if op.Status != "processing" {
		if _, err = tx.Exec(`DELETE FROM ejh_operation_claims WHERE operation_id=?`, op.ID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// The documented 800006 response explicitly rejects card modification. Older
// versions retained it as unknown; release only this specific failed increase.
func (s *Store) ReconcileEJHRejectedIncrease(cardID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var sealed string
	err = tx.QueryRow(`SELECT r.sealed FROM ejh_operation_claims c JOIN ejh_records r ON r.kind='operation' AND r.id=c.operation_id WHERE c.scope=?`, "card:"+cardID).Scan(&sealed)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	raw, err := s.decrypt(sealed)
	if err != nil {
		return err
	}
	var op ejh.Operation
	if err = json.Unmarshal([]byte(raw), &op); err != nil {
		return err
	}
	if op.CardID != cardID || op.Method != "increase" || op.Status != "unknown" || !strings.HasPrefix(op.Error, "EJH 800006:") {
		return nil
	}
	op.Status, op.Error, op.Note, op.UpdatedAt = "failed", "EJH 800006: 卡信息修改失败", "已按 EJH 明确失败响应解除提额操作占用", time.Now()
	if err = s.ejhPutTx(tx, "operation", op.ID, op); err != nil {
		return err
	}
	if _, err = tx.Exec(`DELETE FROM ejh_operation_claims WHERE operation_id=?`, op.ID); err != nil {
		return err
	}
	return tx.Commit()
}

// Read only outstanding claims, so completed operation history is not scanned
// on every poll. Unknown operations from earlier versions recover here too.
func (s *Store) EJHPendingCancellations(now time.Time, limit int) ([]ejh.Operation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.db.Query(`SELECT r.sealed FROM ejh_operation_claims c JOIN ejh_records r ON r.kind='operation' AND r.id=c.operation_id ORDER BY r.updated_at,r.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ejh.Operation
	for rows.Next() {
		var sealed string
		if err = rows.Scan(&sealed); err != nil {
			return nil, err
		}
		plain, err := s.decrypt(sealed)
		if err != nil {
			return nil, err
		}
		var op ejh.Operation
		if err = json.Unmarshal([]byte(plain), &op); err != nil {
			return nil, err
		}
		if op.Method != "cancel" || (op.Status != "submitting" && op.Status != "unknown" && op.Status != "processing") ||
			(op.NextCheckAt != nil && op.NextCheckAt.After(now)) {
			continue
		}
		out = append(out, op)
		if len(out) >= limit {
			break
		}
	}
	return out, rows.Err()
}
