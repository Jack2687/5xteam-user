package httpapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"chatgpt-space-merge/internal/model"
	"chatgpt-space-merge/internal/nexovcc"
	"chatgpt-space-merge/internal/restock"
)

func restockImportFixture(t *testing.T) (*Server, *nexFixture, restock.Config, restock.Task, restock.Snapshot, *atomic.Int32) {
	t.Helper()
	purchases := new(atomic.Int32)
	s, st, _ := gptPayFixture(t, func(w http.ResponseWriter, r *http.Request) {
		purchases.Add(1)
		fmt.Fprint(w, `{"code":0,"data":{"id":"import-order","status":"success"}}`)
	})
	configureProAutoFixture(t, s)
	s.proAutoCancel()
	nex, f := nexFixtureServer(t)
	s.nexovcc.transport = nex.nexovcc.transport
	cfg, credentials, _ := nex.store.NexovccSettings()
	if _, err := st.SaveNexovccSettings(cfg, credentials, false); err != nil {
		t.Fatal(err)
	}
	p, _, _, _ := st.ProSettings()
	p.CardProvider, p.NexovccAutoOpen = "nexovcc", true
	p.NexovccTemplateID, p.NexovccDailyOpenLimit, p.NexovccDailyAmountLimit = "template", 5, "50.00"
	if _, err := st.SaveProSettings(p, "", ""); err != nil {
		t.Fatal(err)
	}
	c := restock.DefaultConfig()
	c.Enabled, c.Mode, c.GroupID = true, "auto", 1
	c, err := st.SaveProRestockConfig(c)
	if err != nil {
		t.Fatal(err)
	}
	pay, _, _ := st.GPTPaySettings()
	snap := restock.Snapshot{At: time.Now(), Valid: true, LoadValid: true, GroupID: 1, Binding: restockBinding(p), NewPlan: restockPlan(pay.Provider, pay.PlanCode, pay.Country, pay.CreditQuantity)}
	target, err := st.SaveDownstreamTarget(model.DownstreamTarget{Scope: "pro-restock", Provider: "sub2", Sub2: p.Sub2, Secret: "test-password"})
	if err != nil {
		t.Fatal(err)
	}
	tasks, err := st.ReserveProRestock(restock.Decision{ID: "card-import", At: time.Now(), Version: c.Version, Suggested: 1, Snapshot: snap}, target, 0)
	if err != nil || len(tasks) != 1 {
		t.Fatal(tasks, err)
	}
	s.proAutoHooks = &proAutomationHooks{
		Session: func(ctx context.Context, email string, rpc func(string, map[string]any) (any, error)) error {
			_, _ = st.UpdateProAccount(email, func(p *model.MailAccountProfile) { p.ProAuto.ExitIP = "198.51.100.8" })
			if _, err := rpc("pro_recharge", map[string]any{"exit_ip": "198.51.100.8", "session": map[string]any{"access_token": proAutoToken(email, "initial"), "user": map[string]any{"email": email}, "account": map[string]any{"id": "personal-id"}}}); err != nil {
				return err
			}
			_, err := rpc("pro_tokens", map[string]any{"exit_ip": "198.51.100.8", "tokens": map[string]any{"access_token": proAutoToken(email, "new"), "refresh_token": "new-rt"}})
			return err
		},
		Push: func(ctx context.Context, email string) (model.MailAccountProfile, error) {
			return st.UpdateProAccount(email, func(p *model.MailAccountProfile) {
				p.PushStatus, p.PushProvider, p.Sub2AccountID = "completed", "sub2", 88
			})
		},
		Quota: func(context.Context, string, bool) (model.MailAccountProfile, error) {
			return model.MailAccountProfile{}, errors.New("fixture quota waits")
		},
	}
	return s, f, c, tasks[0], snap, purchases
}

func seedRestockImport(t *testing.T, s *Server, task restock.Task) (restock.Task, nexovcc.Operation) {
	t.Helper()
	now := time.Now()
	task.Status, task.SubmittedAt = "submitting", &now
	if err := s.store.ClaimProRestockDispatch(task); err != nil {
		t.Fatal(err)
	}
	ctx := context.WithValue(context.Background(), proRestockContextKey{}, task.ID)
	op, err := s.openNexovccLocked(ctx, "nex-import-test1", "template", true)
	if err != nil {
		t.Fatal(err)
	}
	if op.RestockTaskID != task.ID {
		t.Fatal("issuance not bound to original task", op)
	}
	return task, op
}

