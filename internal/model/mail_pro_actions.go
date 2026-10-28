package model

import "time"

// Explicit opt-in for mailbox actions; it does not enroll the account in Pro
// provisioning, quota monitoring, replenishment or the Team rotation queue.
type MailProActions struct {
	EnabledAt time.Time `json:"enabled_at"`
	FinalMode string    `json:"final_mode,omitempty"`
}

func (p MailAccountProfile) ProActionsAllowed() bool {
	return p.ManagementScope == "pro" || (p.ManagementScope == "mail" && p.MailProActions != nil)
}

func (p MailAccountProfile) ProFinalStepAllowed(mode string) bool {
	return p.ManagementScope == "pro" || (p.ManagementScope == "mail" && p.MailProActions != nil && p.MailProActions.FinalMode == mode)
}

// An explicit mailbox inquiry may run after a stopped final step without
// clearing that final step's stop flag or resuming its automatic continuation.
func (p MailAccountProfile) OAIBlockedByAccountStop(mode string) bool {
	v := p.ProOAIConfirmations[mode]
	return p.ProAuto.StopRequested && !(p.ManagementScope == "mail" && p.MailProActions != nil && v != nil && v.Manual && v.RequestedAt != nil)
}
