package sub2

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"chapt-space-user/internal/model"
)

func TestMonitorAccountsRequiresExplicitCompleteList(t *testing.T) {
	for _, tc := range []struct {
		name, data string
		ok         bool
	}{
		{"missing", `{"total":0}`, false},
		{"null", `{"total":0,"items":null}`, false},
		{"negative_total", `{"total":-1,"items":[]}`, false},
		{"empty", `{"total":0,"items":[]}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				data := tc.data
				if r.URL.Path == "/api/v1/auth/login" {
					data = `{"access_token":"fixture"}`
				}
				fmt.Fprintf(w, `{"code":0,"data":%s}`, data)
			}))
			defer up.Close()
			_, err := New().MonitorAccounts(context.Background(), model.Sub2Settings{URL: up.URL, Email: "test@example.com"}, "fixture", 1)
			if (err == nil) != tc.ok {
				t.Fatalf("complete=%v error=%v", tc.ok, err)
			}
		})
	}
}

func TestMonitorAccountsRejectsChangingTotal(t *testing.T) {
	for _, changed := range []bool{false, true} {
		up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/api/v1/auth/login" {
				fmt.Fprint(w, `{"code":0,"data":{"access_token":"fixture"}}`)
				return
			}
			total, id := 2, 1
			if r.URL.Query().Get("page") == "2" {
				id = 2
			} else if changed {
				total = 3
			}
			fmt.Fprintf(w, `{"code":0,"data":{"total":%d,"items":[{"id":%d,"group_ids":[1]}]}}`, total, id)
		}))
		rows, err := New().MonitorAccounts(context.Background(), model.Sub2Settings{URL: up.URL, Email: "test@example.com"}, "fixture", 1)
		up.Close()
		if (err != nil) != changed || (!changed && len(rows) != 2) {
			t.Fatalf("changed=%v rows=%v err=%v", changed, rows, err)
		}
	}
}

func TestMonitorConcurrencyRequiresExplicitAccountMap(t *testing.T) {
	for _, tc := range []struct {
		data string
		ok   bool
	}{
		{`{"enabled":true}`, false},
		{`{"enabled":true,"account":null}`, false},
		{`{"enabled":true,"account":{}}`, true},
	} {
		up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			data := tc.data
			if r.URL.Path == "/api/v1/auth/login" {
				data = `{"access_token":"fixture"}`
			} else if r.URL.Query().Get("strict") != "true" {
				t.Error("missing strict quality request")
			}
			fmt.Fprintf(w, `{"code":0,"data":%s}`, data)
		}))
		_, err := New().MonitorConcurrency(context.Background(), model.Sub2Settings{URL: up.URL, Email: "test@example.com"}, "fixture", 1)
		up.Close()
		if (err == nil) != tc.ok {
			t.Fatalf("data=%s accepted=%v error=%v", tc.data, tc.ok, err)
		}
	}
}
