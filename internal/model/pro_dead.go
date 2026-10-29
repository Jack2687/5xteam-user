package model

import "time"

// Confirmed upstream deactivation is distinct from a completed, requested
// deletion. Keep the evidence and routing state after local account removal.
type ProDeadState struct {
	At     time.Time `json:"at"`
	Source string    `json:"source"`
	Reason string    `json:"reason"`
}

func (p MailAccountProfile) IsProDead() bool { return p.ProDead != nil }

func (p MailAccountProfile) CanMarkProDead() bool {
	return p.ManagementScope == "pro" && !p.DeletionSubmitted() && !p.MergeV2Resolved() && !p.MerchantAccepted() && !p.SpaceMergedOnce && p.TransferStatus != "completed"
}
