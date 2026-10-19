package httpapi

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"

	"chatgpt-space-merge/internal/model"
	"chatgpt-space-merge/internal/sub2"
)

func TestProQualityQueryFailuresUseConfiguredLimit(t *testing.T) {
	for _, action := range []string{"groups", "merge"} {
		t.Run(action, func(t *testing.T) {
			f := newProQualityFixture(t, action)
			q := f.policy
			q.ErrorFailureLimit = 3
			var err error
			f.policy, err = f.s.store.SaveProQualitySettings(q)
			if err != nil {
				t.Fatal(err)
			}
			f.setMode("error")
			for attempt := 1; attempt <= 3; attempt++ {
				if err := f.s.runProQualityBatch(context.Background(), true); err == nil {
					t.Fatal("expected query failure")
				}
				f.s.proMergeWG.Wait()
				p := f.profile(t)
				if p.ProQuality.ErrorFailures != attempt || p.ProQuality.ErrorFailureLimit != 3 {
					t.Fatalf("failure not persisted: %+v", p.ProQuality)
				}
				if attempt < 3 && (f.writes != 0 || len(f.calls) != 0 || p.ProQuality.Degraded) {
					t.Fatal("handled before configured threshold")
				}
			}
			p := f.profile(t)
			if !p.ProQuality.ErrorDegraded || p.ProQuality.Status != "degraded" || p.ProQuality.ActionStatus != "completed" {
				t.Fatalf("threshold did not execute action: %+v", p.ProQuality)
			}
			if action == "merge" {
				for _, stage := range []string{"invite", "accept", "transfer", "remove"} {
					if f.calls[stage] != 1 {
						t.Fatalf("%s ran %d times", stage, f.calls[stage])
					}
				}
			} else {
				if !reflect.DeepEqual(f.groups[42], []int64{2, 9}) {
					t.Fatalf("incorrect groups: %v", f.groups[42])
				}
				_ = f.s.runProQualityBatch(context.Background(), true)
				if f.writes != 1 {
					t.Fatal("repeated failure rewrote completed routing")
				}
				f.setMode("normal")
				f.run(t)
				p = f.profile(t)
				if p.ProQuality.ErrorFailures != 0 || p.ProQuality.ErrorDegraded || !reflect.DeepEqual(f.groups[42], []int64{1, 9}) {
					t.Fatalf("normal result did not reset/restore: %+v", p.ProQuality)
				}
			}
		})
	}
}

func TestProQualityDetectionErrorsDeduplicateAndExecute(t *testing.T) {
	for _, action := range []string{"groups", "merge"} {
		t.Run(action, func(t *testing.T) {
			f := newProQualityFixture(t, action)
			f.setMode("probe_error")
			at := time.Now().UTC()
			f.mu.Lock()
			f.probeAt = &at
			f.mu.Unlock()
			f.run(t)
			f.run(t)
			if f.profile(t).ProQuality.ErrorFailures != 1 || f.writes != 0 || len(f.calls) != 0 {
				t.Fatal("same failed detection counted twice")
			}
			next := at.Add(time.Second)
			f.mu.Lock()
			f.probeAt = &next
			f.mu.Unlock()
			f.run(t)
			f.s.proMergeWG.Wait()
			p := f.profile(t)
			if !p.ProQuality.ErrorDegraded || p.ProQuality.ErrorFailures != 2 || p.ProQuality.ActionStatus != "completed" {
				t.Fatalf("two real failures not handled: %+v", p.ProQuality)
			}
		})
	}
}

func TestProQualityFailureStreakReset(t *testing.T) {
	for _, change := range []string{"normal", "unknown", "clock", "policy", "identity"} {
		t.Run(change, func(t *testing.T) {
			f := newProQualityFixture(t, "groups")
			f.setMode("error")
			_ = f.s.runProQualityBatch(context.Background(), true)
			switch change {
			case "policy":
				q := f.policy
				q.ErrorFailureLimit = 3
				var err error
				f.policy, err = f.s.store.SaveProQualitySettings(q)
				if err != nil {
					t.Fatal(err)
				}
			case "identity":
				_, err := f.s.store.UpdateProAccount(f.email, func(p *model.MailAccountProfile) { now := time.Now(); p.ProQualitySince = &now })
				if err != nil {
					t.Fatal(err)
				}
			default:
				f.setMode(change)
				_ = f.s.runProQualityBatch(context.Background(), true)
				if f.profile(t).ProQuality.ErrorFailures != 0 {
					t.Fatal("intervening result did not break streak")
				}
			}
			f.setMode("error")
			_ = f.s.runProQualityBatch(context.Background(), true)
			p := f.profile(t)
			if p.ProQuality.ErrorFailures != 1 || p.ProQuality.ErrorDegraded || f.writes != 0 {
				t.Fatalf("old failures carried across %s: %+v", change, p.ProQuality)
			}
		})
	}
}

