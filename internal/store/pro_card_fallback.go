package store

import (
	"chatgpt-space-merge/internal/model"
	"encoding/json"
	"fmt"
	"time"
)

func (s *Store) SwitchRestockCardForBalance(id, from string, route model.ProPurchaseRoute) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = lockProCardAdmission(tx); err != nil {
		return err
	}
	t, err := restockDispatchTask(tx, id)
	if err != nil {
		return err
	}
	if t.CardProvider != from || t.CardID != "" || t.OrderID != "" || t.BalanceFallbackFrom != "" {
		return fmt.Errorf("补货开卡进度已变化，不能切换卡台")
	}
	var raw string
	if err = tx.QueryRow(`SELECT profile FROM mail_accounts WHERE email=?`, t.Email).Scan(&raw); err != nil {
		return err
	}
	var p model.MailAccountProfile
	if err = json.Unmarshal([]byte(raw), &p); err != nil {
		return err
	}
	if p.IsProDead() || p.ProAuto.StopRequested || p.ProAuto.ID != "" {
		return fmt.Errorf("账号流程已开始或停止，不能切换卡台")
	}
	if t.PurchaseRoute != nil && (route.PlanCode != t.PurchaseRoute.PlanCode || route.PaymentProvider != t.PurchaseRoute.PaymentProvider || route.Country != t.PurchaseRoute.Country || route.CreditQuantity != t.PurchaseRoute.CreditQuantity) {
		return fmt.Errorf("备用卡台不能更换原套餐")
	}
	var unsafe bool
	if err = tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM gptpay_orders WHERE email=? AND json_extract(profile,'$.status')<>'failed') OR EXISTS(SELECT 1 FROM nexovcc_operations WHERE json_extract(profile,'$.restock_task_id')=? AND (status<>'failed' OR coalesce(json_extract(profile,'$.serial_no'),'')<>'' OR coalesce(json_extract(profile,'$.card_id'),'')<>''))`, t.Email, id).Scan(&unsafe); err != nil {
		return err
	}
	if unsafe {
		return fmt.Errorf("原开卡或付款尚未核实，不能切换卡台")
	}
	t.BalanceFallbackFrom, t.CardProvider, t.StrictCardProvider = from, route.CardProvider, true
	t.PurchaseRoute = &route
	t.UpdatedAt = time.Now()
	t.Message = "原卡台余额不足，已切换备用卡台 " + route.CardProvider
	_, err = tx.Exec(`UPDATE pro_restock_tasks SET payload=? WHERE id=?`, mustJSON(t), id)
	if err != nil {
		return err
	}
	return tx.Commit()
}