func TestRestockIssuanceSurvivesEvaluateRequestCancellation(t *testing.T) {
	s, f, c, task, snap, purchases := restockImportFixture(t)
	parent, cancel := context.WithCancel(context.Background())
	defer cancel()
	transport := s.nexovcc.transport
	s.nexovcc.transport = func(ctx context.Context, r nexovcc.Request) (nexovcc.Response, error) {
		reply, err := transport(ctx, r)
		if r.Path == "/api/card/createUserCard" {
			cancel()
		}
		if ctx.Err() != nil {
			t.Error("page disconnect cancelled claimed card operation")
		}
		return reply, err
	}
	p, _, _, _ := s.store.ProSettings()
	s.dispatchProRestock(parent, c, p, snap)
	profile := waitProAutoFinished(t, s, task.Email)
	if parent.Err() == nil || profile.PushStatus != "completed" || profile.ProAuto.Status != "waiting_quota" || f.opens.Load() != 1 || purchases.Load() != 1 {
		t.Fatalf("did not finish original flow: %+v opens=%d purchases=%d", profile.ProAuto, f.opens.Load(), purchases.Load())
	}
}

func TestRestockDelayedCardImportResumesOriginalAccountAfterRestart(t *testing.T) {
	s, f, c, task, snap, purchases := restockImportFixture(t)
	f.failDetail = true
	task, op := seedRestockImport(t, s, task)
	if op.Status != "import_pending" {
		t.Fatal(op)
	}
	p, _, _, _ := s.store.ProSettings()
	tasks, _, err := s.restockTasks(snap, p, c)
	if err != nil || len(tasks) != 1 || tasks[0].Status != "waiting_card" || tasks[0].CardID != op.CardID || tasks[0].ETA != nil {
		t.Fatal(tasks, err)
	}
	// A fresh server runtime reads the persisted task/operation relationship.
	s.proRestock = proRestockRuntime{}
	s.nexovcc.client = nil
	task = tasks[0]
	task.NextCardCheckAt = nil
	if err := s.store.SaveProRestockTask(task); err != nil {
		t.Fatal(err)
	}
	f.failDetail = false
	transport := s.nexovcc.transport
	var detailReads atomic.Int32
	s.nexovcc.transport = func(ctx context.Context, r nexovcc.Request) (nexovcc.Response, error) {
		if r.Path == "/api/card/getUserCardDetail" {
			detailReads.Add(1)
			if r.Body["serial_no"] != op.Serial {
				t.Error("queried another card")
			}
		}
		if r.Path == "/api/card/findUserCardList" || r.Path == "/api/card/createUserCard" {
			t.Error("recovery queried all cards or opened another card")
		}
		return transport(ctx, r)
	}
	// Already issued work continues across a later forbidden time window.
	c.NoRestockEnabled = true
	now := time.Now().In(time.FixedZone("CST", 8*3600))
	c.NoRestockWindows = []restock.DailyWindow{{Start: now.Add(-time.Hour).Format("15:04"), End: now.Add(time.Hour).Format("15:04")}}
	s.continueProRestockCardImports(context.Background(), c, p)
	profile := waitProAutoFinished(t, s, task.Email)
	if profile.ProAuto.CardID != op.CardID || profile.PushStatus != "completed" || purchases.Load() != 1 || f.opens.Load() != 1 || detailReads.Load() != 1 {
		t.Fatalf("recovery failed: %+v purchases=%d reads=%d", profile.ProAuto, purchases.Load(), detailReads.Load())
	}
	for range 2 {
		s.continueProRestockCardImports(context.Background(), c, p)
	}
	snap.At = time.Now()
	snap.Accounts = []restock.Account{{ID: 88, Ready: true}}
	tasks, _, err = s.restockTasks(snap, p, c)
	if err != nil || tasks[0].Status != "ready" || tasks[0].ReadyAt == nil || purchases.Load() != 1 || f.opens.Load() != 1 {
		t.Fatal("duplicate or unfinished delivery", tasks, err)
	}
}

