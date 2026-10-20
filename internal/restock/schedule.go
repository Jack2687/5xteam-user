package restock

import (
	"fmt"
	"time"
)

// Use Beijing business time independently of the host/container time zone.
var scheduleLocation = time.FixedZone("Asia/Shanghai", 8*60*60)

func (c Config) DailyEJHMergeDeadline(now time.Time) (time.Time, bool) {
	minute, ok := dailyMinute(c.DailyEJHMergeTime)
	if !c.DailyEJHMergeEnabled || !ok {
		return time.Time{}, false
	}
	local := now.In(scheduleLocation)
	return time.Date(local.Year(), local.Month(), local.Day(), minute/60, minute%60, 0, 0, scheduleLocation), true
}

type DailyWindow struct {
	HighDemandEnabled       bool   `json:"high_demand_enabled,omitempty"`
	HighDemandThreshold     int    `json:"high_demand_threshold,omitempty"`
	HighDemandConfirmations int    `json:"high_demand_confirmations,omitempty"`
	Start                   string `json:"start"`
	End                     string `json:"end"`
	AllowedProvider         string `json:"allowed_provider,omitempty"`
	TakeoverEnabled         bool   `json:"takeover_enabled,omitempty"`
}

type CardSchedule struct {
	Active   bool   `json:"active"`
	Blocked  bool   `json:"blocked"`
	Provider string `json:"provider,omitempty"`
	End      string `json:"end,omitempty"`
	Summary  string `json:"summary,omitempty"`
	Reason   string `json:"reason,omitempty"`
}

func CardProviderName(provider string) string {
	if provider == "nexovcc" {
		return "环球"
	}
	if provider == "ejh" {
		return "EJH"
	}
	return "未指定"
}

func windowContains(w DailyWindow, minute int) bool {
	start, _ := dailyMinute(w.Start)
	end, _ := dailyMinute(w.End)
	if start > end {
		return minute >= start || minute < end
	}
	return minute >= start && minute < end
}

func (c Config) dailyWindows() []DailyWindow {
	// Missing/null lists belong to older configurations. An explicit empty
	// list must not silently restore the old interval.
	if c.NoRestockWindows == nil {
		return []DailyWindow{{Start: c.NoRestockStart, End: c.NoRestockEnd}}
	}
	return c.NoRestockWindows
}

func (c Config) validateNoRestockWindows() error {
	windows := c.dailyWindows()
	if len(windows) == 0 {
		return fmt.Errorf("启用每日禁补时段时至少设置一个时间段")
	}
	for i, w := range windows {
		if w.HighDemandEnabled && (w.AllowedProvider != "" || w.HighDemandThreshold < 1 || w.HighDemandThreshold > 1000000 || w.HighDemandConfirmations < 1 || w.HighDemandConfirmations > 1000) {
			return fmt.Errorf("每日禁补时段 %d：高并发放行仅用于完全禁补，阈值应为 1～1000000，连续次数应为 1～1000", i+1)
		}
		start, startOK := dailyMinute(w.Start)
		end, endOK := dailyMinute(w.End)
		if !startOK || !endOK {
			return fmt.Errorf("每日禁补时段 %d：开始和结束时间必须为 HH:mm（00:00～23:59）", i+1)
		}
		if start == end {
			return fmt.Errorf("每日禁补时段 %d：开始和结束时间不能相同；全天停止请关闭自动补货总开关", i+1)
		}
		if w.AllowedProvider != "" && w.AllowedProvider != "ejh" && w.AllowedProvider != "nexovcc" {
			return fmt.Errorf("每日禁补时段 %d：允许卡台无效", i+1)
		}
		if w.TakeoverEnabled && w.AllowedProvider != "" {
			return fmt.Errorf("每日禁补时段 %d：环球接管只能选择完全禁补时段", i+1)
		}
	}
	for i, a := range windows {
		for j := i + 1; j < len(windows); j++ {
			b := windows[j]
			if a.AllowedProvider == "" || b.AllowedProvider == "" || a.AllowedProvider == b.AllowedProvider {
				continue
			}
			for minute := 0; minute < 1440; minute++ {
				if windowContains(a, minute) && windowContains(b, minute) {
					return fmt.Errorf("每日禁补时段 %d 和 %d 重叠，不能允许不同卡台", i+1, j+1)
				}
			}
		}
	}
	return nil
}

func dailyMinute(value string) (int, bool) {
	if len(value) != 5 || value[2] != ':' {
		return 0, false
	}
	for _, i := range []int{0, 1, 3, 4} {
		if value[i] < '0' || value[i] > '9' {
			return 0, false
		}
	}
	hour := int(value[0]-'0')*10 + int(value[1]-'0')
	minute := int(value[3]-'0')*10 + int(value[4]-'0')
	return hour*60 + minute, hour < 24 && minute < 60
}

// NoRestockReason gates new intents and dispatches, never ongoing workflows.
// The daily interval includes its start and excludes its end, including when
// it crosses midnight. Invalid enabled settings fail closed.
func (c Config) NoRestockReason(now time.Time) string {
	return c.CardSchedule(now).Reason
}

func (c Config) EffectiveCardProvider(now time.Time, defaultProvider string) string {
	if policy := c.CardSchedule(now); policy.Provider != "" {
		return policy.Provider
	}
	return defaultProvider
}

func (c Config) CardSchedule(now time.Time) CardSchedule {
	policy := c.StaticCardSchedule(now)
	demand := c.DemandStatus(now)
	if policy.Blocked && demand.Allowed {
		policy.Blocked = false
		policy.Reason = ""
		policy.Summary = "禁补期间高并发放行 · 至" + policy.End
	} else if policy.Blocked && demand.Active {
		policy.Reason += "；" + demand.Reason
	}
	return policy
}

func (c Config) StaticCardSchedule(now time.Time) CardSchedule {
	var policy CardSchedule
	if !c.NoRestockEnabled {
		return policy
	}
	if err := c.validateNoRestockWindows(); err != nil {
		return CardSchedule{Active: true, Blocked: true, Summary: "时段配置无效，暂停补货", Reason: err.Error() + "；暂停新增补货，请检查配置"}
	}
	local := now.In(scheduleLocation)
	minute := local.Hour()*60 + local.Minute()
	for _, w := range c.dailyWindows() {
		if !windowContains(w, minute) {
			continue
		}
		if policy.Blocked && w.AllowedProvider != "" {
			continue
		}
		// Complete prohibition wins. For equal policies show the latest end
		// among currently overlapping intervals, including overnight windows.
		end, _ := dailyMinute(w.End)
		oldEnd, _ := dailyMinute(policy.End)
		if policy.Active && policy.Provider == w.AllowedProvider && (end-minute+1440)%1440 <= (oldEnd-minute+1440)%1440 {
			continue
		}
		policy = CardSchedule{Active: true, Blocked: w.AllowedProvider == "", Provider: w.AllowedProvider, End: w.End}
		if policy.Blocked {
			policy.Summary = "完全禁补 · 至" + w.End
			policy.Reason = fmt.Sprintf("处于每日禁补时段 %s～%s（北京时间），暂停新增补货；已启动流程继续执行", w.Start, w.End)
		} else {
			policy.Summary = "仅" + CardProviderName(w.AllowedProvider) + "补货 · 至" + w.End
		}
	}
	return policy
}
