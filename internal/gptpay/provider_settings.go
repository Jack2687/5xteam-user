package gptpay

import "fmt"

// SettingsInput is only used for writes. Responses must project Settings so a
// newly entered API key is never returned to the browser.
type SettingsInput struct {
	Settings
	APIKey string `json:"api_key"`
}

// A list is optional for older clients. A supplied list must be complete and
// unique; the selected provider is the only active configuration. Common card
// and session limits belong to the automation form, not to an inactive row.
func NormalizeProviderSettings(pay Settings, key string, rows []SettingsInput) (Settings, string, error) {
	if rows == nil {
		v, err := NormalizeSettings(pay)
		return v, key, err
	}
	if len(rows) != 3 {
		return pay, key, fmt.Errorf("请完整提交三家开通供应商配置")
	}
	selected, backup := Provider(pay.Provider), pay.BackupProvider
	if backup != "" && (backup == selected || (backup != Tokenseek && backup != CMSNav && backup != XUPay)) {
		return pay, key, fmt.Errorf("备用供应商须与主供应商不同，或不设置备用")
	}
	seen := map[string]bool{}
	normalized := make([]SettingsInput, len(rows))
	for i, row := range rows {
		if row.Provider != Tokenseek && row.Provider != CMSNav && row.Provider != XUPay || seen[row.Provider] {
			return pay, key, fmt.Errorf("供应商列表含无效或重复项目")
		}
		seen[row.Provider] = true
		row.BackupProvider = ""
		if row.Provider == selected {
			row.BackupProvider = backup
		}
		row.CardAccountLimit, row.SessionTimeoutMinutes = pay.CardAccountLimit, pay.SessionTimeoutMinutes
		v, err := NormalizeSettings(row.Settings)
		if err != nil {
			return pay, key, fmt.Errorf("%s：%w", row.Provider, err)
		}
		row.Settings = v
		normalized[i] = row
	}
	if !seen[selected] {
		return pay, key, fmt.Errorf("请选择一个启用的开通供应商")
	}
	copy(rows, normalized)
	for _, row := range rows {
		if row.Provider == selected {
			return row.Settings, row.APIKey, nil
		}
	}
	return pay, key, fmt.Errorf("请选择一个启用的开通供应商")
}
