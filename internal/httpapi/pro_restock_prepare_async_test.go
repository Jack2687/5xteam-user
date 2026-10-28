package httpapi

import (
	"context"
	"errors"
	"testing"
	"time"

	"chatgpt-space-merge/internal/model"
	"chatgpt-space-merge/internal/nexovcc"
	"chatgpt-space-merge/internal/restock"
)

func TestRestockMonitorContinuesDuringCardPreparation(t *testing.T) {
	s, _, c, task, snap, purchases := restockImportFixture(t)
	entered, release := make(chan struct{}), make(chan struct{})
	transport := s.nexovcc.transport
	s.nexovcc.transport = func(ctx context.Context, r nexovcc.Request) (nexovcc.Response, error) {
		if r.Path == "/api/card/createUserCard" {
			close(entered)
			select {
			case <-release:
			case <-ctx.Done():
				return nexovcc.Response{}, ctx.Err()
			}
			return nexovcc.Response{}, errors.New("fixture uncertain issuance")
		}
		return transport(ctx, r)
	}
	p, _, _, _ := s.store.ProSettings()
	s.proRestock.monitor = func(context.Context, restock.Config, model.ProSettings, string) (restock.Snapshot, error) {
		sample := snap
		sample.ID, sample.At = randomRegistrationID(), time.Now()
		return sample, nil
	}
	s.queueRestockPreparation(context.Background(), c, p, snap, false, true)
	t.Cleanup(func() { close(release); s.proAutoWG.Wait() })
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("preparation did not begin")
	}
	for i := 0; i < 2; i++ {
		before := s.proRestock.lastSnapshot.ID
		finished := make(chan error, 1)
		go func() { finished <- s.tickProRestock(context.Background(), true) }()
		select {
		case err := <-finished:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("monitor waited for issuer preparation")
		}
		if s.proRestock.lastSnapshot.ID == before || s.proRestock.monitorState.Running {
			t.Fatal("new monitoring sample was not published")
		}
		current, err := s.store.ProRestockTask(task.ID)
		if err != nil || current.Status != "submitting" {
			t.Fatalf("active preparation was overwritten: %+v %v", current, err)
		}
		if s.proRestock.decision.Started != 0 || purchases.Load() != 0 {
			t.Fatal("monitor duplicated work during preparation")
		}
	}
}

func TestEJHFailedPreparationReleasesBatchWithoutManualStop(t *testing.T) {
	s, _, c, task, snap, purchases := restockImportFixture(t)
	p, _, _, _ := s.store.ProSettings()
	c.CooldownSeconds, c.EmergencyCooldownSeconds = 1, 1
	c, _ = s.store.SaveProRestockConfig(c)
	// Replay the persisted failure left by an older monitor overwriting a claim.
	now := time.Now()
	task.Status, task.CardProvider, task.SubmittedAt = "attention", "ejh", &now
	task.Message = "补货任务尚未领取或卡台信息无效"
	if err := s.store.SaveProRestockTask(task); err != nil {
		t.Fatal(err)
	}
	// A live preparation must retain its ownership, including before a flow ID exists.
	s.proRestockStartJobs = map[string]context.CancelFunc{task.ID: func() {}}
	tasks, _, err := s.restockTasks(snap, p, c)
	if err != nil || len(tasks) != 1 || !tasks[0].Active() {
		t.Fatalf("live preparation released: %+v %v", tasks, err)
	}
	delete(s.proRestockStartJobs, task.ID)
	tasks, _, err = s.restockTasks(snap, p, c)
	if err != nil || len(tasks) != 1 || tasks[0].Status != "failed" || tasks[0].StoppedAt != nil {
		t.Fatalf("orphan still blocks queue: %+v %v", tasks, err)
	}
	if purchases.Load() != 0 {
		t.Fatal("cleanup submitted payment")
	}
	if restock.WaitForBatch(snap, tasks) == nil {
		t.Fatal("new monitoring barrier bypassed")
	}
	snap.At = time.Now()
	if restock.WaitForBatch(snap, tasks) != nil {
		t.Fatal("failed preparation still blocks refreshed queue")
	}
	time.Sleep(1100 * time.Millisecond) // configured cooldown must still be honored
	snap.At = time.Now()
	allocationAccount(t, s, "next-batch@example.com")
	reserved, err := s.store.ReserveProRestock(restock.Decision{ID: "next-after-prepare-failure", At: time.Now(), Version: c.Version, Suggested: 1, Snapshot: snap}, "", 0)
	if err != nil || len(reserved) != 1 || reserved[0].Email == task.Email {
		t.Fatalf("next batch blocked or original reclaimed: %+v %v", reserved, err)
	}
}

// Tests of async preparation must wait for the worker, not assume a monitor
// tick waits for supplier calls. No real supplier or payment is used here.
func waitRestockPreparationIdle(t *testing.T, s *Server) {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		s.proAutoMu.Lock()
		busy := len(s.proRestockStartJobs) > 0
		s.proAutoMu.Unlock()
		if !busy {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("card preparation worker did not finish")
}
