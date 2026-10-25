package httpapi

import (
	"chatgpt-space-merge/internal/gptpay"
	"chatgpt-space-merge/internal/model"
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

func mergeV2Fixture(t *testing.T) *proExecutionFixture {
	f := newProExecutionFixture(t)
	f.s.stopProAutomations()
	v, _, _, _ := f.s.store.ProSettings()
	v.AutoMergeV2Enabled = true
	v.AutoMergeEnabled = false
	v.AutoDeleteEnabled = false
	v.AutoMerchantEnabled = false
	if _, _, err := f.s.store.SaveProAutomationSettings(v, gptpay.Settings{PlanCode: "pro5"}, ""); err != nil {
		t.Fatal(err)
	}
	_, err := f.s.store.UpdateProAccount(f.email, func(p *model.MailAccountProfile) {
		p.ProAuto = model.ProAutoState{ID: "v2-auto", Stage: "quota", Status: "waiting_quota", Steps: map[string]string{"push": "completed", "quota": "completed"}, StartedAt: time.Now()}
	})
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func mockMergeV2Submit(ctx context.Context) (map[string]any, error) {
	o := ctx.Value(mergeV2LoginKey{}).(*mergeV2LoginOptions)
	for _, method := range []string{"merge_v2_ready", "merge_v2_submitting", "merge_v2_accepted"} {
		if _, err := o.Call(method, map[string]any{"team_id": o.TeamID, "user_id": "pro-user"}); err != nil {
			return nil, err
		}
	}
	return map[string]any{"success": true}, nil
}

func TestProMergeV2FullTailAndNoDuplicate(t *testing.T) {
	f := mergeV2Fixture(t)
	logins := 0
	f.s.proMergeV2Login = func(ctx context.Context, email string) (map[string]any, error) {
		logins++
		return mockMergeV2Submit(ctx)
	}
	if err := f.s.runProAutomationTail(t.Context(), f.email); err != nil {
		t.Fatal(err)
	}
	p, c, _ := f.s.store.MailAccountCredential(f.email)
	if !p.MergeV2Completed() || !p.MergeV2Removed() || p.ProAuto.Status != "completed" || p.ProAuto.Stage != "merge_v2" || p.SpaceMergedOnce || p.TransferStatus == "completed" {
		t.Fatalf("bad independent state: %+v", p.ProMergeV2)
	}
	if c.AccessToken != "child-token" || c.RefreshToken != "child-rt" {
		t.Fatal("push credentials overwritten")
	}
	if f.calls["invite"] != 1 || f.calls["remove"] != 1 || f.calls["accept"] != 0 || f.calls["transfer"] != 0 {
		t.Fatal(f.calls)
	}
	if _, err := f.s.performProDisposition(t.Context(), f.email); err != nil {
		t.Fatal(err)
	}
	if logins != 1 || f.calls["remove"] != 1 {
		t.Fatal("repeated completed request")
	}
}

func TestProMergeV2UnknownPreservesMemberAndMode(t *testing.T) {
	f := mergeV2Fixture(t)
	logins := 0
	f.s.proMergeV2Login = func(ctx context.Context, email string) (map[string]any, error) {
		logins++
		o := ctx.Value(mergeV2LoginKey{}).(*mergeV2LoginOptions)
		for _, method := range []string{"merge_v2_ready", "merge_v2_submitting"} {
			if _, err := o.Call(method, map[string]any{"team_id": o.TeamID, "user_id": "pro-user"}); err != nil {
				return nil, err
			}
		}
		return nil, errors.New("connection lost after POST")
	}
	if _, err := f.s.performProMergeV2(t.Context(), f.email); err == nil {
		t.Fatal("unknown result ignored")
	}
	p, _, _ := f.s.store.MailAccountCredential(f.email)
	if p.ProMergeV2.Status != "unknown" || p.ProMergeV2.SubmittedAt == nil || f.calls["remove"] != 0 {
		t.Fatal(p.ProMergeV2, f.calls)
	}
	v, _, _, _ := f.s.store.ProSettings()
	v.AutoMergeV2Enabled = false
	v.AutoDeleteEnabled = true
	if _, _, err := f.s.store.SaveProAutomationSettings(v, gptpay.Settings{PlanCode: "pro5"}, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.performProDisposition(t.Context(), f.email); !errors.Is(err, errMergeV2Unknown) {
		t.Fatal(err)
	}
	if logins != 1 {
		t.Fatal("resubmitted unknown request")
	}
	if err := f.s.store.RecoverProWorkflows(); err != nil {
		t.Fatal(err)
	}
	p, _, _ = f.s.store.MailAccountCredential(f.email)
	if p.ProMergeV2.SubmittedAt == nil || p.ProDeactivation != nil {
		t.Fatal("recovery lost pin")
	}
	// Only an explicit verified outcome unlocks the accepted tail.
	body := fmt.Sprintf(`{"stage":"submit","status":"completed","expected_status":"unknown","expected_updated_at":%q}`, p.UpdatedAt.Format(time.RFC3339Nano))
	w := f.request(f.s.updateProMergeV2Stage, "PUT", "/stage", body)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if _, err := f.s.performProDisposition(t.Context(), f.email); err != nil {
		t.Fatal(err)
	}
	if logins != 1 || f.calls["remove"] != 1 {
		t.Fatal("incorrect accepted continuation", f.calls, logins)
	}
}

func TestProMergeV2StopAfterAcceptedAndResume(t *testing.T) {
	f := mergeV2Fixture(t)
	f.s.proMergeV2Login = func(ctx context.Context, email string) (map[string]any, error) {
		out, err := mockMergeV2Submit(ctx)
		if _, e := f.s.store.UpdateProAccount(email, func(p *model.MailAccountProfile) { p.ProAuto.StopRequested = true }); e != nil {
			t.Fatal(e)
		}
		return out, err
	}
	if _, err := f.s.performProMergeV2(t.Context(), f.email); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	p, _, _ := f.s.store.MailAccountCredential(f.email)
	if !p.MergeV2Accepted() || p.MergeV2Completed() || p.ProAuto.Status != "stopped" || f.calls["remove"] != 0 {
		t.Fatal("stop ignored", p.ProMergeV2)
	}
	_, _ = f.s.store.UpdateProAccount(f.email, func(p *model.MailAccountProfile) { p.ProAuto.StopRequested = false })
	f.s.proMergeV2Login = func(context.Context, string) (map[string]any, error) {
		t.Fatal("login repeated after acceptance")
		return nil, nil
	}
	if _, err := f.s.performProMergeV2(t.Context(), f.email); err != nil {
		t.Fatal(err)
	}
	p, _, _ = f.s.store.MailAccountCredential(f.email)
	if !p.MergeV2Completed() {
		t.Fatal(p.ProMergeV2)
	}
}

func TestProMergeV2CheckpointsRejectWrongTeamAndStoppedSubmission(t *testing.T) {
	f := mergeV2Fixture(t)
	_, _ = f.s.store.UpdateProAccount(f.email, func(p *model.MailAccountProfile) {
		p.ProMergeV2 = &model.ProMergeV2{TeamID: "original-team", Steps: map[string]string{}}
	})
	if _, err := f.s.mergeV2Checkpoint(t.Context(), f.email, "original-team", "merge_v2_submitting", map[string]any{"team_id": "wrong"}); err == nil {
		t.Fatal("wrong team accepted")
	}
	_, _ = f.s.store.UpdateProAccount(f.email, func(p *model.MailAccountProfile) { p.ProAuto.StopRequested = true })
	if _, err := f.s.mergeV2Checkpoint(t.Context(), f.email, "original-team", "merge_v2_submitting", map[string]any{"team_id": "original-team"}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	p, _, _ := f.s.store.MailAccountCredential(f.email)
	if p.ProMergeV2.SubmittedAt != nil {
		t.Fatal("submitted while stopped")
	}
}

func TestProMergeV2ConcurrentSubmissionAndLateAcceptance(t *testing.T) {
	f := mergeV2Fixture(t)
	_, err := f.s.store.UpdateProAccount(f.email, func(p *model.MailAccountProfile) {
		p.ProMergeV2 = &model.ProMergeV2{TeamID: "original-team"}
	})
	if err != nil {
		t.Fatal(err)
	}
	args := map[string]any{"team_id": "original-team", "user_id": "pro-user"}
	if _, err = f.s.mergeV2Checkpoint(t.Context(), f.email, "original-team", "merge_v2_submitting", args); err == nil {
		t.Fatal("submission allowed before verified login")
	}
	if _, err = f.s.mergeV2Checkpoint(t.Context(), f.email, "original-team", "merge_v2_ready", args); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() {
			<-start
			_, e := f.s.mergeV2Checkpoint(t.Context(), f.email, "original-team", "merge_v2_submitting", args)
			results <- e
		}()
	}
	close(start)
	allowed, blocked := 0, 0
	for i := 0; i < 2; i++ {
		e := <-results
		if e == nil {
			allowed++
		} else if errors.Is(e, errMergeV2Unknown) {
			blocked++
		} else {
			t.Fatal(e)
		}
	}
	if allowed != 1 || blocked != 1 {
		t.Fatalf("duplicate submission: allowed=%d blocked=%d", allowed, blocked)
	}
	_, err = f.s.store.UpdateProAccount(f.email, func(p *model.MailAccountProfile) { p.ProAuto.StopRequested = true })
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err = f.s.mergeV2Checkpoint(ctx, f.email, "original-team", "merge_v2_accepted", nil); err != nil {
		t.Fatal("lost late accepted fact after stop", err)
	}
	p, _, _ := f.s.store.MailAccountCredential(f.email)
	if !p.MergeV2Accepted() || p.ProAuto.Status != "stopped" || p.MergeV2Removed() {
		t.Fatal("late acceptance restarted stopped workflow", p.ProMergeV2)
	}
	if _, err = f.s.mergeV2Checkpoint(ctx, f.email, "original-team", "merge_v2_rejected", nil); err == nil {
		t.Fatal("late rejection erased acceptance")
	}
}

func TestProMergeV2FilterAndSettingsIsolation(t *testing.T) {
	f := mergeV2Fixture(t)
	v, _, _, _ := f.s.store.ProSettings()
	if !v.AutoMergeV2Enabled || v.Disposition() != "merge_v2" {
		t.Fatal("mode not saved")
	}
	for _, mode := range []string{"merge", "deactivate", "merchant"} {
		x := v
		x.AutoMergeEnabled = mode == "merge"
		x.AutoDeleteEnabled = mode == "deactivate"
		x.AutoMerchantEnabled = mode == "merchant"
		if x.ValidateDisposition() == nil {
			t.Fatal("modes not exclusive", mode)
		}
	}
	f.s.proMergeV2Login = func(ctx context.Context, _ string) (map[string]any, error) { return mockMergeV2Submit(ctx) }
	if _, err := f.s.performProMergeV2(t.Context(), f.email); err != nil {
		t.Fatal(err)
	}
	items, total, summary, err := f.s.store.ProAccountsPage("", "merge_v2", 20, 0)
	if err != nil || total != 1 || len(items) != 1 || summary.MergeV2 != 1 || summary.Merged != 0 || summary.Merchant != 0 || summary.Deleted != 0 {
		t.Fatal(total, summary, err)
	}
	items, total, _, err = f.s.store.ProAccountsPage("", "in_progress", 20, 0)
	if err != nil || total != 0 || len(items) != 0 {
		t.Fatal("completed remains in progress", err)
	}
}

func TestProMergeV2UnknownResetRequiresExplicitConfirmation(t *testing.T) {
	f := mergeV2Fixture(t)
	now := time.Now()
	p, err := f.s.store.UpdateProAccount(f.email, func(p *model.MailAccountProfile) {
		p.ProMergeV2 = &model.ProMergeV2{TeamID: "original-team", Status: "unknown", Stage: "submit", UserID: "pro-user", SubmittedAt: &now, Steps: map[string]string{"invite": "completed", "login": "completed", "submit": "unknown"}}
	})
	if err != nil {
		t.Fatal(err)
	}
	body := fmt.Sprintf(`{"stage":"submit","status":"pending","expected_status":"unknown","expected_updated_at":%q}`, p.UpdatedAt.Format(time.RFC3339Nano))
	w := f.request(f.s.updateProMergeV2Stage, "PUT", "/stage", body)
	if w.Code != 400 {
		t.Fatal(w.Code, w.Body.String())
	}
	body = fmt.Sprintf(`{"stage":"submit","status":"pending","confirm_not_accepted":true,"expected_status":"unknown","expected_updated_at":%q}`, p.UpdatedAt.Format(time.RFC3339Nano))
	w = f.request(f.s.updateProMergeV2Stage, "PUT", "/stage", body)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	p, _, _ = f.s.store.MailAccountCredential(f.email)
	if p.ProMergeV2.SubmittedAt != nil || p.ProMergeV2.TeamID != "original-team" {
		t.Fatal(p.ProMergeV2)
	}
}
