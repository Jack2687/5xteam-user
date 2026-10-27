package httpapi

import (
	"chatgpt-space-merge/internal/model"
	"context"
	"errors"
	"testing"
	"time"
)

func TestProQualityConnectionDeadPreventsAllDegradationActions(t *testing.T) {
	for _, action := range []string{"merge", "siwc", "groups"} {
		t.Run(action, func(t *testing.T) {
			f := newProQualityFixture(t, action)
			f.setMode("error")
			f.setConnectionMode("dead")
			_ = f.s.runProQualityBatch(context.Background(), true)
			if f.profile(t).IsProDead() {
				t.Fatal("detected before limit")
			}
			_ = f.s.runProQualityBatch(context.Background(), true)
			f.s.proMergeWG.Wait()
			p := f.profile(t)
			if !p.IsProDead() || p.ProAuto.Status != "dead" || f.connectionTests != 1 || len(f.calls) != 0 || p.ProSIWC != nil {
				t.Fatal("death not isolated", p.ProAuto.Status, f.connectionTests, len(f.calls))
			}
			if _, e := f.s.startProEndJob(f.email, nil, nil, "manual_merge"); !errors.Is(e, errProAccountDead) {
				t.Fatal("manual merge permitted", e)
			}
		})
	}
}
func TestProDeadDetectionExcludesTransportAndIntentionalDeletion(t *testing.T) {
	for _, message := range []string{"API returned 401: unauthorized", "CONNECT tunnel failed, response 504", "invalid_grant", "context canceled"} {
		if isDeadOAuthResult(nil, message) {
			t.Fatal("false death", message)
		}
	}
	p := model.MailAccountProfile{ManagementScope: "pro"}
	if !p.CanMarkProDead() {
		t.Fatal("pro not eligible")
	}
	at := time.Now()
	p.ProDeactivation = &model.ProDeactivation{SubmittedAt: &at}
	if p.CanMarkProDead() {
		t.Fatal("requested deletion counted as loss")
	}
	if !isProProxyTransportFailure(errors.New("Failed curl (7) CONNECT tunnel failed, response 504")) || isProProxyTransportFailure(errors.New("API returned 401 account_deactivated")) {
		t.Fatal("wrong transport classifier")
	}
}

func TestProDeadRoutesGroupsWithoutClaimingRefundOrMerge(t *testing.T) {
	f := newProGroupFixture(t)
	p, err := f.s.markProDead(f.email, "connection_test", "account_deactivated")
	if err != nil {
		t.Fatal(err)
	}
	p, err = f.s.finishProPostMergeGroups(context.Background(), p)
	if err != nil || p.ProPostMergeGroups.Status != "completed" || p.ProPostMergeGroups.Action != "dead" || f.writes.Load() != 1 {
		t.Fatal("dead group move failed", err, p.ProPostMergeGroups)
	}
	if p.SpaceMergedOnce || p.MergeV2Accepted() || p.DeletionAccepted() || p.TransferStatus == "completed" {
		t.Fatal("fabricated disposal outcome")
	}
	if _, err = f.s.finishProPostMergeGroups(context.Background(), p); err != nil || f.writes.Load() != 1 {
		t.Fatal("repeated group write", err)
	}
}

func TestProDeadMergeLoginStopsBeforeSubmission(t *testing.T) {
	for _, message := range []string{"account_deactivated", "CONNECT tunnel failed, response 504"} {
		t.Run(message, func(t *testing.T) {
			f := mergeV2Fixture(t)
			f.s.proMergeV2Login = func(context.Context, string) (map[string]any, error) {
				return map[string]any{"success": false, "error": message}, nil
			}
			_, err := f.s.performProMergeV2(context.Background(), f.email)
			p, _, _ := f.s.store.MailAccountCredential(f.email)
			if err == nil || p.IsProDead() != (message == "account_deactivated") || p.MergeV2Accepted() || f.calls["remove"] != 0 {
				t.Fatal("wrong login failure handling", err, p.IsProDead())
			}
		})
	}
}
