package httpapi

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"chatgpt-space-merge/internal/gptpay"
	"chatgpt-space-merge/internal/model"
)

// Run the production worker from login, with only external session/push/quota
// stubbed. The real four-step merge must wait without serializing login.
func TestProMultiChainSingleMergeProgress(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(fmt.Sprintf("merge_failure_%t", fail), func(t *testing.T) {
			f := newProExecutionFixture(t)
			cfg, _, _, _ := f.s.store.ProSettings()
			cfg.Concurrency, cfg.MergeConcurrency = 3, 1
			if _, err := f.s.store.SaveProSettings(cfg, "", ""); err != nil {
				t.Fatal(err)
			}
			if fail {
				f.failStage, f.failCount = "invite", 1
			}
			hold, err := f.s.proMergeSlot(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer hold()
			logins, quotas := make(chan string, 4), make(chan string, 4)
			allowLogin := make(chan struct{})
			allow := sync.OnceFunc(func() { close(allowLogin) })
			t.Cleanup(func() { allow(); hold(); f.s.stopProAutomations() })
			f.s.proAutoHooks = &proAutomationHooks{
				Session: func(ctx context.Context, email string, _ func(string, map[string]any) (any, error)) error {
					logins <- email
					select {
					case <-allowLogin:
					case <-ctx.Done():
						return ctx.Err()
					}
					for _, stage := range []string{"login", "recharge", "oauth"} {
						if err := f.s.autoProUpdate(email, stage, "completed", ""); err != nil {
							return err
						}
					}
					return nil
				},
				Push: func(_ context.Context, email string) (model.MailAccountProfile, error) {
					return f.s.store.UpdateProAccount(email, func(p *model.MailAccountProfile) { p.PushStatus, p.PushProvider = "completed", "sub2" })
				},
				Quota: func(_ context.Context, email string, _ bool) (model.MailAccountProfile, error) {
					p, err := f.s.store.UpdateProAccount(email, func(p *model.MailAccountProfile) { p.Quota7D = &model.FreeQuotaWindow{UsedPercent: 100} })
					quotas <- email
					return p, err
				},
			}
			emails := []string{"chain-0@example.com", "chain-1@example.com", "chain-2@example.com", "chain-3@example.com"}
			cancels := map[string]context.CancelFunc{}
			for _, email := range emails {
				if _, err := f.s.store.SaveMailAccount(model.MailAccountProfile{Email: email}, model.MailAccountCredentials{Email: email, AccessToken: "child-token", RefreshToken: "child-rt"}); err != nil {
					t.Fatal(err)
				}
				if _, err := f.s.store.UpdateMailAccountManagementScope(email, "pro"); err != nil {
					t.Fatal(err)
				}
				if _, err := f.s.store.UpdateProAccount(email, func(p *model.MailAccountProfile) {
					p.OAuthUserID, p.OAuthAccountID = "pro-user", "personal"
					p.ProAuto = model.ProAutoState{ID: email, Status: "running", QuotaUsedThreshold: 100, Steps: map[string]string{}}
				}); err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithCancel(t.Context())
				cancels[email] = cancel
				unlock := f.s.lockProAccount(email)
				f.s.proAutoMu.Lock()
				if f.s.proAutoJobs == nil {
					f.s.proAutoJobs = map[string]context.CancelFunc{}
				}
				f.s.proAutoJobs[email] = cancel
				f.s.proAutoWG.Add(1)
				f.s.proAutoMu.Unlock()
				go f.s.runProAutomation(ctx, email, unlock, cancel, false, gptpay.Settings{SessionTimeoutMinutes: 1}, "", gptpay.Card{}, gptpay.CardSecret{})
			}
			receive := func(ch <-chan string) string {
				t.Helper()
				select {
				case email := <-ch:
					return email
				case <-time.After(5 * time.Second):
					t.Fatal("chain failed to progress")
					return ""
				}
			}
			active := []string{receive(logins), receive(logins), receive(logins)}
			select {
			case email := <-logins:
				t.Fatalf("chain limit exceeded: %s", email)
			case <-time.After(150 * time.Millisecond):
			}
			allow()
			for range 3 {
				receive(quotas)
			}
			waitProMergeCondition(t, func() bool { f.s.proMergeMu.Lock(); defer f.s.proMergeMu.Unlock(); return f.s.proAutoRunning == 3 })
			if proMergeRunningCount(f.s) != 1 {
				t.Fatal("merge pool changed while held")
			}
			f.mu.Lock()
			calls := len(f.calls)
			f.mu.Unlock()
			if calls != 0 {
				t.Fatal("merge started while its sole slot was occupied")
			}
			// Cancel a merge waiter, then prove the fourth login can start even
			// while the one-slot merge pool is still occupied.
			cancelled := active[0]
			cancels[cancelled]()
			if p := waitProAutoFinished(t, f.s, cancelled); p.ProAuto.Status != "interrupted" {
				t.Fatal("queued cancellation not recorded", p.ProAuto)
			}
			receive(logins)
			receive(quotas)
			hold()
			completed, failed := 0, 0
			for _, email := range emails {
				if email == cancelled {
					continue
				}
				p := waitProAutoFinished(t, f.s, email)
				switch p.ProAuto.Status {
				case "completed":
					completed++
				case "failed":
					failed++
				default:
					t.Fatalf("stuck chain %s: %+v", email, p.ProAuto)
				}
			}
			wantFailed := 0
			if fail {
				wantFailed = 1
			}
			if completed != 3-wantFailed || failed != wantFailed {
				t.Fatalf("completed=%d failed=%d", completed, failed)
			}
			f.s.proMergeMu.Lock()
			autoCount, mergeCount := f.s.proAutoRunning, f.s.proMergeRunning
			f.s.proMergeMu.Unlock()
			if autoCount != 0 || mergeCount != 0 {
				t.Fatalf("leaked slots: chain=%d merge=%d", autoCount, mergeCount)
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			for _, stage := range []string{"invite", "accept", "transfer", "remove"} {
				want := 3 - wantFailed
				if stage == "invite" {
					want = 3
				}
				if f.calls[stage] != want {
					t.Fatalf("%s calls=%d want=%d", stage, f.calls[stage], want)
				}
			}
		})
	}
}
