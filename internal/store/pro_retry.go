package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"chatgpt-space-merge/internal/model"
	"chatgpt-space-merge/internal/restock"
)

// An explicit retry becomes a manual flow. Keep the failed restock intent and
// its budget history, but let the new flow participate as external in-flight
// work instead of leaving it hidden behind a finished task's account claim.
// The caller holds both the account lock and the restock sweep lock.
func (s *Store) DetachFailedProRestock(email, flowID, orderID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var raw string
	if err = tx.QueryRow(`SELECT profile FROM mail_accounts WHERE email=?`, email).Scan(&raw); err != nil {
		return err
	}
	var p model.MailAccountProfile
	if err = json.Unmarshal([]byte(raw), &p); err != nil {
		return err
	}
	if p.ProAuto.ID != flowID || p.ProAuto.OrderID != orderID || p.ProAuto.Steps["recharge"] == "completed" || p.ProAuto.Steps["oauth"] == "completed" || p.PushStatus == "completed" || p.SpaceMergedOnce {
		return errors.New("账号进度已变化，不能重新开通")
	}
	if p.ProRestockTaskID == "" {
		return nil
	}
	var unresolved int
	if err = tx.QueryRow(`SELECT count(*) FROM gptpay_orders WHERE email=? AND json_extract(profile,'$.status')!='failed'`, email).Scan(&unresolved); err != nil {
		return err
	}
	if unresolved > 0 {
		return errors.New("账号仍有已成功或待确认的订单，不能重新开通")
	}
	if orderID != "" {
		var status string
		if err = tx.QueryRow(`SELECT json_extract(profile,'$.status') FROM gptpay_orders WHERE id=? AND email=?`, orderID, email).Scan(&status); err != nil || status != "failed" {
			return errors.New("原订单未确认失败，不能重新开通")
		}
	}
	var task restock.Task
	if err = tx.QueryRow(`SELECT payload FROM pro_restock_tasks WHERE id=? AND email=?`, p.ProRestockTaskID, email).Scan(&raw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("原补货任务不存在，请核对记录")
		}
		return err
	}
	if err = json.Unmarshal([]byte(raw), &task); err != nil {
		return err
	}
	if task.Status == "ready" {
		return errors.New("原补货任务已就绪，不能重新开通")
	}
	if p.ProAuto.ID == "" && task.CardProvider == "nexovcc" {
		if task.Status != "attention" && task.Status != "failed" && task.StoppedAt == nil {
			return errors.New("补货任务仍在排队或启动中，请等待当前任务")
		}
		// Issuance precedes the workflow ID. No payment does not prove that
		// issuing another card is safe: first resolve every unfinished open.
		var pendingOpen bool
		if err = tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM nexovcc_operations WHERE scope='open' AND status NOT IN ('succeeded','failed'))`).Scan(&pendingOpen); err != nil {
			return err
		}
		if pendingOpen {
			return errors.New("环球开卡结果尚未核实，请先在环球操作记录查询或同步原卡，再重试当前账号")
		}
	}
	if task.StoppedAt == nil {
		task.Status, task.OrderID = "failed", orderID
		task.ETA = nil
		task.Message = "开通未成功，已由用户从登录开始重新执行；原记录保留"
		task.UpdatedAt = time.Now()
		if orderID == "" {
			task.Cost = 0
		}
		if _, err = tx.Exec(`UPDATE pro_restock_tasks SET status=?,cost=?,payload=? WHERE id=?`, task.Status, task.Cost, mustJSON(task), task.ID); err != nil {
			return err
		}
	}
	// Issuance may fail before ProAuto exists. Preserve the task's group snapshot
	// before detaching, including when the next card preparation also fails.
	if len(p.ProAuto.Sub2PushGroupIDs) == 0 && task.PurchaseRoute != nil {
		p.ProAuto.Sub2PushGroupIDs = append([]int64(nil), task.PurchaseRoute.Sub2GroupIDs...)
	}
	// Retain the original downstream target; only release ownership of the old intent.
	if _, err = tx.Exec(`UPDATE mail_accounts SET profile=json_set(json_remove(profile,'$.pro_restock_task_id'),'$.pro_auto.sub2_push_group_ids',json(?)) WHERE email=?`, mustJSON(p.ProAuto.Sub2PushGroupIDs), email); err != nil {
		return err
	}
	return tx.Commit()
}
