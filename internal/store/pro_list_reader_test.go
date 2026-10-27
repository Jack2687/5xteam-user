package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestProListReaderConcurrentSnapshot(t *testing.T) {
	s := transferStore(t)
	transferSource(t, s, "first@example.com")
	reader, err := s.ProListReader(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	_, total, _, err := reader.ProAccountsPageContext(t.Context(), "", "", 10, 0)
	if err != nil || total != 1 {
		t.Fatal(total, err)
	}
	// A slow writer holding the shared store lock cannot hold up the list.
	s.mu.Lock()
	done := make(chan error, 1)
	go func() { _, _, _, e := reader.ProAccountsPageContext(t.Context(), "", "", 10, 0); done <- e }()
	select {
	case err = <-done:
		s.mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		s.mu.Unlock()
		t.Fatal("list waited for the main store mutex")
	}
	transferSource(t, s, "second@example.com")
	_, total, _, err = reader.ProAccountsPageContext(t.Context(), "", "", 10, 0)
	if err != nil || total != 1 {
		t.Fatal("snapshot changed mid-request", total, err)
	}
	if _, err = reader.db.Exec("DELETE FROM mail_accounts"); err == nil {
		t.Fatal("read-only list allowed a write")
	}
	next, err := s.ProListReader(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()
	_, total, _, err = next.ProAccountsPageContext(t.Context(), "", "", 10, 0)
	if err != nil || total != 2 {
		t.Fatal("next request retained stale snapshot", total, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if canceled, e := s.ProListReader(ctx); !errors.Is(e, context.Canceled) {
		if canceled != nil {
			canceled.Close()
		}
		t.Fatal(e)
	}
}
