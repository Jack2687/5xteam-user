package store

import (
	"encoding/json"
	"errors"
	"strings"

	"chatgpt-space-merge/internal/model"
)

// Delete only local Pro credentials and their Team projection. Keep orders,
// card usage, audit records and workspace history independently of the account.
func (s *Store) DeleteProAccount(email string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	email = strings.ToLower(strings.TrimSpace(email))
	if err := s.checkProAccountDeletionLocked(email); err != nil {
		return err
	}
	var raw string
	if err := s.db.QueryRow("SELECT profile FROM mail_accounts WHERE email=?", email).Scan(&raw); err != nil {
		return err
	}
	var p model.MailAccountProfile
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		return err
	}
	if p.VM2 != nil && p.VM2.DeletedAt == nil {
		return errors.New("请先清理 vm2api 槽位及运行环境")
	}
	return s.deleteMailAccountLocked(email, true)
}

func (s *Store) CheckProAccountDeletion(email string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.checkProAccountDeletionLocked(strings.ToLower(strings.TrimSpace(email)))
}

func (s *Store) checkProAccountDeletionLocked(email string) error {
	var raw string
	if err := s.db.QueryRow("SELECT profile FROM mail_accounts WHERE email=?", email).Scan(&raw); err != nil {
		return errors.New("Pro 账号不存在")
	}
	var p model.MailAccountProfile
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		return err
	}
	if normalizeMailManagementScope(p.ManagementScope) != "pro" {
		return errors.New("账号不在 Pro 管理中，未删除")
	}
	if err := s.checkActiveRestockDeletionLocked(email); err != nil {
		return err
	}
	if p.ProWorkflowRunning || p.ProAuto.Status == "running" {
		return errors.New("账号正在执行全自动或空间合并，请完成或停止后再删除")
	}
	for _, stage := range p.ProManualStages {
		if stage.Status == "running" {
			return errors.New("账号正在执行操作，请稍后再删除")
		}
	}
	var pending int
	if err := s.db.QueryRow(`SELECT (SELECT count(*) FROM gptpay_orders WHERE email=? AND active=1) + (SELECT count(*) FROM pro_card_usage WHERE email=? AND status NOT IN ('success','failed'))`, email, email).Scan(&pending); err != nil {
		return err
	}
	if pending > 0 {
		return errors.New("账号有未结束的开通订单，请确认开通结果后再删除")
	}
	return nil
}

func (s *Store) checkActiveRestockDeletionLocked(email string) error {
	var active bool
	if err := s.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM pro_restock_tasks WHERE email=? AND status NOT IN ('ready','failed','cancelled'))`, email).Scan(&active); err != nil {
		return err
	}
	if active {
		return errors.New("账号存在未结束的自动补货任务，请先暂停并等待未提交预留撤销，或核验已提交任务")
	}
	return nil
}
