package httpapi

import (
	"math"

	"chatgpt-space-merge/internal/model"
)

func validProQuotaWindow(w *model.FreeQuotaWindow) bool {
	return w != nil && !math.IsNaN(w.UsedPercent) && !math.IsInf(w.UsedPercent, 0) && w.UsedPercent >= 0 && w.UsedPercent <= 100
}

func sameProQuotaBinding(a, b model.MailAccountProfile) bool {
	if a.PushProvider != b.PushProvider || b.PushStatus != "completed" || a.Sub2AccountID != b.Sub2AccountID || a.CPAAuthFileName != b.CPAAuthFileName || a.ProRestockTargetID != b.ProRestockTargetID {
		return false
	}
	if a.VM2 == nil || b.VM2 == nil {
		return a.VM2 == nil && b.VM2 == nil
	}
	return a.VM2.ConnectionID == b.VM2.ConnectionID && a.VM2.SlotID == b.VM2.SlotID && b.VM2.DeletedAt == nil
}
