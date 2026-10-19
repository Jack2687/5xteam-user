package httpapi

import (
	"chatgpt-space-merge/internal/model"
	"database/sql"
	"errors"
)

// The full automation continues after delivery (quota, merge, removal). A
// consumed account must not turn back into incoming supply just because it is
// no longer schedulable or has moved out of the monitored group.
func restockAccountExiting(p model.MailAccountProfile) bool {
	if p.PushProvider != "sub2" || p.Sub2AccountID <= 0 || (p.PushStatus != "completed" && p.PushStatus != "removed") {
		return false
	}
	if p.ProAuto.ID != "" {
		// Old push/merge flags cannot finish a new provisioning attempt.
		if p.ProAuto.Steps["push"] != "completed" {
			return false
		}
		switch p.ProAuto.Stage {
		case "login", "recharge", "oauth", "push":
			return false
		}
	}
	return p.ProWorkflowRunning || p.ProAuto.Stage == "merge" || p.SpaceMergedOnce ||
		p.ProAuto.Steps["merge"] == "completed" || p.RemoveStatus == "completed" || p.RemoveStatus == "team_removed"
}

// Only delivery tracking is finished. Payment uncertainty keeps its original
// order, budget and inflight occupancy even if progress was manually edited.
func (s *Server) restockExitSettled(p model.MailAccountProfile, orderID string) (bool, error) {
	if !restockAccountExiting(p) {
		return false, nil
	}
	if _, err := s.store.ActiveGPTPayOrder(p.Email); err == nil {
		return false, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	if orderID == "" {
		orderID = p.ProAuto.OrderID
	}
	linked := orderID != ""
	if orderID == "" && p.ProAuto.ID != "" {
		orderID = p.ProAuto.PaymentOrderID()
	}
	if orderID == "" {
		return true, nil // Historical/manual delivery without a payment link.
	}
	order, _, err := s.store.GPTPayOrder(orderID)
	if errors.Is(err, sql.ErrNoRows) {
		return !linked, nil
	}
	if err != nil {
		return false, err
	}
	return order.Status == "success", nil
}
