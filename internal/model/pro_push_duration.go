package model

import "time"

// A display clock only: this does not change quota, timeout or retry policies.
func (p MailAccountProfile) ProPushEndTime() *time.Time {
	if p.ProPushEndedAt != nil && !p.ProPushEndedAt.IsZero() {
		return p.ProPushEndedAt
	}
	var end *time.Time
	add := func(at time.Time) {
		if at.IsZero() || (p.ProPushStartedAt != nil && at.Before(*p.ProPushStartedAt)) {
			return
		}
		if end == nil || at.Before(*end) {
			value := at
			end = &value
		}
	}
	for _, stage := range []string{"merge", "deactivate"} {
		add(p.ProManualStages[stage].StartedAt)
		for _, state := range []string{"running", "completed", "failed"} {
			add(p.ProAuto.StageTimes[stage+":"+state])
		}
	}
	if p.ProDeactivation != nil {
		add(p.ProDeactivation.StartedAt)
		if p.ProDeactivation.AcceptedAt != nil {
			add(*p.ProDeactivation.AcceptedAt)
		}
		if p.ProDeactivation.ConfirmedAt != nil {
			add(*p.ProDeactivation.ConfirmedAt)
		}
	}
	if p.SpaceMergedAt != nil {
		add(*p.SpaceMergedAt)
	}
	return end
}

func (p *MailAccountProfile) FreezeProPushDuration(now time.Time) {
	if p.ProPushStartedAt == nil || p.ProPushStartedAt.IsZero() {
		return
	}
	if end := p.ProPushEndTime(); end != nil {
		p.ProPushEndedAt = end
		return
	}
	if p.ProWorkflowRunning || p.SpaceMergedOnce || p.DeletionSubmitted() ||
		(p.ProAuto.Status == "running" && (p.ProAuto.Stage == "merge" || p.ProAuto.Stage == "deactivate")) {
		p.ProPushEndedAt = &now
	}
}
