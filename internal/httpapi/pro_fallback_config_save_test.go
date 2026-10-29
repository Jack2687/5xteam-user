package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestFallbackConfigSaveDuringCollection(t *testing.T) {
	for _, kind := range []string{"restock", "fallback"} {
		t.Run(kind, func(t *testing.T) {
			s, st, m, _ := fallbackFixture(t)
			entered, release := make(chan struct{}), make(chan struct{})
			var once, unblock sync.Once
			defer unblock.Do(func() { close(release) })
			m.before = func(r *http.Request) {
				if r.URL.Path == "/api/v1/admin/accounts" && r.URL.Query().Get("group") == "9" {
					once.Do(func() { close(entered); <-release })
				}
			}
			ticked := make(chan error, 1)
			go func() { ticked <- s.tickProFallback(context.Background(), true) }()
			select {
			case <-entered:
			case <-time.After(3 * time.Second):
				t.Fatal("monitor did not start")
			}
			var payload []byte
			var save http.HandlerFunc
			if kind == "restock" {
				c, _ := st.ProRestockConfig()
				c.Enabled = false
				c.MoneyWindowMinutes = 7
				payload, _ = json.Marshal(c)
				save = s.saveProRestockConfig
			} else {
				f, _, _ := st.ProFallbackConfig()
				f.Enabled = false
				payload, _ = json.Marshal(fallbackInput{FallbackConfig: f})
				save = s.saveProFallback
			}
			call := func() *httptest.ResponseRecorder {
				w := httptest.NewRecorder()
				save(w, httptest.NewRequest(http.MethodPut, "/", bytes.NewReader(payload)))
				return w
			}
			saved := make(chan *httptest.ResponseRecorder, 1)
			go func() { saved <- call() }()
			select {
			case w := <-saved:
				if w.Code != 200 {
					t.Fatalf("save while monitoring: %d %s", w.Code, w.Body.String())
				}
			case <-time.After(2 * time.Second):
				t.Fatal("slow monitor blocked config save")
			}
			if w := call(); w.Code != 409 {
				t.Fatalf("stale config must still be rejected: %d", w.Code)
			}
			unblock.Do(func() { close(release) })
			if err := <-ticked; err == nil {
				t.Fatal("obsolete monitoring round was not discarded")
			}
			if m.writes[1] != 0 || m.writes[2] != 0 {
				t.Fatal("stale round changed upstream scheduling")
			}
			// Version changes bypass the old cooldown; no unsynchronized next writes.
			if err := s.tickProFallback(context.Background(), false); err != nil {
				t.Fatal(err)
			}
			c, _ := st.ProRestockConfig()
			f, _, _ := st.ProFallbackConfig()
			if s.proFallback.configVersion != c.Version || s.proFallback.fallbackVersion != f.Version {
				t.Fatal("new config did not trigger next round")
			}
		})
	}
}

func TestFallbackConfigSaveWaitsForSubmittedScheduling(t *testing.T) {
	s, st, m, _ := fallbackFixture(t)
	f, target, _ := st.ProFallbackConfig()
	f.AccountIDs = []int64{1}
	f, err := st.SaveProFallbackConfig(f, target)
	if err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	var once, unblock sync.Once
	defer unblock.Do(func() { close(release) })
	m.before = func(r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/api/v1/admin/accounts/1/schedulable" {
			once.Do(func() { close(entered); <-release })
		}
	}
	ticked := make(chan error, 1)
	go func() { ticked <- s.tickProFallback(context.Background(), true) }()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("scheduling did not start")
	}
	f.Enabled = false
	raw, _ := json.Marshal(fallbackInput{FallbackConfig: f})
	saved := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		w := httptest.NewRecorder()
		s.saveProFallback(w, httptest.NewRequest(http.MethodPut, "/", bytes.NewReader(raw)))
		saved <- w
	}()
	select {
	case w := <-saved:
		t.Fatalf("save raced submitted scheduling: %d", w.Code)
	case <-time.After(100 * time.Millisecond):
	}
	unblock.Do(func() { close(release) })
	if err := <-ticked; err != nil {
		t.Fatal(err)
	}
	select {
	case w := <-saved:
		if w.Code != 200 {
			t.Fatal(w.Body.String())
		}
	case <-time.After(2 * time.Second):
		t.Fatal("save did not finish")
	}
	if !m.on[1] || m.writes[1] != 1 {
		t.Fatal("submitted result was lost")
	}
	if err := s.tickProFallback(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if m.on[1] || m.writes[1] != 2 {
		t.Fatal("new config did not drain previous account")
	}
}
