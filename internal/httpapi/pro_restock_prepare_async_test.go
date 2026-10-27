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
