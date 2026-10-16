package model

import "time"

// nil preserves unknown historical policy; false explicitly means no deadline.
func (p *MailAccountProfile) CaptureProPushTimeout(c ProSettings) {
	if p.ProPushTimeoutEnabled != nil {
		return
	}
	enabled := c.ForceMergeEnabled
	p.ProPushTimeoutEnabled = &enabled
	if enabled {
		p.ProPushTimeoutMinutes = c.ForceMergeMinutes
	}
}
func (p MailAccountProfile) ProPushDeadline() *time.Time {
	if p.ProPushTimeoutEnabled == nil || !*p.ProPushTimeoutEnabled || p.ProPushTimeoutMinutes < 1 || p.ProPushTimeoutMinutes > 525600 || p.ProPushStartedAt == nil || p.ProPushStartedAt.IsZero() {
		return nil
	}
	at := p.ProPushStartedAt.Add(time.Duration(p.ProPushTimeoutMinutes) * time.Minute)
	return &at
}
