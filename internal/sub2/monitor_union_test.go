package sub2

import (
	"chatgpt-space-merge/internal/model"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestMonitorGroupsDeduplicatesSharedAccountAndRejectsPartial(t *testing.T) {
	broken := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		write := func(v any) { json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": v}) }
		switch r.URL.Path {
		case "/api/v1/auth/login":
			write(map[string]any{"access_token": "test"})
		case "/api/v1/admin/accounts":
			write(map[string]any{"total": 1, "items": []any{map[string]any{"id": 5, "group_ids": []int64{7, 42}, "concurrency": 80, "status": "active", "schedulable": true}}})
		case "/api/v1/admin/ops/concurrency":
			load := map[string]any{}
			if !broken || r.URL.Query().Get("group_id") != "42" {
				used := 20
				if r.URL.Query().Get("group_id") == "42" {
					used = 21
				}
				load["5"] = map[string]any{"account_id": 5, "current_in_use": used, "waiting_in_queue": 0, "max_capacity": 80}
			}
			write(map[string]any{"enabled": true, "timestamp": time.Now(), "account": load})
		default:
			t.Error("unexpected", r.URL.Path)
			fmt.Fprint(w, `{}`)
		}
	}))
	defer server.Close()
	client := New()
	cfg := model.Sub2Settings{URL: server.URL, Email: "test@example.test"}
	rows, loads, e := client.MonitorGroups(context.Background(), cfg, "test", []int64{7, 42, 7})
	if e != nil || len(rows) != 1 || len(loads.Account) != 1 || loads.Account[5].Used != 21 {
		t.Fatal("duplicated shared capacity/load", rows, loads, e)
	}
	broken = true
	if _, _, e = client.MonitorGroups(context.Background(), cfg, "test", []int64{7, 42}); e == nil {
		t.Fatal("partial group accepted")
	}
}
