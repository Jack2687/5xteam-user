package model

import "time"

// A separate durable workflow: acceptance is not proof of a refund.
type ProMergeV2 struct {
	InviteAcceptAttemptedAt *time.Time                  `json:"invite_accept_attempted_at,omitempty"`
	InviteAcceptedAt        *time.Time                  `json:"invite_accepted_at,omitempty"`
	Status                  string                      `json:"status"`
	Stage                   string                      `json:"stage"`
	Steps                   map[string]string           `json:"steps"`
	Manual                  map[string]ProStageOverride `json:"manual,omitempty"`
	AdminID                 string                      `json:"admin_id"`
	TeamID                  string                      `json:"team_id"`
	SeatType                string                      `json:"seat_type"`
	UserID                  string                      `json:"user_id,omitempty"`
	Error                   string                      `json:"error,omitempty"`
	Message                 string                      `json:"message,omitempty"`
	StartedAt               time.Time                   `json:"started_at"`
	UpdatedAt               time.Time                   `json:"updated_at"`
	SubmittedAt             *time.Time                  `json:"submitted_at,omitempty"`
	AcceptedAt              *time.Time                  `json:"accepted_at,omitempty"`
	CompletedAt             *time.Time                  `json:"completed_at,omitempty"`
	AutoResumeAt            *time.Time                  `json:"auto_resume_at,omitempty"`
}

func (p MailAccountProfile) MergeV2Started() bool { return p.ProMergeV2 != nil }
func (p MailAccountProfile) MergeV2Accepted() bool {
	return p.ProMergeV2 != nil && p.ProMergeV2.AcceptedAt != nil
}
func (p MailAccountProfile) MergeV2Completed() bool {
	return p.ProMergeV2 != nil && p.ProMergeV2.CompletedAt != nil
}
func (p MailAccountProfile) MergeV2Removed() bool {
	return p.MergeV2Accepted() && p.ProMergeV2.Steps["remove"] == "completed"
}

func (p *MailAccountProfile) ReconcileProMergeV2(now time.Time) bool {
	m := p.ProMergeV2
	if m == nil || p.ProAuto.ID == "" {
		return false
	}
	status, step := "interrupted", m.Status
	if m.Status == "running" && p.ProWorkflowRunning {
		status = "running"
	}
	if m.Error != "" {
		status, step = "failed", "failed"
	}
	if m.SubmittedAt != nil && m.AcceptedAt == nil {
		status, step = "interrupted", "unknown"
	}
	if m.CompletedAt != nil {
		status, step = "completed", "completed"
	}
	if p.ProAuto.StopRequested {
		status = "stopped"
	}
	changed := p.ProAuto.Status != status || p.ProAuto.Stage != "merge_v2" || p.ProAuto.Error != m.Error || p.ProAuto.NextCheckAt != nil || p.ProAuto.RetryAt != nil || p.ProAuto.Steps["merge_v2"] != step
	if p.ProAuto.Steps == nil {
		p.ProAuto.Steps = map[string]string{}
	}
	p.ProAuto.Status, p.ProAuto.Stage, p.ProAuto.Error = status, "merge_v2", m.Error
	p.ProAuto.Steps["merge_v2"] = step
	p.ProAuto.NextCheckAt, p.ProAuto.RetryAt, p.ProForceMergeNextAt = nil, nil, nil
	if changed {
		p.ProAuto.UpdatedAt = now
	}
	return changed
}
