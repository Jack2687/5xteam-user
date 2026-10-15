package restock

import (
	"math"
	"time"
)

// ObserveWindows never interprets resets, stale observations, or plan changes as consumption.
func ObserveWindows(current, previous []Window, samePlan bool, maxAge time.Duration) []Window {
	for i := range current {
		w := &current[i]
		w.Rate, w.Samples = 0, 0
		if !samePlan {
			continue
		}
		for _, p := range previous {
			elapsed := w.At.Sub(p.At)
			if p.Seconds != w.Seconds || p.ResetAt != w.ResetAt || elapsed < time.Second || elapsed > maxAge || !validWindow(w.Used, 0) || !validWindow(p.Used, p.Rate) || w.Used < p.Used {
				continue
			}
			rate := (w.Used - p.Used) / elapsed.Minutes()
			// React to rising consumption immediately; decay more slowly after a fall.
			w.Rate = math.Max(rate, p.Rate*.6+rate*.4)
			w.Samples = p.Samples + 1
			break
		}
	}
	return current
}
func Pressure(c Config, s Snapshot) bool {
	if !s.Valid || !s.LoadValid {
		return false
	}
	capacity, used, queue := 0, 0, 0
	for _, a := range s.Accounts {
		if a.Ready {
			capacity += a.Capacity
		}
		used += a.Used
		queue += a.Waiting
	}
	return queue > 0 || (capacity > 0 && float64(used)*100 >= float64(capacity*c.TriggerPercent))
}

