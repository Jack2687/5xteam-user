package restock

import (
	"fmt"
	"time"
)

// Use Beijing business time independently of the host/container time zone.
var scheduleLocation = time.FixedZone("Asia/Shanghai", 8*60*60)

type DailyWindow struct {
	Start string `json:"start"`
	End   string `json:"end"`
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
		start, startOK := dailyMinute(w.Start)
		end, endOK := dailyMinute(w.End)
		if !startOK || !endOK {
			return fmt.Errorf("每日禁补时段 %d：开始和结束时间必须为 HH:mm（00:00～23:59）", i+1)
		}
		if start == end {
			return fmt.Errorf("每日禁补时段 %d：开始和结束时间不能相同；全天停止请关闭自动补货总开关", i+1)
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
	if !c.NoRestockEnabled {
		return ""
	}
	if err := c.validateNoRestockWindows(); err != nil {
		return err.Error() + "；暂停新增补货，请检查配置"
	}
	local := now.In(scheduleLocation)
	minute := local.Hour()*60 + local.Minute()
	for _, w := range c.dailyWindows() {
		start, _ := dailyMinute(w.Start)
		end, _ := dailyMinute(w.End)
		inside := minute >= start && minute < end
		if start > end {
			inside = minute >= start || minute < end
		}
		if inside {
			return fmt.Sprintf("处于每日禁补时段 %s～%s（北京时间），暂停新增补货；已启动流程继续执行", w.Start, w.End)
		}
	}
	return ""
}
