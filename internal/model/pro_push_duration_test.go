package model

import (
	"testing"
	"time"
)

func TestProPushDurationFreezesAtDisposition(t *testing.T) {
	start := time.Date(2026, 10, 21, 0, 0, 0, 0, time.UTC)
	end := start.Add(25 * time.Minute)
	for _, kind := range []string{"queued", "auto_merge", "delete", "merged", "manual"} {
		t.Run(kind, func(t *testing.T) {
			p := MailAccountProfile{ProPushStartedAt: &start}
			p.FreezeProPushDuration(end)
			if p.ProPushEndedAt != nil {
				t.Fatal("active quota clock frozen")
			}
			switch kind {
			case "queued":
				p.ProWorkflowRunning = true
			case "auto_merge":
				p.ProAuto = ProAutoState{Status: "running", Stage: "merge", StageTimes: map[string]time.Time{"merge:running": end}}
			case "delete":
				p.ProDeactivation = &ProDeactivation{StartedAt: end}
			case "merged":
				p.SpaceMergedOnce = true
				p.SpaceMergedAt = &end
			case "manual":
				p.ProManualStages = map[string]ProStageProgress{"merge": {StartedAt: end}}
			}
			p.FreezeProPushDuration(end)
			if p.ProPushEndedAt == nil || !p.ProPushEndedAt.Equal(end) {
				t.Fatal("end not recorded")
			}
			p.ProWorkflowRunning = false
			p.ProAuto.StageTimes = map[string]time.Time{"merge:running": end.Add(time.Hour)}
			p.FreezeProPushDuration(end.Add(2 * time.Hour))
			if !p.ProPushEndedAt.Equal(end) {
				t.Fatal("retry moved end")
			}
		})
	}
}
