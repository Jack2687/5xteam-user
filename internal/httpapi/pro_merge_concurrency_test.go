package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"chatgpt-space-merge/internal/model"
)

type proMergeGate struct {
	team, stage string
	release     chan struct{}
}

func proMergeRunningCount(s *Server) int {
	s.proMergeMu.Lock()
	defer s.proMergeMu.Unlock()
	return s.proMergeRunning
}

func waitProMergeCondition(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("merge condition did not become true")
}

func queueProMergeAutomation(t *testing.T, s *Server, email string) {
	t.Helper()
	past := time.Now().Add(-time.Minute)
	_, err := s.store.UpdateProAccount(email, func(p *model.MailAccountProfile) {
		p.ProAuto = model.ProAutoState{ID: "auto-" + email, Status: "waiting_quota", NextCheckAt: &past, Steps: map[string]string{"oauth": "completed", "push": "completed", "quota": "completed"}}
	})
	if err != nil {
		t.Fatal(err)
	}
	s.queueDueProAutomation(email)
}

// Block real workflow HTTP steps, not a replacement Merge hook. Automatic and
// manual jobs must share a limit for the whole workflow, including removal.
func TestProMergeWholeWorkflowConcurrency(t *testing.T) {
	for _, tc := range []struct {
		sameTeam bool
		limit    int
	}{{false, 2}, {true, 2}, {false, 1}, {true, 1}} {
		sameTeam, serial := tc.sameTeam, tc.sameTeam || tc.limit == 1
		t.Run(fmt.Sprintf("same_team_%t_merge_limit_%d", sameTeam, tc.limit), func(t *testing.T) {
			f := newProExecutionFixture(t)
			events := make(chan proMergeGate, 20)
			abort := make(chan struct{})
			remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				stage := "invite"
				switch {
				case r.Method == "DELETE":
					stage = "remove"
				case strings.HasSuffix(r.URL.Path, "/invites/accept"):
					stage = "accept"
				case r.URL.Path == "/accounts/transfer":
					stage = "transfer"
				case !strings.HasSuffix(r.URL.Path, "/invites"):
					t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
					http.Error(w, "unexpected", 400)
					return
				}
				gate := proMergeGate{team: r.Header.Get("chatgpt-account-id"), stage: stage, release: make(chan struct{})}
				events <- gate
				select {
				case <-gate.release:
				case <-abort:
				case <-r.Context().Done():
					return
				}
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, "{\"success\":true}")
			}))
			t.Cleanup(func() { close(abort); f.s.stopProAutomations(); f.s.stopProMergeJobs(); remote.Close() })
			settings := f.s.store.Settings()
			settings.BaseURL, settings.ProxyURL = remote.URL, remote.URL
			if err := f.s.store.SaveSettings(settings); err != nil {
				t.Fatal(err)
			}
			admins := make([]model.AdminAccountProfile, 3)
			for i := range admins {
				team := fmt.Sprintf("team-%d", i)
				if sameTeam {
					team = "same-team"
				}
				admins[i] = saveTestAdmin(t, f.s.store, model.AdminAccountProfile{Label: fmt.Sprintf("mother-%d", i), TeamAccountID: team}, "mother-token", remote.URL)
			}
			cfg, _, _, _ := f.s.store.ProSettings()
			cfg.Concurrency, cfg.MergeConcurrency, cfg.TargetAdminID = 3, tc.limit, admins[0].ID
			if _, err := f.s.store.SaveProSettings(cfg, "", ""); err != nil {
				t.Fatal(err)
			}
			emails := []string{"first@example.com", "second@example.com", "third@example.com"}
			for i, email := range emails {
				if _, err := f.s.store.SaveMailAccount(model.MailAccountProfile{Email: email}, model.MailAccountCredentials{Email: email, AccessToken: "child-token", RefreshToken: "child-rt"}); err != nil {
					t.Fatal(err)
				}
				if _, err := f.s.store.UpdateMailAccountManagementScope(email, "pro"); err != nil {
					t.Fatal(err)
				}
				if _, err := f.s.store.UpdateProAccount(email, func(p *model.MailAccountProfile) {
					p.OAuthUserID, p.OAuthAccountID = "pro-user", "personal"
					p.PushStatus, p.PushProvider = "completed", "sub2"
					p.TargetAdminID, p.TargetTeamID = admins[i].ID, admins[i].TeamAccountID
					// A retry is pinned to its own mother, independent of current defaults.
					p.InviteStatus = "failed"
				}); err != nil {
					t.Fatal(err)
				}
			}
			next := func(team, stage string) proMergeGate {
				t.Helper()
				select {
				case event := <-events:
					if event.team != team || event.stage != stage {
						t.Fatalf("got %s/%s; want %s/%s", event.team, event.stage, team, stage)
					}
					return event
				case <-time.After(5 * time.Second):
					t.Fatalf("no %s/%s request", team, stage)
				}
				return proMergeGate{}
			}
			quiet := func() {
				t.Helper()
				select {
				case event := <-events:
					t.Fatalf("workflow exceeded concurrency/Team limit: %s/%s", event.team, event.stage)
				case <-time.After(150 * time.Millisecond):
				}
			}
			queueProMergeAutomation(t, f.s, emails[0])
			first := next(admins[0].TeamAccountID, "invite")
			queueProMergeAutomation(t, f.s, emails[1])
			wantRunning := 2
			if serial {
				wantRunning = 1
			}
			waitProMergeCondition(t, func() bool { return proMergeRunningCount(f.s) == wantRunning })
			var second proMergeGate
			if !serial {
				second = next(admins[1].TeamAccountID, "invite")
			}
			if _, err := f.s.startProMergeJob(emails[2], nil); err != nil {
				t.Fatal(err)
			}
			quiet()
			for _, stage := range []string{"accept", "transfer", "remove"} {
				close(first.release)
				first = next(admins[0].TeamAccountID, stage)
				quiet()
				if count := proMergeRunningCount(f.s); count != wantRunning {
					t.Fatalf("released slot during %s: %d", stage, count)
				}
			}
			close(first.release)
			if p := waitProAutoFinished(t, f.s, emails[0]); p.ProAuto.Status != "completed" {
				t.Fatal(p.ProAuto)
			}
			// A different Team can enter while the second is blocked. With one Team,
			// each remaining four-step sequence must instead execute serially.
			if !serial {
				third := next(admins[2].TeamAccountID, "invite")
				close(second.release)
				close(third.release)
				for range 6 {
					select {
					case event := <-events:
						close(event.release)
					case <-time.After(5 * time.Second):
						t.Fatal("remaining workflows stalled")
					}
				}
			} else {
				for range 2 {
					var event proMergeGate
					select {
					case event = <-events:
						if event.stage != "invite" {
							t.Fatalf("expected invite, got %+v", event)
						}
					case <-time.After(5 * time.Second):
						t.Fatal("queued workflow stalled")
					}
					team := event.team
					quiet()
					for _, stage := range []string{"accept", "transfer", "remove"} {
						close(event.release)
						event = next(team, stage)
						quiet()
					}
					close(event.release)
				}
			}
			f.waitMerge(t)
			if p := waitProAutoFinished(t, f.s, emails[1]); p.ProAuto.Status != "completed" {
				t.Fatal(p.ProAuto)
			}
			if count := proMergeRunningCount(f.s); count != 0 {
				t.Fatalf("leaked slots: %d", count)
			}
			for _, email := range emails {
				p, _, err := f.s.store.MailAccountCredential(email)
				if err != nil || p.RemoveStatus != "completed" {
					t.Fatalf("unfinished %s: %v %s", email, err, p.ProLastError)
				}
			}
		})
	}
}

