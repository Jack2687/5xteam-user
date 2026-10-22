package model

import (
	"testing"
	"time"
)

func TestProDeletionDisplayAndCompletion(t *testing.T) {
	now := time.Now()
	p := MailAccountProfile{ManagementScope: "pro", ProAuto: ProAutoState{ID: "auto"}, ProDeactivation: &ProDeactivation{Status: "running", Steps: map[string]string{}}, ProPostMergeGroups: &ProPostMergeGroups{Action: "deactivate", Status: "pending"}}
	if p.ProStages().Steps["deactivate"] != "running" {
		t.Fatal("pending group plan falsely displayed as failure")
	}
	p.ProDeactivation.SubmittedAt, p.ProDeactivation.AcceptedAt = &now, &now
	p.ProDeactivation.Status = "queued"
	p.ReconcileProAutoDeletion(now)
	if p.ProAuto.Status != "awaiting_deletion" || p.ProStages().Steps["deactivate"] != "waiting" {
		t.Fatal("queued deletion shown as completed")
	}
	p.ProDeactivation.ConfirmedAt = &now
	p.ProDeactivation.Status = "completed"
	p.ReconcileProAutoDeletion(now)
	if p.ProAuto.Status != "completed" || p.ProStages().Steps["deactivate"] != "completed" || p.DeletionNeedsContinuation() {
		t.Fatal("confirmed deletion continued waiting for groups")
	}
	p.ProPostMergeGroups.Status = "completed"
	p.ReconcileProAutoDeletion(now)
	if p.ProAuto.Status != "completed" {
		t.Fatal("completed cleanup not reflected")
	}
	p.ProAuto.StopRequested = true
	p.ReconcileProAutoDeletion(now)
	if p.ProAuto.Status != "stopped" {
		t.Fatal("stop request overwritten")
	}
}

func TestProDeletionConfirmationStopsClocksWithCleanupErrors(t *testing.T) {
	for _, source := range []string{"manual", "http_401", "upstream"} {
		t.Run(source, func(t *testing.T) {
			now := time.Now()
			p := MailAccountProfile{
				ProAuto:            ProAutoState{ID: "auto", Status: "awaiting_deletion", NextCheckAt: &now},
				ProDeactivation:    &ProDeactivation{Status: "completed", SubmittedAt: &now, AcceptedAt: &now, ConfirmedAt: &now, NextCheckAt: &now, ConfirmationSource: source, Error: "group unavailable"},
				ProPostMergeGroups: &ProPostMergeGroups{Action: "deactivate", Status: "failed", Error: "group unavailable"},
				VM2:                &ProVM2Binding{Stage: "delete_failed", Error: "cleanup unavailable"},
			}
			if !p.ReconcileProAutoDeletion(now) || p.DeletionNeedsContinuation() || p.ProDeactivation.NextCheckAt != nil || p.ProAuto.NextCheckAt != nil || p.ProAuto.Status != "completed" || p.ProAuto.Error != "" || p.ProStages().Steps["deactivate"] != "completed" {
				t.Fatal("confirmed deletion is not terminal")
			}
			if p.ProPostMergeGroups.Status != "failed" || p.ProDeactivation.Error != "group unavailable" {
				t.Fatal("cleanup error was lost")
			}
			if p.ReconcileProAutoDeletion(now.Add(time.Minute)) {
				t.Fatal("completed state did not stabilize")
			}
		})
	}
}
