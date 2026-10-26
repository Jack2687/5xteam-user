package httpapi

import (
	"context"
	"sync"
	"testing"
	"time"

	"chatgpt-space-merge/internal/model"
)

func (f *proQualityFixture) setConnectionMode(mode string) {
	f.mu.Lock()
	f.connectionMode = mode
	f.mu.Unlock()
}

func TestProQualityConnectionRecoveryPreventsEveryAction(t *testing.T) {
	for _, action := range []string{"groups", "merge", "siwc"} {
		for _, source := range []string{"error", "probe_error"} {
			t.Run(action+"/"+source, func(t *testing.T) {
				f := newProQualityFixture(t, action)
				f.setMode(source)
				f.setConnectionMode("normal")
				for i := 0; i < 2; i++ {
					_ = f.s.runProQualityBatch(context.Background(), true)
					if i == 0 && f.connectionTests != 0 {
						t.Fatal("tested before the configured failure limit")
					}
				}
				f.s.proMergeWG.Wait()
				p := f.profile(t)
				if f.connectionTests != 1 || p.ProQuality.Status != "normal" || p.ProQuality.ErrorFailures != 0 || p.ProQuality.ErrorDegraded || p.ProQuality.Degraded || p.ProQuality.Error != "" || !p.ProQuality.SourceRecovered {
					t.Fatalf("connection recovery was not saved: tests=%d state=%+v", f.connectionTests, p.ProQuality)
				}
				if len(f.calls) != 0 || f.writes != 0 || p.ProSIWC != nil || p.ProQuality.Action != "" {
					t.Fatalf("recovery still dispatched %s", action)
				}
			})
		}
	}
}

func TestProQualityConnectionFailureContinuesOriginalHandling(t *testing.T) {
	for _, mode := range []string{"upstream_error", "http_error", "truncated", "empty"} {
		t.Run(mode, func(t *testing.T) {
			f := newProQualityFixture(t, "groups")
			f.setMode("error")
			f.setConnectionMode(mode)
			_ = f.s.runProQualityBatch(context.Background(), true)
			_ = f.s.runProQualityBatch(context.Background(), true)
			state := f.profile(t).ProQuality
			if f.connectionTests != 1 || !state.ErrorDegraded || state.ActionStatus != "completed" || f.writes != 1 || state.ConnectionCheck == nil || state.ConnectionCheck.Status != "error" {
				t.Fatalf("failed connection did not preserve handling: %+v", state)
			}
		})
	}
}

func TestProQualityConnectionRecoveryDoesNotRecountCachedError(t *testing.T) {
	f := newProQualityFixture(t, "groups")
	f.setMode("probe_error")
	f.setConnectionMode("normal")
	at := time.Now().UTC()
	f.mu.Lock()
	f.probeAt = &at
	f.mu.Unlock()
	f.run(t)
	next := at.Add(time.Second)
	f.mu.Lock()
	f.probeAt = &next
	f.mu.Unlock()
	f.run(t)
	initial := f.profile(t).ProQuality
	if initial.ValidUntil == nil {
		t.Fatalf("second distinct error did not trigger recovery: tests=%d state=%+v", f.connectionTests, initial)
	}
	until := *initial.ValidUntil
	for i := 0; i < 3; i++ {
		f.run(t)
	}
	state := f.profile(t).ProQuality
	if f.connectionTests != 1 || state.Status != "normal" || state.ErrorFailures != 0 || !state.ValidUntil.Equal(until) || f.writes != 0 {
		t.Fatalf("cached error erased recovery or extended validity: %+v", state)
	}
	nextSample := next.Add(time.Second)
	f.mu.Lock()
	f.probeAt = &nextSample
	f.mu.Unlock()
	f.run(t)
	if state = f.profile(t).ProQuality; state.ErrorFailures != 1 || state.Status != "error" || f.connectionTests != 1 {
		t.Fatalf("new error did not start a fresh streak: %+v", state)
	}
}