func Evaluate(c Config, s Snapshot, tasks []Task, limits Limits, confirmations, newSlots int, lead float64, now time.Time) Decision {
	d := Decision{At: now, Version: c.Version, Snapshot: s, Config: c, Limits: limits, Confirmations: confirmations, LeadMinutes: lead, TriggerMinutes: float64(c.TriggerMinutes), TargetMinutes: float64(c.TargetMinutes), Warnings: []string{}}
	if c.Early {
		d.TriggerMinutes = math.Max(d.TriggerMinutes, lead+float64(c.MarginMinutes))
		d.TargetMinutes = math.Max(d.TargetMinutes, d.TriggerMinutes+1)
	}
	for _, a := range s.Accounts {
		if a.Ready {
			d.Usable++
			d.Capacity += a.Capacity
		}
		d.Used += a.Used
		d.Waiting += a.Waiting
	}
	timely := 0
	for _, t := range tasks {
		if t.Active() {
			d.Inflight++
			if t.Predictable() && t.Binding == s.Binding && t.GroupID == s.GroupID && !t.ETA.After(now.Add(time.Duration(lead*float64(time.Minute)))) {
				timely++
			}
		}
	}
	block := func(reason string) Decision { d.Suggested = 0; d.Reason = reason; return d }
	if !s.Valid || s.At.IsZero() || now.Sub(s.At) > time.Duration(c.FreshSeconds)*time.Second || s.At.After(now.Add(5*time.Second)) {
		return block(first(s.Error, "监控快照缺失或过期，暂停新增"))
	}
	for _, a := range s.Accounts {
		if a.Shared {
			return block("监控组包含多组共享账号，无法可靠归属需求")
		}
	}
	faults := 0
	for _, a := range s.Accounts {
		if a.Fault {
			faults++
		}
	}
	if faults > 0 && faults*100 >= len(s.Accounts)*c.FaultPercent {
		return block("监控组大量账号异常或过载，先排查上游；暂停新增")
	}
	if !s.LoadValid {
		return block(first(s.Error, "并发采集完整性未知，暂停新增"))
	}
	if lead >= float64(c.ForecastMinutes) || d.TargetMinutes > float64(c.ForecastMinutes) {
		return block("预计就绪或补后目标超出预测上限，等待在途推进")
	}
	if newSlots < 1 {
		return block("推送设置的单账号并发无效")
	}
	d.DemandKnown = true
	for _, a := range s.Accounts {
		if a.ExpiresAt == nil || !a.Ready || len(a.Windows) == 0 {
			continue
		}
		left := 100.0
		trusted := true
		for _, w := range a.Windows {
			if !validWindow(w.Used, w.Rate) || w.Samples < 2 || now.Sub(w.At) > time.Duration(c.QuotaSeconds*2)*time.Second || w.ResetAt <= a.ExpiresAt.Unix() {
				trusted = false
			}
			left = math.Min(left, math.Max(0, 100-w.Used-w.Rate*math.Max(0, a.ExpiresAt.Sub(w.At).Minutes())))
		}
		if trusted {
			d.IdleEquivalent += left / 100
		}
	}
	arrival := now.Add(time.Duration(lead * float64(time.Minute)))
	futureCapacity := 0
	for _, a := range s.Accounts {
		if usableAt(a, arrival, now, time.Duration(c.QuotaSeconds*2)*time.Second) {
			futureCapacity += a.Capacity
		}
	}
	if c.ConcurrencyEnabled && confirmations >= c.Confirmations && Pressure(c, s) {
		d.ConcurrencyNeed = max(0, int(math.Ceil((float64(d.Used+d.Waiting)*100/float64(c.TargetPercent)-float64(futureCapacity+timely*newSlots))/float64(newSlots))))
	}
	if c.TimeEnabled {
		base, coverage, ok := forecast(c, s, tasks, 0, newSlots, lead, now, 0)
		d.Coverage = coverage
		if ok {
			d.SupplyMinutes = &base
			if base < lead {
				d.Warnings = append(d.Warnings, "到货前可能出现供给缺口；新号不能消除已经发生的等待")
			}
			if base <= d.TriggerMinutes {
				// Existing arrivals can already cover the future even when the
				// unavoidable gap before arrival remains. Zero is a valid net need.
				lo, hi := 0, c.MaxInflight
				for lo < hi {
					mid := (lo + hi) / 2
					until, _, _ := forecast(c, s, tasks, mid, newSlots, lead, now, lead)
					if until >= d.TargetMinutes {
						hi = mid
					} else {
						lo = mid + 1
					}
				}
				until, _, _ := forecast(c, s, tasks, lo, newSlots, lead, now, lead)
				if until >= d.TargetMinutes {
					d.TimeNeed = lo
				} else {
					d.Warnings = append(d.Warnings, "受就绪时间、使用时限或在途上限影响，时间目标暂不可达；暂停时间侧新增，避免持续补满")
				}
			}
		} else {
			d.Warnings = append(d.Warnings, "额度样本不足、套餐口径不同或窗口过期，预计支撑时间暂不可估")
		}
	}
	d.ReserveNeed = max(0, c.Reserve-d.Usable-timely)
	// This optional ordinary-purchase guard never overrides confirmed pressure
	// or the explicitly configured reserve. Rates are trusted by forecast above.
	if c.MinNewUsePercent > 0 && s.LifetimeMinutes > 0 && d.SupplyMinutes != nil && d.TimeNeed > 0 {
		rates := map[int64]float64{}
		for _, a := range s.Accounts {
			if a.Ready && a.Plan == s.NewPlan {
				for _, w := range a.Windows {
					rates[w.Seconds] += w.Rate
				}
			}
		}
		peak := 0.0
		for _, rate := range rates {
			peak = math.Max(peak, rate)
		}
		limit := max(0, int(math.Floor(peak*float64(s.LifetimeMinutes)/float64(c.MinNewUsePercent)-float64(futureCapacity)/float64(newSlots)))-timely)
		if d.TimeNeed > limit {
			d.TimeNeed = limit
			d.Warnings = append(d.Warnings, "按推送等待时限与最低预计使用比例限制普通补货；持续并发和最低备用仍独立生效")
		}
	}
	need := max(d.TimeNeed, d.ConcurrencyNeed, d.ReserveNeed)
	if need == 0 {
		return block("没有已确认的净缺口；继续采样并跟踪在途")
	}
	if limits.Failures >= c.FailureLimit {
		return block("连续失败已达熔断阈值，处理原因后在配置页重置熔断")
	}
	cooldown := c.CooldownSeconds
	if d.Waiting >= c.EmergencyQueue {
		cooldown = c.EmergencyCooldownSeconds
	}
	if limits.LastBatch != nil && now.Sub(*limits.LastBatch) < time.Duration(cooldown)*time.Second {
		return block("正常／紧急冷却尚未结束")
	}
	hard := min(c.MaxBatch, c.MaxInflight-d.Inflight, c.PoolLimit-len(s.Accounts)-d.Inflight, c.HourLimit-limits.HourCount, c.DayLimit-limits.DayCount)
	if c.HourBudget > 0 {
		hard = min(hard, int(max(int64(0), c.HourBudget-limits.HourCost)/max(int64(1), c.UnitCost)))
	}
	if c.DayBudget > 0 {
		hard = min(hard, int(max(int64(0), c.DayBudget-limits.DayCost)/max(int64(1), c.UnitCost)))
	}
	if !c.AllowSmall && hard < c.MinBatch {
		return block("在途、池容量或预算余量不足最小批量")
	}
	if !c.AllowSmall {
		need = max(need, c.MinBatch)
	}
	d.Suggested = max(0, min(need, hard))
	if d.Suggested == 0 {
		return block("达到在途、账号池、小时／日数量或预算上限")
	}
	d.Reason = "时间、并发与备用净缺口取最大值，及时在途已在各规则中扣除"
	if !c.Enabled {
		d.Reason = "总开关关闭，仅展示建议"
	} else if c.Mode == "observe" {
		d.Reason = "观察模式，仅计算建议"
	}
	return d
}
func first(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
func usableAt(a Account, t, now time.Time, maxAge time.Duration) bool {
	if !a.Ready || (a.ExpiresAt != nil && !a.ExpiresAt.After(t)) {
		return false
	}
	for _, w := range a.Windows {
		if w.Fresh(now, maxAge) && w.ResetAt > t.Unix() && w.Used+w.Rate*math.Max(0, t.Sub(w.At).Minutes()) >= 100 {
			return false
		}
	}
	return true
}

// Forecast uses percentage-point rates only within one verified plan and the
// same set of quota windows. It integrates available accounts, resets, expiry
// and arrivals on a 15-second timeline; money and tokens never enter this model.
// A gap before new arrivals is reported separately, not compensated by buying
// an unbounded number of accounts that will arrive too late.
func forecast(c Config, s Snapshot, tasks []Task, extra, slots int, lead float64, now time.Time, checkFrom float64) (float64, float64, bool) {
	type supply struct {
		a         Account
		arrive    float64
		remaining map[int64]float64
		reset     map[int64]int64
	}
	pool := []supply{}
	rates := map[int64]float64{}
	plan := ""
	eligible, covered := 0, 0
	var template []Window
	for _, a := range s.Accounts {
		if !a.Ready {
			continue
		}
		eligible++
		good := a.Plan != "" && len(a.Windows) > 0
		seenWindows := map[int64]bool{}
		for _, w := range a.Windows {
			if seenWindows[w.Seconds] || !validWindow(w.Used, w.Rate) || w.At.After(now.Add(5*time.Second)) || w.Samples < 2 || w.Seconds <= 0 || w.ResetAt <= now.Unix() || now.Sub(w.At) > time.Duration(c.QuotaSeconds*2)*time.Second {
				good = false
			}
			seenWindows[w.Seconds] = true
		}
		if !good {
			continue
		}
		if s.NewPlan == "" || s.NewPlan != a.Plan {
			return 0, 0, false
		}
		covered++
		if plan != "" && plan != a.Plan {
			return 0, 0, false
		}
		plan = a.Plan
		if template == nil {
			template = a.Windows
		} else {
			if len(template) != len(a.Windows) {
				return 0, 0, false
			}
			for _, w := range template {
				found := false
				for _, v := range a.Windows {
					if v.Seconds == w.Seconds {
						found = true
					}
				}
				if !found {
					return 0, 0, false
				}
			}
		}
		p := supply{a: a, remaining: map[int64]float64{}, reset: map[int64]int64{}}
		for _, w := range a.Windows {
			p.remaining[w.Seconds] = math.Max(0, 100-w.Used-w.Rate*math.Max(0, now.Sub(w.At).Minutes()))
			p.reset[w.Seconds] = w.ResetAt
			rates[w.Seconds] += w.Rate
		}
		pool = append(pool, p)
	}
	coverage := float64(covered) * 100 / float64(max(1, eligible))
	if coverage < float64(c.QuotaCoverage) || len(pool) == 0 {
		return 0, coverage, false
	}
	positive := false
	for _, v := range rates {
		if v > 0 {
			positive = true
		}
	}
	if !positive {
		return 0, coverage, false
	}
	add := func(at float64) {
		p := supply{a: Account{Ready: true, Capacity: slots}, arrive: at, remaining: map[int64]float64{}, reset: map[int64]int64{}}
		if s.LifetimeMinutes > 0 {
			expires := now.Add(time.Duration((at + float64(s.LifetimeMinutes)) * float64(time.Minute)))
			p.a.ExpiresAt = &expires
		}
		for _, w := range template {
			p.remaining[w.Seconds] = 100
			p.reset[w.Seconds] = now.Add(time.Duration(at*float64(time.Minute))).Unix() + w.Seconds
		}
		pool = append(pool, p)
	}
	for _, t := range tasks {
		if t.Predictable() && t.GroupID == s.GroupID && t.Binding == s.Binding && t.Plan == s.NewPlan {
			add(math.Max(0, t.ETA.Sub(now).Minutes()))
		}
	}
	for range extra {
		add(lead)
	}
	for minute := 0.; minute <= float64(c.ForecastMinutes); minute += .25 {
		at := now.Add(time.Duration(minute * float64(time.Minute)))
		available := []int{}
		capacity := 0
		for i := range pool {
			p := &pool[i]
			if minute < p.arrive || (p.a.ExpiresAt != nil && !p.a.ExpiresAt.After(at)) {
				continue
			}
			ok := true
			for sec, remain := range p.remaining {
				if at.Unix() >= p.reset[sec] {
					p.remaining[sec] = 100
					p.reset[sec] += sec
					remain = 100
				}
				if remain <= 0 {
					ok = false
				}
			}
			if ok {
				available = append(available, i)
				capacity += p.a.Capacity
			}
		}
		demand := 0
		for _, a := range s.Accounts {
			demand += a.Used + a.Waiting
		}
		gap := capacity == 0 || capacity < demand
		if gap && minute >= checkFrom {
			return minute, coverage, true
		}
		if capacity == 0 {
			continue
		}
		for _, i := range available {
			for sec, rate := range rates {
				pool[i].remaining[sec] -= rate * .25 * float64(pool[i].a.Capacity) / float64(capacity)
			}
		}
	}
	return float64(c.ForecastMinutes), coverage, true
}
func validWindow(used, rate float64) bool {
	return !math.IsNaN(used) && !math.IsInf(used, 0) && !math.IsNaN(rate) && !math.IsInf(rate, 0) && used >= 0 && used <= 100 && rate >= 0
}
