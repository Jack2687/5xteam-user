package sub2

import (
	"chatgpt-space-merge/internal/model"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func TestFallbackAccountListPaginationTypesAndSecrets(t *testing.T) {
	pages := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/auth/login" {
			_, _ = w.Write([]byte(`{"code":0,"data":{"access_token":"token"}}`))
			return
		}
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/admin/accounts" {
			t.Error("unexpected mutation")
			http.NotFound(w, r)
			return
		}
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		pages++
		if r.URL.Query().Get("group") != "7" {
			t.Fatal("wrong group query")
		}
		items := []map[string]any{}
		for id := (page-1)*100 + 1; id <= min(page*100, 105); id++ {
			kind, platform := "oauth", "openai"
			if id%2 == 0 {
				kind = "apikey"
			}
			if id == 103 {
				kind = "setup-token"
			}
			if id == 104 {
				platform = "anthropic"
			}
			items = append(items, map[string]any{"id": id, "name": "account", "type": kind, "platform": platform, "schedulable": false, "group_ids": []int64{7}, "credentials": map[string]string{"api_key": "private-api-key", "refresh_token": "private-refresh"}})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{"items": items, "total": 105}})
	}))
	defer srv.Close()
	a, err := New().FallbackAccounts(context.Background(), model.Sub2Settings{URL: srv.URL, Email: "test@example.test"}, "secret", 7)
	if err != nil || len(a) != 103 || pages != 2 {
		t.Fatal(len(a), pages, err)
	}
	for _, v := range a {
		if !v.Supported() || !v.InGroup(7) || v.Schedulable == nil {
			t.Fatal(v)
		}
	}
	raw, _ := json.Marshal(a)
	if strings.Contains(string(raw), "private-") || strings.Contains(string(raw), "credentials") {
		t.Fatal("secret leaked from account selection")
	}
}
