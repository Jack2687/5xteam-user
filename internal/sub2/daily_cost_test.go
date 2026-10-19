package sub2

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"chatgpt-space-merge/internal/model"
)

func TestDailyUserCostUsesBeijingUserDeductions(t *testing.T) {
	response := `{"total_actual_cost":12.3456789,"total_cost":500,"total_account_cost":99}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/auth/login" {
			fmt.Fprint(w, `{"code":0,"data":{"access_token":"test"}}`)
			return
		}
		q := r.URL.Query()
		if r.Method != "GET" || r.URL.Path != "/api/v1/admin/usage/stats" || q.Get("timezone") != "Asia/Shanghai" || q.Get("start_date") != "2026-10-16" || q.Get("end_date") != "2026-10-16" || q.Get("account_id") != "42" {
			t.Errorf("wrong read: %s", r.URL)
		}
		fmt.Fprintf(w, `{"code":0,"data":%s}`, response)
	}))
	defer srv.Close()
	c := New()
	cfg := model.Sub2Settings{URL: srv.URL, Email: "test@example.com"}
	v, err := c.QueryDailyUserCost(context.Background(), cfg, "password", 42, "2026-10-16")
	if err != nil || v != 12.3456789 {
		t.Fatal(v, err)
	}
	for _, raw := range []string{`{"total_account_cost":99}`, `{"total_actual_cost":null}`, `{"total_actual_cost":-1}`} {
		response = raw
		if _, err = c.QueryDailyUserCost(context.Background(), cfg, "password", 42, "2026-10-16"); err == nil {
			t.Fatal("missing/negative user charge accepted")
		}
	}
	response = `{"total_actual_cost":0}`
	if v, err = c.QueryDailyUserCost(context.Background(), cfg, "password", 42, "2026-10-16"); err != nil || v != 0 {
		t.Fatal("valid zero", v, err)
	}
}
