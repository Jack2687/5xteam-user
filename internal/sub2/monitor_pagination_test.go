package sub2

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"chatgpt-space-merge/internal/model"
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

func TestMonitorAccountsUsesSub2GroupFilter(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/auth/login" {
			fmt.Fprint(w, `{"code":0,"data":{"access_token":"fixture"}}`)
			return
		}
		// The Sub2 accounts endpoint reads "group", unlike ops/concurrency.
		switch r.URL.Query().Get("group") {
		case "4":
			fmt.Fprint(w, `{"code":0,"data":{"total":0,"items":[]}}`)
		case "1":
			id := 1
			if r.URL.Query().Get("page") == "2" {
				id = 2
			}
			fmt.Fprintf(w, `{"code":0,"data":{"total":2,"items":[{"id":%d,"group_ids":[1]}]}}`, id)
		default:
			fmt.Fprint(w, `{"code":0,"data":{"total":1,"items":[{"id":99,"group_ids":[2]}]}}`)
		}
	}))
	defer up.Close()
	for _, tc := range []struct {
		name  string
		group int64
		want  int
	}{{"empty_group", 4, 0}, {"populated_group_pagination", 1, 2}} {
		t.Run(tc.name, func(t *testing.T) {
			rows, err := New().MonitorAccounts(context.Background(), model.Sub2Settings{URL: up.URL, Email: "test@example.com"}, "fixture", tc.group)
			if err != nil || len(rows) != tc.want {
				t.Fatalf("group=%d rows=%v want=%d err=%v", tc.group, rows, tc.want, err)
			}
		})
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
			} else if r.URL.Query().Has("strict") || r.URL.Query().Get("group_id") != "1" {
				t.Error("incorrect native monitoring query")
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

func TestMonitorLoadRequiresNativeCounters(t *testing.T) {
	for _, field := range []string{"account_id", "current_in_use", "waiting_in_queue", "max_capacity"} {
		for _, null := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/null=%v", field, null), func(t *testing.T) {
				load := map[string]any{"account_id": 1, "current_in_use": 0, "waiting_in_queue": 0, "max_capacity": 10}
				if null {
					load[field] = nil
				} else {
					delete(load, field)
				}
				raw, _ := json.Marshal(load)
				var result MonitorLoad
				if err := json.Unmarshal(raw, &result); err == nil {
					t.Fatal("missing counter accepted as zero")
				}
			})
		}
	}
}
