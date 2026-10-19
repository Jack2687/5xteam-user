package httpapi

import (
	"sort"
	"time"

	"chatgpt-space-merge/internal/gptpay"
	"chatgpt-space-merge/internal/model"
	"chatgpt-space-merge/internal/restock"
	"chatgpt-space-merge/internal/store"
)

type proRestockPeriod struct {
	StartMinute int    `json:"start_minute"`
	EndMinute   int    `json:"end_minute"`
	Provider    string `json:"card_provider,omitempty"`
	Plan        string `json:"plan_code,omitempty"`
	Blocked     bool   `json:"blocked"`
	Source      string `json:"source"`
	Error       string `json:"error,omitempty"`
}

type proRestockTimetable struct {
	Periods []proRestockPeriod `json:"periods"`
	Current proRestockPeriod   `json:"current"`
}

func (p proRestockPeriod) sameRule(other proRestockPeriod) bool {
	p.StartMinute, p.EndMinute = 0, 0
	other.StartMinute, other.EndMinute = 0, 0
	return p == other
}

// Project the same purchase resolver and prohibition policy used at dispatch
// onto a Beijing business day. No provider calls or workflow changes occur.
func proRestockDailyTimetable(p model.ProSettings, pay gptpay.Settings, c restock.Config, now time.Time) proRestockTimetable {
	local := now.In(time.FixedZone("Asia/Shanghai", 8*3600))
	day := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, local.Location())
	boundaries := map[int]bool{0: true, 1440: true}
	add := func(value string) {
		if parsed, err := time.Parse("15:04", value); err == nil && parsed.Format("15:04") == value {
			boundaries[parsed.Hour()*60+parsed.Minute()] = true
		}
	}
	if p.PurchaseScheduleEnabled {
		for _, w := range p.PurchaseWindows {
			add(w.Start)
			add(w.End)
		}
	}
	if c.NoRestockEnabled {
		if c.NoRestockWindows == nil {
			add(c.NoRestockStart)
			add(c.NoRestockEnd)
		}
		for _, w := range c.NoRestockWindows {
			add(w.Start)
			add(w.End)
		}
	}
	minutes := make([]int, 0, len(boundaries))
	for minute := range boundaries {
		minutes = append(minutes, minute)
	}
	sort.Ints(minutes)
	out := proRestockTimetable{Periods: []proRestockPeriod{}}
	for i, start := range minutes[:len(minutes)-1] {
		at := day.Add(time.Duration(start) * time.Minute)
		policy := c.CardSchedule(at)
		period := proRestockPeriod{StartMinute: start, EndMinute: minutes[i+1], Blocked: policy.Blocked, Source: "default"}
		if policy.Blocked {
			period.Source = "blocked"
			// Invalid legacy settings fail closed, just like dispatch.
			if policy.End == "" {
				period.Error = policy.Reason
			}
		} else {
			selected, payment, err := store.ResolveProPurchase(p, pay, c, at)
			period.Provider, period.Plan = selected.AutoCardProvider(), payment.PlanCode
			if w, matched := p.PurchaseWindow(at); matched {
				period.Source, period.Plan = "purchase", w.PlanCode
			}
			if policy.Provider != "" {
				period.Source = "allowed_provider"
			}
			if err != nil {
				period.Error = err.Error()
			}
		}
		last := len(out.Periods) - 1
		if last >= 0 && out.Periods[last].sameRule(period) {
			out.Periods[last].EndMinute = period.EndMinute
		} else {
			out.Periods = append(out.Periods, period)
		}
	}
	// Join the two sides of midnight into one readable recurring interval.
	if last := len(out.Periods) - 1; last > 0 && out.Periods[0].sameRule(out.Periods[last]) {
		out.Periods[last].EndMinute = out.Periods[0].EndMinute
		out.Periods = out.Periods[1:]
	}
	minute := local.Hour()*60 + local.Minute()
	for _, period := range out.Periods {
		inside := minute >= period.StartMinute && minute < period.EndMinute
		if period.StartMinute > period.EndMinute {
			inside = minute >= period.StartMinute || minute < period.EndMinute
		}
		if inside {
			out.Current = period
			break
		}
	}
	return out
}
