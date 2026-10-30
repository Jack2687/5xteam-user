package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"chatgpt-space-merge/internal/gptpay"
	"chatgpt-space-merge/internal/model"
	"chatgpt-space-merge/internal/restock"
)

// Keep each account attempt on its own supplier even when another chain has
// already changed the global primary. Never persist API keys in account JSON.
func (s *Store) ProRechargePay(r *model.ProRechargeReplacement) (gptpay.Settings, string, error) {
	if r == nil || r.Payment == nil {
		return s.GPTPaySettings()
	}
	pay, key, err := s.GPTPayProviderSettings(r.Payment.Provider)
	if err == nil {
		pay, err = gptpay.NormalizeSettings(r.Payment.Apply(pay))
	}
	return pay, key, err
}

func readRechargeSettings(q proCapacityQuery, provider string) (pay gptpay.Settings, sealed string, err error) {
	var raw string
	err = q.QueryRow(`SELECT profile,encrypted_key FROM gptpay_settings WHERE id=1`).Scan(&raw, &sealed)
	if err != nil {
		return
	}
	if err = json.Unmarshal([]byte(raw), &pay); err != nil {
		return
	}
	if provider != "" && provider != gptpay.Provider(pay.Provider) {
		err = q.QueryRow(`SELECT profile,encrypted_key FROM gptpay_provider_settings WHERE provider=?`, provider).Scan(&raw, &sealed)
		if err != nil {
			return
		}
		pay = gptpay.Settings{}
		if err = json.Unmarshal([]byte(raw), &pay); err != nil {
			return
		}
	}
	pay, err = gptpay.NormalizeSettings(pay)
	return
}

// Called inside the account-claim transaction. Role exchange, chain state and
// successor reservation either all commit or all roll back. A concurrent chain
// which observed the same pair accepts the exchange instead of reversing it.
func (s *Store) switchRechargeProvider(tx *sql.Tx, r *model.ProRechargeReplacement) error {
	active, _, err := readRechargeSettings(tx, "")
	if err != nil {
		return err
	}
	already := active.Provider == r.BackupProvider && active.BackupProvider == r.PrimaryProvider
	if !already && (active.Provider != r.PrimaryProvider || active.BackupProvider != r.BackupProvider) {
		return errors.New("主备供应商配置已变化，暂停自动切换，请核对该任务")
	}
	backup, sealed, err := readRechargeSettings(tx, r.BackupProvider)
	if err != nil {
		return fmt.Errorf("无法读取备用供应商配置：%w", err)
	}
	key, err := s.decryptOptional(sealed)
	if err != nil || key == "" {
		return errors.New("备用供应商密钥不可用，等待配置后继续")
	}
	if err = gptpay.ValidatePurchase(backup); err != nil {
		return err
	}
	// Retain the purchased product; use the backup's country and own API key.
	intent := backup
	intent.PlanCode, intent.CreditQuantity = r.Payment.PlanCode, r.Payment.CreditQuantity
	intent, err = gptpay.NormalizeSettings(intent)
	if err != nil {
		return err
	}
	if !already {
		backup.BackupProvider = r.PrimaryProvider
		backup.CardAccountLimit, backup.SessionTimeoutMinutes = active.CardAccountLimit, active.SessionTimeoutMinutes
		if err = s.saveGPTPaySettingsTx(tx, &backup, ""); err != nil {
			return err
		}
		active.BackupProvider = ""
		if err = s.saveGPTPayProviderSettingsTx(tx, &active, ""); err != nil {
			return err
		}
	}
	r.Payment = model.RechargePayment(intent)
	if r.PurchaseRoute != nil {
		route := *r.PurchaseRoute
		route.PaymentProvider, route.Country, route.CreditQuantity = intent.Provider, intent.Country, intent.CreditQuantity
		r.PurchaseRoute = &route
	}
	r.ProviderSwitched = true
	return nil
}

// Resolve the current schedule/templates against a successor's pinned payment
// intent, so a global role exchange is not mistaken for a product change.
func readRestockReplacementPurchase(q proCapacityQuery, t restock.Task, c restock.Config, now time.Time) (p model.ProSettings, pay gptpay.Settings, pinned bool, err error) {
	var raw string
	err = q.QueryRow(`SELECT profile FROM mail_accounts WHERE email=?`, t.Email).Scan(&raw)
	if err != nil {
		return
	}
	var account model.MailAccountProfile
	if err = json.Unmarshal([]byte(raw), &account); err != nil {
		return
	}
	r := account.ProAuto.Replacement
	if r == nil || r.Payment == nil || r.PreviousEmail == "" || account.ProRestockTaskID != t.ID {
		return
	}
	pinned = true
	if account.ProAuto.StopRequested {
		err = errors.New("换号任务已停止")
		return
	}
	p = model.DefaultProSettings()
	if err = q.QueryRow(`SELECT profile FROM pro_settings WHERE id=1`).Scan(&raw); err != nil {
		return
	}
	if err = json.Unmarshal([]byte(raw), &p); err != nil {
		return
	}
	pay, _, err = readRechargeSettings(q, r.Payment.Provider)
	if err != nil {
		return
	}
	pay = r.Payment.Apply(pay)
	p, pay, err = ResolveProPurchase(p, pay, c, now)
	if err == nil && (t.Plan != fmt.Sprintf("%s:%s:%s:%d", pay.Provider, pay.PlanCode, pay.Country, pay.CreditQuantity) || !SameProPurchase(t.PurchaseRoute, p.PurchaseRoute)) {
		err = errors.New("换号任务的分时套餐或模板已变化，暂停派发")
	}
	return
}

func (s *Store) ProRestockReplacementPurchase(t restock.Task, c restock.Config, now time.Time) (model.ProSettings, gptpay.Settings, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return readRestockReplacementPurchase(s.db, t, c, now)
}
