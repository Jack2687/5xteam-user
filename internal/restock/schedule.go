package restock

import (
	"fmt"
	"time"
)

// Use Beijing business time independently of the host/container time zone.
var scheduleLocation = time.FixedZone("Asia/Shanghai", 8*60*60)

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
	start, startOK := dailyMinute(c.NoRestockStart)
	end, endOK := dailyMinute(c.NoRestockEnd)
	if !startOK || !endOK || start == end {
		return "每日禁补时段配置无效，暂停新增补货，请检查开始和结束时间"
	}
	local := now.In(scheduleLocation)
	minute := local.Hour()*60 + local.Minute()
	inside := minute >= start && minute < end
	if start > end {
		inside = minute >= start || minute < end
	}
	if inside {
		return fmt.Sprintf("处于每日禁补时段 %s～%s（北京时间），暂停新增补货；已启动流程继续执行", c.NoRestockStart, c.NoRestockEnd)
	}
	return ""
}
