package httpapi

import (
	"chatgpt-space-merge/internal/model"
	"chatgpt-space-merge/internal/restock"
)

// Keep forecasts aligned with the policy actually used by this account's
// workflow. Existing full-auto jobs pin their threshold at start; the separate
// quota sweep uses current Pro settings when its automatic merge is enabled.
func applyRestockExitPolicy(a *restock.Account, p model.MailAccountProfile, settings model.ProSettings) {
	if p.DispositionStarted() || p.ProAuto.StopRequested || p.ProAuto.Status == "stopped" || (p.ProMigration != nil && p.ProMigration.Paused) || proTransferNeedsConfirmation(p) {
		return
	}
	if p.ProAuto.ID != "" && p.ProAuto.Status != "completed" && p.ProAuto.QuotaUsedThreshold > 0 && p.ProAuto.QuotaUsedThreshold <= 100 {
		t := p.ProAuto.QuotaUsedThreshold
		a.QuotaMergeThreshold = &t
	}
	if settings.QuotaEnabled && (settings.AutoMergeEnabled || settings.AutoMergeV2Enabled || settings.AutoDeleteEnabled || settings.AutoMerchantEnabled) && !p.SpaceMergedOnce && settings.QuotaUsedThreshold > 0 && settings.QuotaUsedThreshold <= 100 {
		if a.QuotaMergeThreshold == nil || settings.QuotaUsedThreshold < *a.QuotaMergeThreshold {
			t := settings.QuotaUsedThreshold
			a.QuotaMergeThreshold = &t
		}
	}
	a.ExitDeadline = p.ProPushDeadline()
	a.Leaving = p.ProWorkflowRunning || ((p.ProAuto.Stage == "merge" || p.ProAuto.Stage == "deactivate") && p.ProAuto.Status == "running")
}
