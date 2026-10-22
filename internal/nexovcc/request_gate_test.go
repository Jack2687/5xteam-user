package nexovcc

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"
)

func TestRequestGateStopsInflightAndOldEpoch(t *testing.T) {
	g := NewRequestGate(false)
	old, finish, err := g.Bind(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer finish()
	started := make(chan struct{})
	var calls atomic.Int32
	c := New(Credentials{}, func(ctx context.Context, _ Request) (Response, error) {
		calls.Add(1)
		close(started)
		<-ctx.Done()
		return Response{}, ctx.Err()
	})
	c.cookie = "fixture"
	c.SetRequestGate(g)
	done := make(chan error, 1)
	go func() { _, err := c.Do(old, "/api/card/userCardWithdraw", nil); done <- err }()
	<-started
	g.Stop()
	select {
	case err := <-done:
		if !IsUnknown(err) {
			t.Fatal("submitted withdrawal lost uncertain status", err)
		}
	case <-time.After(time.Second):
		t.Fatal("transport not canceled")
	}
	if _, err := c.Do(context.Background(), "/api/getBalanceInfo", nil); err == nil {
		t.Fatal("pause bypassed")
	}
	g.Resume()
	if _, err := c.Do(old, "/api/getBalanceInfo", nil); err == nil {
		t.Fatal("old epoch revived")
	}
	if calls.Load() != 1 {
		t.Fatal("canceled operation replayed")
	}
	if _, finish, err := g.Bind(context.Background()); err != nil {
		t.Fatal(err)
	} else {
		finish()
	}
}

func TestRequestGateStopsHTTPRetryWait(t *testing.T) {
	for _, status := range []int{500, 522} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			g := NewRequestGate(false)
			var calls atomic.Int32
			waiting := make(chan struct{})
			c := New(Credentials{}, func(context.Context, Request) (Response, error) { calls.Add(1); return Response{Status: status}, nil })
			c.cookie = "fixture"
			c.SetRequestGate(g)
			c.http522Retries = 3
			c.http522Interval = time.Minute
			c.retryProgress = func(string) { close(waiting) }
			done := make(chan error, 1)
			go func() { _, err := c.Do(context.Background(), "/api/getBalanceInfo", nil); done <- err }()
			<-waiting
			g.Stop()
			select {
			case err := <-done:
				if err == nil {
					t.Fatal("retry unexpectedly succeeded")
				}
			case <-time.After(time.Second):
				t.Fatal("retry wait not canceled")
			}
			g.Resume()
			if calls.Load() != 1 {
				t.Fatal("retried after stop")
			}
		})
	}
}
