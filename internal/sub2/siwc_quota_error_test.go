package sub2

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"chatgpt-space-merge/internal/model"
)

func TestSIWCQuotaPreservesFirstLookupError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/auth/login":
			fmt.Fprint(w, `{"code":0,"data":{"access_token":"fixture"}}`)
		case "/api/v1/admin/siwc/accounts/3/usage":
			fmt.Fprint(w, `{"code":0,"data":{"checked_at":"0001-01-01T00:00:00Z","error":"SIWC 网页额度 HTTP 401；保留上次结果"}}`)
		default:
			t.Error("unexpected endpoint", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	_, err := New().QueryQuota(context.Background(), model.Sub2Settings{URL: server.URL, Email: "admin@example.test", AuthMode: "siwc"}, "fixture", 3)
	if err == nil || !strings.Contains(err.Error(), "HTTP 401") || strings.Contains(err.Error(), "过期") {
		t.Fatal("upstream error masked", err)
	}
}
