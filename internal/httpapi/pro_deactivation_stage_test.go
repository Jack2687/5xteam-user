package httpapi

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"chatgpt-space-merge/internal/model"
)

func deletionStageEdit(t *testing.T, f *proExecutionFixture, stage, status string) int {
	t.Helper()
	p, _, err := f.s.store.MailAccountCredential(f.email)
	if err != nil {
		t.Fatal(err)
	}
	previous := ""
	if p.ProDeactivation != nil {
		previous = p.ProDeactivation.Steps[stage]
	}
	body, _ := json.Marshal(map[string]any{"stage": stage, "status": status, "expected_status": previous, "expected_updated_at": p.UpdatedAt})
	r := f.request(f.s.updateProDeletionStage, "PUT", "/deletion-stage", string(body))
	if r.Code != 200 {
		t.Log(r.Body.String())
	}
	return r.Code
}

func TestProDeletionManualConfirmationStopsTimersAndAllowsManualCleanup(t *testing.T) {
	f := newProGroupFixture(t)
	enableDeletion(t, f.proExecutionFixture)
	now := time.Now().Add(-time.Hour)
	_, err := f.s.store.UpdateProAccount(f.email, func(p *model.MailAccountProfile) {
		p.ProAuto = model.ProAutoState{ID: "manual-confirm", Status: "awaiting_deletion", Steps: map[string]string{}}
		p.ProDeactivation = &model.ProDeactivation{Status: "unknown", Stage: "submit", SubmittedAt: &now, Error: "response lost", AccountID: "personal", UserID: "pro-user", NextCheckAt: &now, Steps: map[string]string{"credentials": "completed", "verify": "completed", "submit": "unknown"}}
	})
	if err != nil {
		t.Fatal(err)
	}
	before, creds, _ := f.s.store.MailAccountCredential(f.email)
	f.s.proDeleteHooks = &proDeactivationHooks{Protocol: func(context.Context, map[string]any, func(string, map[string]any) (any, error)) (map[string]any, error) {
		t.Error("manual confirmation re-queried or resubmitted deletion")
		return nil, nil
	}}
	if code := deletionStageEdit(t, f.proExecutionFixture, "confirm", "completed"); code != 200 {
		t.Fatal(code)
	}
	p, afterCreds, _ := f.s.store.MailAccountCredential(f.email)
	d := p.ProDeactivation
	if !p.DeletionConfirmed() || !p.DeletionAccepted() || !d.SubmittedAt.Equal(now) || d.ConfirmationSource != "manual" || d.Manual["confirm"].Status != "completed" || d.Error != "" {
		t.Fatalf("manual confirmation missing: %+v", d)
	}
	if d.NextCheckAt != nil || !p.ProPostMergeGroupsPending() || p.ProAuto.Status != "completed" {
		t.Fatal("manual confirmation did not stop background work")
	}
	if !reflect.DeepEqual(creds, afterCreds) || p.SpaceMergedOnce != before.SpaceMergedOnce || p.TransferStatus != before.TransferStatus || len(f.calls) != 0 || f.reads.Load() != 0 || f.writes.Load() != 0 {
		t.Fatal("editing performed remote action or changed unrelated facts")
	}
	if !p.ProStages().Manual["deactivate"] {
		t.Fatal("full automation omitted manual attribution")
	}
	if err = f.s.store.RecoverProWorkflows(); err != nil {
		t.Fatal(err)
	}
	f.s.tickProDeactivations(t.Context())
	if err = f.s.runProAutomationTail(t.Context(), f.email); err != nil {
		t.Fatal(err)
	}
	if _, err = f.s.performProDisposition(t.Context(), f.email); err != nil {
		t.Fatal(err)
	}
	if f.reads.Load() != 0 || f.writes.Load() != 0 {
		t.Fatal("manual confirmation scheduled downstream work after restart")
	}
	for _, action := range []string{"deactivate", "automatic"} {
		if _, err = f.s.startProEndJob(f.email, nil, nil, action); err == nil {
			t.Fatal("stale automatic job resumed confirmed deletion", action)
		}
	}
	// Explicit operator retries may still finish the saved grouping plan.
	if p, err = f.s.performProDeactivation(t.Context(), f.email); err != nil || p.ProPostMergeGroupsPending() || p.ProDeactivation.NextCheckAt != nil || p.ProAuto.Status != "completed" {
		t.Fatal("cleanup failed", err)
	}
	if _, err = f.s.performProDeactivation(t.Context(), f.email); err != nil || f.writes.Load() != 1 {
		t.Fatal("completed cleanup repeated", err)
	}
	if err = f.s.store.RecoverProWorkflows(); err != nil {
		t.Fatal(err)
	}
	due, err := f.s.store.DueProDeactivations(time.Now().Add(24 * time.Hour))
	if err != nil || len(due) != 0 {
		t.Fatal("manual confirmation still polled", err)
	}
	// Reopening confirmation keeps the original submission guard.
	if code := deletionStageEdit(t, f.proExecutionFixture, "confirm", "unknown"); code != 200 {
		t.Fatal(code)
	}
	p, _, _ = f.s.store.MailAccountCredential(f.email)
	if p.DeletionConfirmed() || !p.DeletionAccepted() || !p.ProDeactivation.SubmittedAt.Equal(now) || p.ProDeactivation.NextCheckAt == nil || p.ProDeactivation.ConfirmationSource != "" {
		t.Fatal("reopening removed submission or did not schedule read")
	}
	checks := 0
	f.s.proDeleteHooks = &proDeactivationHooks{Protocol: func(_ context.Context, payload map[string]any, _ func(string, map[string]any) (any, error)) (map[string]any, error) {
		checks++
		if payload["check_only"] != true {
			t.Fatal("reopened confirmation resubmitted deletion")
		}
		return map[string]any{"status": "confirmed", "message": "fixture confirmed"}, nil
	}}
	p, err = f.s.performProDeactivation(t.Context(), f.email)
	if err != nil || checks != 1 || p.ProDeactivation.NextCheckAt != nil || p.ProDeactivation.ConfirmationSource != "upstream" || p.ProDeactivation.Manual["confirm"].Status != "" {
		t.Fatal("upstream result did not supersede manual status", err)
	}
}