func TestProMergeSlotIncludesDownstreamAndFailure(t *testing.T) {
	for _, automatic := range []bool{false, true} {
		for _, fail := range []bool{false, true} {
			t.Run(fmt.Sprintf("automatic_%t_fail_%t", automatic, fail), func(t *testing.T) {
				f := newProGroupFixture(t)
				f.entered, f.release = make(chan struct{}), make(chan struct{})
				f.fail.Store(fail)
				var once sync.Once
				defer once.Do(func() { close(f.release) })
				cfg, _, _, _ := f.s.store.ProSettings()
				cfg.Concurrency, cfg.MergeConcurrency = 3, 1
				if _, err := f.s.store.SaveProSettings(cfg, "", ""); err != nil {
					t.Fatal(err)
				}
				if automatic {
					queueProMergeAutomation(t, f.s, f.email)
				} else if _, err := f.s.startProMergeJob(f.email, nil); err != nil {
					t.Fatal(err)
				}
				select {
				case <-f.entered:
				case <-time.After(5 * time.Second):
					t.Fatal("downstream update not reached")
				}
				ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
				defer cancel()
				if release, err := f.s.proMergeSlot(ctx); err == nil {
					release()
					t.Fatal("slot released before downstream completion")
				}
				once.Do(func() { close(f.release) })
				if automatic {
					waitProAutoFinished(t, f.s, f.email)
				} else {
					f.waitMerge(t)
				}
				if count := proMergeRunningCount(f.s); count != 0 {
					t.Fatalf("slot leaked after completion/failure: %d", count)
				}
				p, _, err := f.s.store.MailAccountCredential(f.email)
				if err != nil || p.RemoveStatus != "completed" || (p.ProPostMergeGroups.Status == "failed") != fail {
					t.Fatal("unexpected downstream result", err, p.ProLastError)
				}
			})
		}
	}
}