func TestProQualityQueuedConnectionRecoveryCancelsDisposition(t *testing.T) {
	f := newProQualityFixture(t, "merge")
	value, _ := f.s.autoAdminLocks.LoadOrStore(f.admin.TeamAccountID, &sync.Mutex{})
	lock := value.(*sync.Mutex)
	lock.Lock()
	f.setMode("error")
	_ = f.s.runProQualityBatch(context.Background(), true)
	_ = f.s.runProQualityBatch(context.Background(), true)
	f.setConnectionMode("normal")
	lock.Unlock()
	f.s.proMergeWG.Wait()
	state := f.profile(t).ProQuality
	if f.connectionTests != 2 || len(f.calls) != 0 || state.Status != "normal" || state.ActionStarted || state.ActionStatus != "cancelled" || state.ErrorFailures != 0 {
		t.Fatalf("recovered queued account still ran: tests=%d calls=%v state=%+v", f.connectionTests, f.calls, state)
	}
}

func TestProQualityConnectionRecheckHonorsChangesAndCancellation(t *testing.T) {
	for _, change := range []string{"policy", "binding", "stop", "cancel"} {
		t.Run(change, func(t *testing.T) {
			f := newProQualityFixture(t, "groups")
			f.setMode("error")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			f.connectionHook = func() {
				var err error
				switch change {
				case "policy":
					q := f.policy
					q.Enabled = false
					_, err = f.s.store.SaveProQualitySettings(q)
				case "binding":
					_, err = f.s.store.UpdateProAccount(f.email, func(p *model.MailAccountProfile) { p.Sub2AccountID = 99 })
				case "stop":
					_, err = f.s.store.UpdateProAccount(f.email, func(p *model.MailAccountProfile) { p.ProAuto.StopRequested = true })
				case "cancel":
					cancel()
				}
				if err != nil {
					t.Error(err)
				}
			}
			_ = f.s.runProQualityBatch(ctx, true)
			_ = f.s.runProQualityBatch(ctx, true)
			if f.writes != 0 || len(f.calls) != 0 || f.profile(t).ProQuality.ActionStarted {
				t.Fatal("changed or cancelled account was handled")
			}
		})
	}
}

func TestProQualityConfirmedDegradationDoesNotUseConnectionOverride(t *testing.T) {
	f := newProQualityFixture(t, "groups")
	f.setConnectionMode("normal")
	f.run(t)
	if f.connectionTests != 0 || f.writes != 1 || !f.profile(t).ProQuality.Degraded {
		t.Fatal("confirmed quality degradation was treated as a detection error")
	}
}

func TestProQualityQueuedConnectionCheckHonorsStop(t *testing.T) {
	f := newProQualityFixture(t, "merge")
	f.setMode("error")
	f.connectionHook = func() {
		f.mu.Lock()
		count := f.connectionTests
		f.mu.Unlock()
		if count == 2 {
			_, err := f.s.store.UpdateProAccount(f.email, func(p *model.MailAccountProfile) { p.ProAuto.StopRequested = true })
			if err != nil {
				t.Error(err)
			}
		}
	}
	_ = f.s.runProQualityBatch(context.Background(), true)
	_ = f.s.runProQualityBatch(context.Background(), true)
	f.s.proMergeWG.Wait()
	if f.connectionTests != 2 || len(f.calls) != 0 || f.profile(t).ProQuality.ActionStarted {
		t.Fatal("stop during queued connection check did not prevent handling")
	}
}

func TestProQualityConnectionRecoveryExpiresAndPolicyResets(t *testing.T) {
	for _, change := range []string{"expired", "policy"} {
		t.Run(change, func(t *testing.T) {
			f := newProQualityFixture(t, "groups")
			f.setMode("probe_error")
			f.setConnectionMode("normal")
			f.run(t)
			f.run(t)
			if f.profile(t).ProQuality.Status != "normal" {
				t.Fatal("setup did not recover")
			}
			_, err := f.s.store.UpdateProAccount(f.email, func(p *model.MailAccountProfile) {
				if change == "expired" {
					p.ProQuality.ConnectionCheck.CheckedAt = time.Now().Add(-time.Hour)
				} else {
					p.ProQuality.Revision = "previous-policy"
				}
			})
			if err != nil {
				t.Fatal(err)
			}
			f.run(t)
			state := f.profile(t).ProQuality
			if state.Status == "normal" || f.connectionTests != 1 || f.writes != 0 {
				t.Fatalf("old recovery reused after %s: %+v", change, state)
			}
		})
	}
}
