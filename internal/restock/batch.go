package restock

import (
	"fmt"
	"time"
)

// BatchWait describes why the next batch cannot be reserved. Existing members
// of the current batch still dispatch within the automation concurrency limit.
type BatchWait struct {
	Remaining int    `json:"remaining"`
	Queued    int    `json:"queued"`
	Verifying int    `json:"verifying"`
	Attention int    `json:"attention"`
	Refresh   bool   `json:"refresh"`
	Message   string `json:"message"`
}

// RefreshedAfter requires both a new upstream load sample and a collection
// started after the milestone; a request already in flight is not a refresh.
func (s Snapshot) RefreshedAfter(at time.Time) bool {
	return s.Valid && s.LoadValid && s.At.After(at) &&
		(s.CollectionStartedAt.IsZero() || s.CollectionStartedAt.After(at))
}

func WaitForBatch(s Snapshot, tasks []Task) *BatchWait {
	w := &BatchWait{}
	var finished time.Time
	for _, t := range tasks {
		if t.Active() {
			w.Remaining++
			switch t.Status {
			case "reserved":
				w.Queued++
			case "verifying":
				w.Verifying++
			case "attention", "unknown":
				w.Attention++
			}
			continue
		}
		if t.Binding != s.Binding || t.GroupID != s.GroupID {
			continue
		}
		at := t.UpdatedAt
		if t.ReadyAt != nil {
			at = *t.ReadyAt
		}
		if at.After(finished) {
			finished = at
		}
	}
	if w.Remaining > 0 {
		w.Message = fmt.Sprintf("本批剩余 %d 个，全部推送并经监控核验后再评估下一批", w.Remaining)
		if w.Attention > 0 {
			w.Message = fmt.Sprintf("本批剩余 %d 个，其中 %d 个结果待核验；暂停下一批", w.Remaining, w.Attention)
		} else if w.Verifying == w.Remaining {
			w.Message = "本批已推送，等待监控刷新并核验可用"
		}
		return w
	}
	if !finished.IsZero() && !s.RefreshedAfter(finished) {
		w.Refresh = true
		w.Message = "本批已结束，等待监控刷新后重新评估"
		return w
	}
	return nil
}