func TestProMergeMonitorCleanupSharesGlobalSlot(t *testing.T) {
	f := newProExecutionFixture(t)
	cfg, _, _, _ := f.s.store.ProSettings()
	cfg.Concurrency, cfg.MergeConcurrency, cfg.QuotaEnabled = 3, 1, true
	if _, err := f.s.store.SaveProSettings(cfg, "", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.store.UpdateProAccount(f.email, func(p *model.MailAccountProfile) {
		p.PushProvider = "sub2"
		p.InviteStatus, p.AcceptStatus, p.TransferStatus = "completed", "completed", "completed"
		p.SpaceMergedOnce, p.RemoveStatus = true, "failed"
	}); err != nil {
		t.Fatal(err)
	}
	release, err := f.s.proMergeSlot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	release = sync.OnceFunc(release)
	defer release()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { defer close(done); f.s.monitorProAccounts(ctx) }()
	defer func() {
		cancel()
		release()
		<-done
		waitProMergeCondition(t, func() bool { f.s.proMonitorMu.Lock(); defer f.s.proMonitorMu.Unlock(); return !f.s.proMonitorRunning })
	}()
	waitProMergeCondition(t, func() bool { cfg, _, _, _ := f.s.store.ProSettings(); return cfg.LastQuotaSweepAt != nil })
	time.Sleep(200 * time.Millisecond)
	f.mu.Lock()
	calls := f.calls["remove"]
	f.mu.Unlock()
	if calls != 0 {
		t.Fatal("monitor bypassed the occupied global slot")
	}
	release()
	waitProMergeCondition(t, func() bool { p, _, _ := f.s.store.MailAccountCredential(f.email); return p.RemoveStatus == "completed" })
	waitProMergeCondition(t, func() bool { return proMergeRunningCount(f.s) == 0 })
}

func TestProMergeAndAutomationLimitsAreIndependent(t *testing.T) {
	f := newProExecutionFixture(t)
	cfg, _, _, _ := f.s.store.ProSettings()
	cfg.Concurrency, cfg.MergeConcurrency = 1, 2
	if _, err := f.s.store.SaveProSettings(cfg, "", ""); err != nil {
		t.Fatal(err)
	}
	autoRelease, err := f.s.proAutoSlot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer autoRelease()
	for range 2 {
		release, err := f.s.proMergeSlot(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		defer release()
	}
	for _, acquire := range []func(context.Context) (func(), error){f.s.proAutoSlot, f.s.proMergeSlot} {
		ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
		release, err := acquire(ctx)
		cancel()
		if err == nil {
			release()
			t.Fatal("independent concurrency bound exceeded")
		}
	}
	autoRelease()
	// Releasing the chain cannot release any merge slots, and a full merge
	// pool cannot prevent a new chain from starting its login.
	if proMergeRunningCount(f.s) != 2 {
		t.Fatal("chain release affected merge pool")
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	release, err := f.s.proAutoSlot(ctx)
	if err != nil {
		t.Fatal("merge pool blocked login chain", err)
	}
	release()
}

func TestProMergeQueuedSlotUsesUpdatedLimit(t *testing.T) {
	f := newProExecutionFixture(t)
	cfg, _, _, _ := f.s.store.ProSettings()
	cfg.MergeConcurrency = 1
	if _, err := f.s.store.SaveProSettings(cfg, "", ""); err != nil {
		t.Fatal(err)
	}
	first, err := f.s.proMergeSlot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer first()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		release, err := f.s.proMergeSlot(ctx)
		if err == nil {
			release()
		}
		done <- err
	}()
	select {
	case err := <-done:
		t.Fatal("waiter did not wait", err)
	case <-time.After(100 * time.Millisecond):
	}
	cfg.MergeConcurrency = 2
	if _, err := f.s.store.SaveProSettings(cfg, "", ""); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal("queued task kept stale limit", err)
	}
}
