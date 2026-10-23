package model

import (
	"errors"
	"time"
)

// Deactivation is independent of the four Team membership steps. A queued
// response confirms acceptance only, never deletion or a financial refund.
type ProDeactivation struct {
	Status             string                      `json:"status"`
	Stage              string                      `json:"stage"`
	Steps              map[string]string           `json:"steps"`
	Manual             map[string]ProStageOverride `json:"manual,omitempty"`
	ConfirmationSource string                      `json:"confirmation_source,omitempty"`
	Error              string                      `json:"error,omitempty"`
	Message            string                      `json:"message,omitempty"`
	StartedAt          time.Time                   `json:"started_at"`
	UpdatedAt          time.Time                   `json:"updated_at"`
	SubmittedAt        *time.Time                  `json:"submitted_at,omitempty"`
	AcceptedAt         *time.Time                  `json:"accepted_at,omitempty"`
	ConfirmedAt        *time.Time                  `json:"confirmed_at,omitempty"`
	CheckedAt          *time.Time                  `json:"checked_at,omitempty"`
	NextCheckAt        *time.Time                  `json:"next_check_at,omitempty"`
	AccountID          string                      `json:"account_id,omitempty"`
	UserID             string                      `json:"user_id,omitempty"`
}

func (s ProSettings) ValidateDisposition() error {
	if s.DeletionCheckIntervalSeconds < 0 || s.DeletionCheckIntervalSeconds > 86400 {
		return errors.New("删除结果核验间隔须为 1～86400 秒")
	}
	if s.AutoDeleteEnabled && s.AutoMergeEnabled {
		return errors.New("自动删除账户与自动合并只能开启一个")
	}
	return nil
}

func (s ProSettings) DeletionCheckInterval() time.Duration {
	seconds := s.DeletionCheckIntervalSeconds
	if seconds < 1 || seconds > 86400 {
		seconds = 300
	}
	return time.Duration(seconds) * time.Second
}

func (p MailAccountProfile) DeletionNeedsContinuation() bool {
	// Confirmation is terminal for background work. Failed downstream cleanup
	// remains visible and may only be resumed by an explicit operator action.
	return p.DeletionSubmitted() && !p.DeletionConfirmed()
}
func (s ProSettings) Disposition() string {
	if s.AutoDeleteEnabled {
		return "deactivate"
	}
	return "merge"
}
func (s ProSettings) DispositionLabel() string {
	if s.AutoDeleteEnabled {
		return "删除账户"
	}
	return "空间合并"
}
func (p MailAccountProfile) DeletionSubmitted() bool {
	return p.ProDeactivation != nil && p.ProDeactivation.SubmittedAt != nil
}
func (p MailAccountProfile) DeletionConfirmed() bool {
	return p.ProDeactivation != nil && p.ProDeactivation.ConfirmedAt != nil
}
func (p MailAccountProfile) DeletionAccepted() bool {
	return p.ProDeactivation != nil && p.ProDeactivation.AcceptedAt != nil
}

// Late worker results may still record upstream facts, but cannot revive a
// stopped account or its timers. Only an explicit resume clears StopRequested.
func (p *MailAccountProfile) ReconcileProStop() bool {
	if !p.ProAuto.StopRequested {
		return false
	}
	changed := p.ProAuto.Status != "stopped" || p.ProAuto.NextCheckAt != nil || p.ProAuto.RetryAt != nil || p.ProForceMergeNextAt != nil
	p.ProAuto.Status = "stopped"
	p.ProAuto.NextCheckAt, p.ProAuto.RetryAt, p.ProForceMergeNextAt = nil, nil, nil
	if d := p.ProDeactivation; d != nil && d.NextCheckAt != nil {
		d.NextCheckAt = nil
		changed = true
	}
	if d := p.ProDailyMerge; d != nil && d.NextAt != nil {
		d.NextAt = nil
		changed = true
	}
	return changed
}

func (p *MailAccountProfile) ReconcileProAutoDeletion(now time.Time) bool {
	d := p.ProDeactivation
	if d == nil || d.SubmittedAt == nil {
		return false
	}
	changed := false
	if d.ConfirmedAt != nil && d.NextCheckAt != nil {
		d.NextCheckAt = nil
		changed = true
	}
	if p.ProAuto.ID == "" {
		return changed
	}
	status, step := "awaiting_deletion", "waiting"
	autoError := d.Error
	if d.ConfirmedAt != nil {
		status, step = "completed", "completed"
		autoError = ""
	} else if d.Error != "" {
		step = "failed"
	}
	if p.ProAuto.StopRequested {
		status = "stopped"
	}
	changed = changed || p.ProAuto.Stage != "deactivate" || p.ProAuto.Status != status || p.ProAuto.NextCheckAt != nil || p.ProAuto.Error != autoError || p.ProAuto.Steps["deactivate"] != step
	if p.ProAuto.Steps == nil {
		p.ProAuto.Steps = map[string]string{}
	}
	p.ProAuto.Stage, p.ProAuto.Status, p.ProAuto.Error = "deactivate", status, autoError
	p.ProAuto.Steps["deactivate"] = step
	p.ProAuto.NextCheckAt = nil
	if changed {
		p.ProAuto.UpdatedAt = now
	}
	return changed
}
