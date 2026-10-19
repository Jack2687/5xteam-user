package restock

import (
	"math"
	"sort"
	"time"
)

type ForceMergeStats struct {
	Participants   int      `json:"participants"`
	Untimed        int      `json:"untimed"`
	Unknown        int      `json:"unknown"`
	AverageMinutes *float64 `json:"average_minutes,omitempty"`
	Below          int      `json:"below"`
	Pending        int      `json:"pending"`
	Ready          int      `json:"ready"`
	Triggered      bool     `json:"triggered"`
	Sources        []string `json:"sources"`
}

// A ready replacement covers exactly one original account lifecycle.
func ForceMergeSummary(c Config, s Snapshot, tasks []Task, now time.Time) ForceMergeStats {
	out := ForceMergeStats{Sources: []string{}}
	covered := map[string]Task{}
	ready := map[int64]bool{}
	for _, a := range s.Accounts {
		ready[a.ID] = a.Ready
	}
	for _, t := range tasks {
		if t.GroupID != s.GroupID || t.ForceMergeSource == "" {
			continue
		}
		if t.Active() || (t.Status == "ready" && ready[t.AccountID]) {
			covered[t.ForceMergeSource] = t
		}
	}
	total := 0.0
	candidates := []Account{}
	for _, a := range s.Accounts {
		if !a.Ready || (!a.EJH && a.CardProvider != "nexovcc") {
			continue
		}
		if a.PushTimeoutEnabled != nil && !*a.PushTimeoutEnabled {
			out.Untimed++
			continue
		}
		if a.PushTimeoutEnabled == nil || a.ForceMergeDeadline == nil || a.ForceMergeKey == "" {
			out.Unknown++
			continue
		}
		left := math.Max(0, a.ForceMergeDeadline.Sub(now).Minutes())
		total += left
		out.Participants++
		if t, ok := covered[a.ForceMergeKey]; ok {
			if t.Status == "ready" {
				out.Ready++
			} else {
				out.Pending++
			}
		}
		if left < float64(c.ForceMergeMinutes) {
			out.Below++
			candidates = append(candidates, a)
		}
	}
	if out.Participants > 0 {
		avg := total / float64(out.Participants)
		out.AverageMinutes = &avg
		out.Triggered = c.ForceMergeEnabled && avg < float64(c.ForceMergeMinutes)
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].ForceMergeDeadline.Equal(*candidates[j].ForceMergeDeadline) {
			return candidates[i].ForceMergeKey < candidates[j].ForceMergeKey
		}
		return candidates[i].ForceMergeDeadline.Before(*candidates[j].ForceMergeDeadline)
	})
	if out.Triggered {
		for _, a := range candidates {
			if _, ok := covered[a.ForceMergeKey]; !ok {
				out.Sources = append(out.Sources, a.ForceMergeKey)
			}
		}
	}
	return out
}
