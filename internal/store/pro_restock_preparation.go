package store

import (
	"chatgpt-space-merge/internal/model"
	"chatgpt-space-merge/internal/restock"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

// Caller excludes live preparation and account workers. Recheck the durable
// intent and all possible issuance/payment evidence under the admission lock.
func (s *Store) FinishUnsubmittedRestockPreparation(expected *restock.Task) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	if err = lockProCardAdmission(tx); err != nil {
		return false, err
	}
	current, err := restockDispatchTask(tx, expected.ID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if current.UpdatedAt != expected.UpdatedAt && !current.UpdatedAt.Equal(expected.UpdatedAt) {
		return false, nil
	}
	if current.CardProvider != "nexovcc" || current.OrderID != "" || current.CardID != "" {
		return false, nil
	}
	var raw string
	if err = tx.QueryRow(`SELECT profile FROM mail_accounts WHERE email=?`, current.Email).Scan(&raw); err != nil {
		return false, err
	}
	var p model.MailAccountProfile
	if err = json.Unmarshal([]byte(raw), &p); err != nil {
		return false, err
	}
	if p.ProAuto.ID != "" || p.ProRestockTaskID != current.ID || p.PushStatus == "completed" || p.DispositionStarted() || p.SpaceMergedOnce {
		return false, nil
	}
	var unsafe bool
	err = tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM gptpay_orders WHERE email=? AND (json_extract(profile,'$.status')<>'failed' OR json_extract(profile,'$.succeeded_at') IS NOT NULL)) OR EXISTS(SELECT 1 FROM nexovcc_operations WHERE json_extract(profile,'$.restock_task_id')=? AND (status<>'failed' OR coalesce(json_extract(profile,'$.serial_no'),'')<>'' OR coalesce(json_extract(profile,'$.card_id'),'')<>''))`, current.Email, current.ID).Scan(&unsafe)
	if err != nil || unsafe {
		return false, err
	}
	current.Status, current.Stage, current.ETA, current.Cost = "failed", "card", nil, 0
	current.UpdatedAt = time.Now()
	current.Message = "开卡准备失败，已核实无开卡或付款提交，释放本次在途：" + current.Message
	if _, err = tx.Exec(`UPDATE pro_restock_tasks SET status=?,cost=?,payload=? WHERE id=?`, current.Status, current.Cost, mustJSON(current), current.ID); err != nil {
		return false, err
	}
	if err = tx.Commit(); err != nil {
		return false, err
	}
	*expected = current
	return true, nil
}
