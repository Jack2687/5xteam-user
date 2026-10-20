package restock

import (
	"fmt"
	"time"
)

type DemandWindow struct {
	Key           string `json:"key"`
	Start         string `json:"start"`
	End           string `json:"end"`
	Threshold     int    `json:"threshold"`
	Confirmations int    `json:"confirmations"`
	Consecutive   int    `json:"consecutive"`
}

// DemandGate is an observation, never a user-supplied authorization.
type DemandGate struct {
	Version          int            `json:"version"`
	Scope            string         `json:"scope"`
	Active           bool           `json:"active"`
	Allowed          bool           `json:"allowed"`
	Valid            bool           `json:"valid"`
	Phase            string         `json:"phase"`
	Reason           string         `json:"reason"`
	MonitorUsed      int            `json:"monitor_used"`
	FallbackUsed     int            `json:"fallback_used"`
	TotalUsed        int            `json:"total_used"`
	FallbackAccounts int            `json:"fallback_accounts"`
	CheckedAt        time.Time      `json:"checked_at"`
	MainAt           time.Time      `json:"main_at"`
	FallbackAt       time.Time      `json:"fallback_at"`
	ValidUntil       time.Time      `json:"valid_until"`
	Windows          []DemandWindow `json:"windows"`
}

func (c Config) demandWindows(now time.Time) (rows []DemandWindow, strict bool) {
	if !c.NoRestockEnabled || c.validateNoRestockWindows() != nil {
		return nil, false
	}
	local := now.In(scheduleLocation)
	minute := local.Hour()*60 + local.Minute()
	for i, w := range c.dailyWindows() {
		if w.AllowedProvider != "" || !windowContains(w, minute) {
			continue
		}
		if !w.HighDemandEnabled {
			strict = true
			continue
		}
		day := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, scheduleLocation)
		start, _ := dailyMinute(w.Start)
		end, _ := dailyMinute(w.End)
		if start > end && minute < end {
			day = day.AddDate(0, 0, -1)
		}
		rows = append(rows, DemandWindow{Key: fmt.Sprintf("%s/%d/%s/%s", day.Format("2006-01-02"), i, w.Start, w.End), Start: w.Start, End: w.End, Threshold: w.HighDemandThreshold, Confirmations: w.HighDemandConfirmations})
	}
	return
}

func (c Config) HasDemandWindows() bool {
	if !c.NoRestockEnabled {
		return false
	}
	for _, w := range c.dailyWindows() {
		if w.HighDemandEnabled {
			return true
		}
	}
	return false
}

func (c Config) DemandActive(now time.Time) bool {
	rows, _ := c.demandWindows(now)
	return len(rows) > 0
}

func sameDemandWindows(a, b []DemandWindow) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Key != b[i].Key || a[i].Threshold != b[i].Threshold || a[i].Confirmations != b[i].Confirmations {
			return false
		}
	}
	return true
}

func (c Config) DemandStatus(now time.Time) DemandGate {
	rows, strict := c.demandWindows(now)
	v := DemandGate{Version: c.Version, Active: len(rows) > 0, Phase: "inactive", Windows: rows}
	if !v.Active {
		return v
	}
	v.Phase, v.Reason = "unknown", "禁补中：等待合计并发检测"
	old := c.DemandGate
	if old != nil && old.Version == c.Version && sameDemandWindows(rows, old.Windows) {
		v = *old
		v.Windows = append([]DemandWindow(nil), old.Windows...)
		if !v.Valid || !now.Before(v.ValidUntil) || v.CheckedAt.After(now.Add(5*time.Second)) {
			v.Valid, v.Allowed = false, false
			v.Phase = "unknown"
			if v.Reason == "" || old.Valid {
				v.Reason = "禁补中：并发数据已过期，等待重新检测"
			}
			for i := range v.Windows {
				v.Windows[i].Consecutive = 0
			}
		}
	}
	if strict {
		v.Allowed = false
		v.Phase = "blocked"
		v.Reason = "重叠时段要求完全禁补，暂停新增"
	}
	return v
}

// AdvanceDemand counts only independently fresh samples and does not reset on
// batch activity or traffic moving between primary and fallback accounts.
func (c Config) AdvanceDemand(sample DemandGate, now time.Time) DemandGate {
	rows, strict := c.demandWindows(now)
	sample.Version, sample.Active, sample.CheckedAt, sample.Windows = c.Version, len(rows) > 0, now, rows
	sample.Allowed = false
	if !sample.Active {
		sample.Phase = "inactive"
		return sample
	}
	if !sample.Valid || sample.Scope == "" || !now.Before(sample.ValidUntil) {
		sample.Valid = false
		sample.Phase = "unknown"
		if sample.Reason == "" {
			sample.Reason = "禁补中：并发采集不完整，等待重试"
		}
		return sample
	}
	previous := c.DemandStatus(now)
	continuous := previous.Active && previous.Valid && previous.Scope == sample.Scope && sameDemandWindows(rows, previous.Windows)
	fresh := !continuous || (sample.MainAt.After(previous.MainAt) && (sample.FallbackAt.IsZero() || sample.FallbackAt.After(previous.FallbackAt)))
	sample.Allowed = true
	for i := range sample.Windows {
		w := &sample.Windows[i]
		if sample.TotalUsed >= w.Threshold {
			if continuous {
				w.Consecutive = previous.Windows[i].Consecutive
			}
			if fresh {
				w.Consecutive = min(w.Confirmations, w.Consecutive+1)
			}
		}
		if w.Consecutive < w.Confirmations {
			sample.Allowed = false
		}
	}
	sample.Phase, sample.Reason = "waiting", "禁补中：等待合计并发连续达标"
	if sample.Allowed {
		sample.Phase = "allowed"
		sample.Reason = "禁补期间合计并发连续达标，按正常配置补货"
	}
	if strict {
		sample.Allowed = false
		sample.Phase = "blocked"
		sample.Reason = "重叠时段要求完全禁补，暂停新增"
	}
	return sample
}
