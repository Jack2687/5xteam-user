package httpapi

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"chatgpt-space-merge/internal/restock"
)

func notificationNumber(n float64) string {
	if math.IsNaN(n) || math.IsInf(n, 0) {
		return "未记录"
	}
	return strings.TrimRight(strings.TrimRight(strconv.FormatFloat(n, 'f', 2, 64), "0"), ".")
}

// Match the active rules in the decision detail, using that decision's inputs.
// Do not substitute current settings or sum overlapping replacement demands.
func restockNotificationReasons(d restock.Decision) string {
	c := d.Config
	lines := []string{}
	add := func(name string, need int, evidence string) {
		if need > 0 {
			lines = append(lines, fmt.Sprintf("%s · 净缺口 %d 个\n%s", name, need, evidence))
		}
	}
	supply := "暂不可估"
	if d.SupplyMinutes != nil {
		supply = notificationNumber(*d.SupplyMinutes) + " 分钟"
	}
	add("预计供给不足", d.TimeNeed, fmt.Sprintf("预计支撑 %s；实际触发水位 ≤ %s 分钟；补后目标 %s 分钟。", supply, notificationNumber(d.TriggerMinutes), notificationNumber(d.TargetMinutes)))
	utilization := "无有效容量"
	if d.Capacity > 0 {
		utilization = notificationNumber(float64(d.Used)*100/d.Capacity) + "%"
	}
	add("持续并发压力", d.ConcurrencyNeed, fmt.Sprintf("原始容量 %d × %d%% = %s。占用 %d / 容量 %s（%s），触发 ≥ %d 并发（%d%% 向下取整）；账号侧等待 %d，有等待也参与触发；连续确认 %d / %d 次。", d.ReportedCapacity, c.CapacityPercent, notificationNumber(d.Capacity), d.Used, notificationNumber(d.Capacity), utilization, c.ConcurrencyTrigger(d.ReportedCapacity), c.TriggerPercent, d.Waiting, d.Confirmations, c.Confirmations))
	if e := d.Exit; e != nil {
		add("账号退出前补货", d.ExitNeed, fmt.Sprintf("手动提前 %d 分钟；临期 %d 个，当前容量 %s，退出后容量 %s，采用需求 %d。", c.ExitMinutes, e.AtRisk, notificationNumber(e.CurrentCapacity), notificationNumber(e.AfterCapacity), e.Demand))
	}
	reserve := fmt.Sprintf("目标 %d 个 − 当前可用 %d 个", c.Reserve, d.Usable)
	empty := fmt.Sprintf("监控组实际 %d 个账号；空组启动设置 %d 个", len(d.Snapshot.Accounts), c.EmptyGroupCount)
	if d.Trace != nil {
		reserve += fmt.Sprintf(" − 预计及时就绪在途 %d 个", d.Trace.TimelyInflight)
		empty += fmt.Sprintf("，扣除同组全部在途 %d 个（包含结果未知）", d.Trace.GroupInflight)
	}
	add("最低备用", d.ReserveNeed, reserve+"。")
	add("空分组启动", d.EmptyGroupNeed, empty+"。")
	average := "暂不可估"
	if d.ForceMerge.AverageMinutes != nil {
		average = notificationNumber(*d.ForceMerge.AverageMinutes) + " 分钟"
	}
	add("强制合并前接替", d.ForceMergeNeed, fmt.Sprintf("参与账号 %d 个；平均剩余 %s，触发条件 < %d 分钟。", d.ForceMerge.Participants, average, c.ForceMergeMinutes))
	if len(lines) == 0 {
		return "具体触发依据未记录，请查看本轮补货决策"
	}
	return strings.Join(lines, "\n\n")
}