func TestProQualityFailedMergeQueueRechecksWithoutCounting(t *testing.T) {
	for _, recovery := range []string{"error", "normal", "disabled", "clock"} {
		t.Run(recovery, func(t *testing.T) {
			f := newProQualityFixture(t, "merge")
			value, _ := f.s.autoAdminLocks.LoadOrStore(f.admin.TeamAccountID, &sync.Mutex{})
			lock := value.(*sync.Mutex)
			lock.Lock()
			f.setMode("error")
			_ = f.s.runProQualityBatch(context.Background(), true)
			_ = f.s.runProQualityBatch(context.Background(), true)
			f.setMode(recovery)
			lock.Unlock()
			f.s.proMergeWG.Wait()
			p := f.profile(t)
			if recovery == "error" {
				if p.ProQuality.ActionStatus != "completed" || p.ProQuality.ErrorFailures != 2 || f.calls["transfer"] != 1 {
					t.Fatalf("failed evidence not respected: %+v", p.ProQuality)
				}
			} else if len(f.calls) != 0 || p.ProQuality.ErrorFailures != 0 {
				t.Fatalf("recovered or invalid result executed: %+v", p.ProQuality)
			}
		})
	}
}

func TestProQualityFailureEvidenceBoundaries(t *testing.T) {
	for _, invalid := range []string{"stale", "before_push", "future", "disabled", "unselected", "revision", "missing_time", "wrong_account", "clock", "cancelled"} {
		t.Run(invalid, func(t *testing.T) {
			q := model.DefaultProQualitySettings()
			q.Revision = "local"
			c := model.DefaultProSettings()
			now := time.Now().UTC()
			p := model.MailAccountProfile{ID: "a", Sub2AccountID: 42, CreatedAt: now.Add(-time.Hour)}
			p.ProQuality = model.ProQualityState{AccountQuality: model.AccountQuality{Identity: proQualityIdentity(p, c), Revision: q.Revision}, ErrorFailures: 1}
			snapshot := sub2.RemoteQualitySnapshot{ServerNow: now}
			snapshot.Settings.Enabled, snapshot.Settings.QuestionEnabled, snapshot.Settings.Revision = true, true, "remote"
			v := sub2.RemoteQualityResult{AccountID: 42, Revision: "remote", Version: "v1", Question: sub2.RemoteQualityQuestion{RemoteQualityVerdict: sub2.RemoteQualityVerdict{Status: "error", Error: "timeout", CheckedAt: &now}}}
			var readErr error
			switch invalid {
			case "stale":
				old := now.Add(-time.Hour)
				v.Question.CheckedAt = &old
			case "before_push":
				p.CreatedAt = now.Add(time.Second)
			case "future":
				later := now.Add(time.Hour)
				v.Question.CheckedAt = &later
			case "disabled":
				snapshot.Settings.QuestionEnabled = false
			case "unselected":
				q.QuestionEnabled = false
				q.ModelAuditEnabled = true
			case "revision":
				v.Revision = "old"
			case "missing_time":
				v.Question.CheckedAt = nil
			case "wrong_account":
				v.AccountID = 43
			case "clock":
				readErr = errProQualityClock
			case "cancelled":
				readErr = context.Canceled
			}
			state, bad, _ := proQualityObservation(p, q, c, snapshot, v, readErr)
			if bad || state.ErrorFailures != 0 || state.ErrorDegraded {
				t.Fatalf("invalid evidence counted: %+v", state)
			}
		})
	}
}

func TestProQualityRecheckDoesNotConsumeNewFailureSample(t *testing.T) {
	q := model.DefaultProQualitySettings()
	q.Revision = "local"
	c := model.DefaultProSettings()
	p := model.MailAccountProfile{ID: "a", Sub2AccountID: 42, CreatedAt: time.Now().Add(-time.Hour)}
	for i := 0; i < 3; i++ {
		state, bad, _ := observeProQuality(p, q, c, sub2.RemoteQualitySnapshot{}, sub2.RemoteQualityResult{}, errors.New("timeout"), false)
		p.ProQuality = state
		if bad || state.ErrorFailures != 0 || state.ErrorSample != "" {
			t.Fatal("merge validation advanced or consumed failure evidence")
		}
	}
}

func TestProQualityErrorFailureLimitAPIValidation(t *testing.T) {
	f := newProQualityFixture(t, "groups")
	for _, limit := range []int{0, -1, 21, 1, 5, 20} {
		body := fmt.Sprintf(`{"enabled":false,"error_failure_limit":%d}`, limit)
		w := f.request(f.s.saveProQualitySettings, "PUT", "/api/pro-quality/settings", body)
		if limit < 1 || limit > 20 {
			if w.Code != 400 {
				t.Fatalf("invalid limit %d accepted", limit)
			}
			continue
		}
		if w.Code != 200 {
			t.Fatal(w.Body.String())
		}
		saved, err := f.s.store.ProQualitySettings()
		if err != nil || saved.ErrorFailureLimit != limit {
			t.Fatalf("limit not saved: %+v %v", saved, err)
		}
	}
}
