package model

import "time"

// Bound to the subscription and automation captured in one durable daily snapshot.
type ProDailyMergeRequest struct {
	At           time.Time  `json:"at"`
	OrderID      string     `json:"order_id"`
	AutomationID string     `json:"automation_id"`
	Attempts     int        `json:"attempts"`
	NextAt       *time.Time `json:"next_at,omitempty"`
	Completed    bool       `json:"completed"`
}

func (p MailAccountProfile) DailyMergeMatches() bool {
	r := p.ProDailyMerge
	return r != nil && !r.Completed && r.OrderID != "" && r.AutomationID == p.ProAuto.ID && (p.ProAuto.ID == "" || p.ProAuto.OrderID == r.OrderID)
}