func TestProDeletionManualStagesKeepSubmissionGuard(t *testing.T) {
	f := newProExecutionFixture(t)
	enableDeletion(t, f)
	for _, stage := range []string{"credentials", "verify"} {
		for _, status := range []string{"not_started", "pending", "completed", "failed", "interrupted", "unknown"} {
			if code := deletionStageEdit(t, f, stage, status); code != 200 {
				t.Fatal(stage, status, code)
			}
			p, _, _ := f.s.store.MailAccountCredential(f.email)
			if p.DeletionSubmitted() || p.ProDeactivation.Steps[stage] != status || p.ProDeactivation.Manual[stage].Status != status {
				t.Fatal("pre-submit edit created submission or lost annotation")
			}
			if p.ProStages().Steps["deactivate"] == "completed" {
				t.Fatal("an individual preparatory step marked the entire deletion complete")
			}
		}
	}
	if code := deletionStageEdit(t, f, "submit", "completed"); code != 200 {
		t.Fatal(code)
	}
	p, _, _ := f.s.store.MailAccountCredential(f.email)
	submitted := *p.ProDeactivation.SubmittedAt
	for _, status := range []string{"failed", "not_started", "unknown", "pending", "interrupted"} {
		if code := deletionStageEdit(t, f, "submit", status); code != 200 {
			t.Fatal(code)
		}
		p, _, _ = f.s.store.MailAccountCredential(f.email)
		if !p.DeletionAccepted() || !p.ProDeactivation.SubmittedAt.Equal(submitted) || p.ProDeactivation.NextCheckAt == nil {
			t.Fatal("status edit removed replay guard")
		}
	}
	if len(f.calls) != 0 {
		t.Fatal("edit called upstream")
	}
}

func TestProDeletionManualStageRejectsStaleAndRunning(t *testing.T) {
	f := newProExecutionFixture(t)
	enableDeletion(t, f)
	p, _, _ := f.s.store.MailAccountCredential(f.email)
	body, _ := json.Marshal(map[string]any{"stage": "confirm", "status": "completed", "expected_status": "", "expected_updated_at": p.UpdatedAt})
	unlock := f.s.lockProAccount(f.email)
	if rec := f.request(f.s.updateProDeletionStage, "PUT", "/deletion-stage", string(body)); rec.Code != 409 {
		t.Fatal("concurrent edit accepted")
	}
	unlock()
	if _, err := f.s.store.UpdateProAccount(f.email, func(p *model.MailAccountProfile) { p.ProWorkflowRunning = true }); err != nil {
		t.Fatal(err)
	}
	if code := deletionStageEdit(t, f, "confirm", "completed"); code != 409 {
		t.Fatal("running edit accepted")
	}
	if _, err := f.s.store.UpdateProAccount(f.email, func(p *model.MailAccountProfile) { p.ProWorkflowRunning = false }); err != nil {
		t.Fatal(err)
	}
	if rec := f.request(f.s.updateProDeletionStage, "PUT", "/deletion-stage", string(body)); rec.Code != 409 {
		t.Fatal("stale edit accepted")
	}
	for _, input := range []string{`{"stage":"confirm","status":"running"}`, `{"stage":"confirm","status":"skipped"}`, `{"stage":"merge","status":"completed"}`} {
		if rec := f.request(f.s.updateProDeletionStage, "PUT", "/deletion-stage", input); rec.Code != 400 {
			t.Fatal("invalid edit accepted")
		}
	}
	p, _, _ = f.s.store.MailAccountCredential(f.email)
	if p.ProDeactivation != nil {
		t.Fatal("rejected edit changed state")
	}
}
