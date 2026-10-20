package httpapi

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"time"

	"chatgpt-space-merge/internal/model"
	"chatgpt-space-merge/internal/restock"
	"chatgpt-space-merge/internal/sub2"
)

type fallbackRuleState struct {
	Rule    restock.FallbackRule        `json:"rule"`
	Monitor restock.FallbackObservation `json:"monitor"`
	Plan    restock.FallbackPlan        `json:"plan"`
	Source  string                      `json:"-"`
}

func (s *Server) collectFallbackRules(ctx context.Context, f restock.FallbackConfig, c restock.Config) []fallbackRuleState {
	rules := f.EffectiveRules(c.GroupID)
	rows := make([]fallbackRuleState, len(rules))
	s.proFallback.mu.Lock()
	old := append([]fallbackRuleState(nil), s.proFallback.rules...)
	s.proFallback.mu.Unlock()
	jobs := make(chan int)
	var wg sync.WaitGroup
	for n := 0; n < min(4, len(rules)); n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				r := rules[i]
				row := fallbackRuleState{Rule: r, Monitor: restock.FallbackObservation{GroupID: r.MonitorGroupID}}
				if f.Enabled && r.Enabled {
					policy := c
					policy.GroupID = r.MonitorGroupID
					sampleCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
					row.Monitor, row.Source = s.collectFallbackSource(sampleCtx, policy)
					cancel()
					for _, prev := range old {
						if row.Monitor.Error != "" && row.Source == prev.Source && prev.Monitor.Count != nil {
							row.Monitor.Count, row.Monitor.At, row.Monitor.GroupName = prev.Monitor.Count, prev.Monitor.At, prev.Monitor.GroupName
							break
						}
					}
				}
				row.Plan = f.RuleConfig(r).Plan(s.fallbackNow(), row.Monitor)
				rows[i] = row
			}
		}()
	}
	for i := range rules {
		jobs <- i
	}
	close(jobs)
	wg.Wait()
	return rows
}

// A shared account stays enabled while any validated rule needs it. Unknown
// rules can prevent a shutdown, but can never authorize an activation.
func fallbackAccountPlan(f restock.FallbackConfig, rows []fallbackRuleState, id int64, account *sub2.FallbackAccount, now time.Time) (on, hold bool, group int64, reason string) {
	for _, row := range rows {
		r := row.Rule
		if !f.Enabled || !r.Enabled || !slices.Contains(r.AccountIDs, id) {
			continue
		}
		member := account == nil
		for _, g := range r.GroupIDs {
			if account == nil || account.InGroup(g) {
				member = true
				group = g
				break
			}
		}
		if !member {
			reason = "账号已离开所属规则的兜底分组，请重新选择"
			continue
		}
		p := f.RuleConfig(r).Plan(now, row.Monitor)
		if p.Hold {
			hold = true
		}
		if p.Active {
			on = true
		}
	}
	if on {
		return true, false, group, ""
	}
	if hold {
		return false, true, group, "相关监控分组数据待确认，保持当前调度"
	}
	return false, false, group, reason
}

func fallbackSummary(f restock.FallbackConfig, rows []fallbackRuleState) restock.FallbackPlan {
	if !f.Enabled {
		return restock.FallbackPlan{Phase: "disabled", Reason: "兜底未启用；关闭已接管账号调度"}
	}
	active, off, unknown := 0, 0, 0
	for _, r := range rows {
		if !r.Rule.Enabled {
			continue
		}
		if r.Plan.Hold {
			unknown++
		} else if r.Plan.Active {
			active++
		} else {
			off++
		}
	}
	p := restock.FallbackPlan{Active: active > 0, Hold: unknown > 0, Phase: "off", Reason: fmt.Sprintf("%d 条开启兜底 · %d 条关闭兜底 · %d 条待核验", active, off, unknown)}
	if len(rows) == 1 {
		return rows[0].Plan
	}
	if unknown > 0 {
		p.Phase = "unknown"
	} else if active > 0 && off > 0 {
		p.Phase = "mixed"
	} else if active > 0 {
		p.Phase = "active"
	}
	return p
}

func fallbackRowsForDisplay(f restock.FallbackConfig, c restock.Config, p model.ProSettings, password string, saved []fallbackRuleState, now time.Time) []fallbackRuleState {
	rows := []fallbackRuleState{}
	for _, rule := range f.EffectiveRules(c.GroupID) {
		policy := c
		policy.GroupID = rule.MonitorGroupID
		row := fallbackRuleState{Rule: rule, Monitor: restock.FallbackObservation{GroupID: rule.MonitorGroupID}}
		for _, old := range saved {
			if old.Rule.MonitorGroupID == rule.MonitorGroupID && old.Source == fallbackSourceKey(policy, p, password) {
				row.Monitor, row.Source = old.Monitor, old.Source
				break
			}
		}
		row.Plan = f.RuleConfig(rule).Plan(now, row.Monitor)
		rows = append(rows, row)
	}
	return rows
}