func TestRestockImportRecoveryGuards(t *testing.T) {
	for _, mode := range []string{"stopped", "paused", "issuer_disabled", "backoff", "occupied", "closed", "unlinked", "unknown", "already_started", "concurrency", "manual_concurrency"} {
		t.Run(mode, func(t *testing.T) {
			s, f, c, task, _, purchases := restockImportFixture(t)
			f.failDetail = mode != "occupied" && mode != "closed"
			task, op := seedRestockImport(t, s, task)
			p, _, _, _ := s.store.ProSettings()
			switch mode {
			case "stopped":
				_, _ = s.store.UpdateProAccount(task.Email, func(p *model.MailAccountProfile) { p.ProAuto.StopRequested = true })
			case "paused":
				c.Enabled = false
			case "issuer_disabled":
				cfg, _, _ := s.store.NexovccSettings()
				cfg.Enabled = false
				_, _ = s.store.SaveNexovccSettings(cfg, nexovcc.Credentials{}, false)
			case "backoff":
				future := time.Now().Add(time.Hour)
				task.NextCardCheckAt = &future
				_ = s.store.SaveProRestockTask(task)
			case "unlinked":
				op.RestockTaskID = "different-task"
				_ = s.store.SaveNexovccOperation(op)
			case "unknown":
				op.Serial, op.CardID, op.Status = "", "", "unknown"
				_ = s.store.SaveNexovccOperation(op)
			case "already_started":
				_, _ = s.store.UpdateProAccount(task.Email, func(p *model.MailAccountProfile) { p.ProAuto.ID = "already-started" })
			case "concurrency":
				p.Concurrency = 0
			case "manual_concurrency":
				s.proAutoRunning = p.Concurrency
			case "closed":
				card, _ := s.store.NexovccCard(op.CardID)
				card.Status = "4"
				_, _ = s.store.SaveNexovccCard(card, nil)
			case "occupied":
				allocationAccount(t, s, "other@example.com")
				if err := s.store.ReserveProCard("other@example.com", op.CardID, false, ""); err != nil {
					t.Fatal(err)
				}
				ctx := context.WithValue(context.Background(), proRestockContextKey{}, task.ID)
				if _, _, err := s.reserveProStartCard(ctx, task.Email, op.CardID, true, false); err == nil {
					t.Fatal("occupied original card replaced")
				}
			}
			s.nexovcc.client = nil
			s.nexovcc.transport = func(context.Context, nexovcc.Request) (nexovcc.Response, error) {
				t.Error("blocked recovery contacted supplier")
				return nexovcc.Response{}, errors.New("blocked")
			}
			s.continueProRestockCardImports(context.Background(), c, p)
			if purchases.Load() != 0 || f.opens.Load() != 1 || s.proAutomationRunning(task.Email) {
				t.Fatal("recovery bypassed guard")
			}
		})
	}
}

func TestRestockPreparationStopsWithServer(t *testing.T) {
	s := &Server{}
	parent, disconnect := context.WithCancel(context.Background())
	ctx, finish, err := s.beginRestockStart(parent, "task")
	if err != nil {
		t.Fatal(err)
	}
	disconnect()
	if ctx.Err() != nil {
		t.Fatal("page controls durable task")
	}
	stopped := make(chan struct{})
	go func() { s.stopProAutomations(); close(stopped) }()
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("shutdown did not cancel preparation")
	}
	finish()
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("shutdown did not join preparation")
	}
}

func TestRestockCompletedImportUsesCachedCardAndCannotIssueAgain(t *testing.T) {
	s, f, c, task, _, purchases := restockImportFixture(t)
	task, op := seedRestockImport(t, s, task)
	ctx := context.WithValue(context.Background(), proRestockContextKey{}, task.ID)
	if _, err := s.openNexovccLocked(ctx, "nex-import-test2", "template", true); err == nil || f.opens.Load() != 1 {
		t.Fatal("a second issuance was allowed for the same task", err)
	}
	s.nexovcc.client = nil
	s.nexovcc.transport = func(context.Context, nexovcc.Request) (nexovcc.Response, error) {
		t.Error("completed card was queried again")
		return nexovcc.Response{}, errors.New("unexpected request")
	}
	p, _, _, _ := s.store.ProSettings()
	s.continueProRestockCardImports(context.Background(), c, p)
	profile := waitProAutoFinished(t, s, task.Email)
	if profile.ProAuto.CardID != op.CardID || profile.PushStatus != "completed" || purchases.Load() != 1 {
		t.Fatal(profile.ProAuto, purchases.Load())
	}
}
