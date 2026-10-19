package httpapi

import (
	"chatgpt-space-merge/internal/ejh"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestEJHSyncRejectsMissingLists(t *testing.T) {
	for _, kind := range []string{"cards_missing", "cards_null", "trades_missing", "trades_null", "empty_lists"} {
		t.Run(kind, func(t *testing.T) {
			s, _ := ejhTestServer(t, 200)
			var calls atomic.Int32
			up := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/v3/authenticate" {
					fmt.Fprint(w, `{"code":"000000","data":{"authToken":"local","validity":60000}}`)
					return
				}
				n := calls.Add(1)
				data := `{}`
				if n == 1 {
					if kind == "cards_null" {
						data = `{"cardInfos":null}`
					} else if kind != "cards_missing" {
						data = `{"cardInfos":[]}`
					}
				} else {
					if kind == "trades_null" {
						data = `{"tradeInfos":null}`
					} else if kind != "trades_missing" {
						data = `{"tradeInfos":[]}`
					}
				}
				fmt.Fprintf(w, `{"code":"000000","data":%s}`, data)
			}))
			defer up.Close()
			cfg, _, err := s.store.EJHSettings()
			if err != nil {
				t.Fatal(err)
			}
			cfg.URL = up.URL
			if _, err = s.store.SaveEJHSettings(cfg, "fixture-secret"); err != nil {
				t.Fatal(err)
			}
			if kind == "trades_missing" || kind == "trades_null" {
				if _, err = s.store.ImportEJHCard(ejh.Card{OrderNo: "test-card", Number: "4242424242424242", CVV: "123", Validity: "1299", Status: "1"}, false); err != nil {
					t.Fatal(err)
				}
			}
			s.ejh.client = ejh.New()
			s.ejh.client.HTTP = up.Client()
			err = s.syncEJHLocked(context.Background(), time.Now().Add(-time.Hour), time.Now())
			var state ejh.SyncState
			if readErr := s.store.EJHGet("sync", "latest", &state); readErr != nil {
				t.Fatal(readErr)
			}
			if kind == "empty_lists" {
				if err != nil || state.LastSuccess == nil {
					t.Fatal("valid empty lists rejected", err)
				}
			} else if err == nil || state.LastSuccess != nil || state.Error == "" {
				t.Fatalf("missing list recorded as successful sync: %v %+v", err, state)
			}
		})
	}
}
