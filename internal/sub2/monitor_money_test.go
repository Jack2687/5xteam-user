package sub2

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"chatgpt-space-merge/internal/model"
)

func TestMonitorMoneyAccountBasisAndReadRequests(t *testing.T) {
	for _, raw := range []string{`"total_account_cost":350`, `"total_account_cost":0`, `"total_account_cost":null`, `"total_account_cost":-1`, `"other":2`} {
		t.Run(raw, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/v1/auth/login" {
					fmt.Fprint(w, `{"code":0,"data":{"access_token":"fixture"}}`)
					return
				}
				if r.Method != http.MethodGet {
					t.Errorf("unexpected mutation %s", r.Method)
				}
				switch r.URL.Path {
				case "/api/v1/admin/accounts/42/usage":
					if r.URL.RawQuery != "" {
						t.Error("must not force or request Anthropic passive mode")
					}
					fmt.Fprintf(w, `{"code":0,"data":{"updated_at":%q,"seven_day":{"utilization":50,"resets_at":%q,"window_stats":{"cost":266.41,"user_cost":53.282,"standard_cost":999}}}}`, time.Now().Format(time.RFC3339Nano), time.Now().Add(time.Hour).Format(time.RFC3339Nano))
				case "/api/v1/admin/usage/stats":
					q := r.URL.Query()
					day := time.Now().UTC().Format("2006-01-02")
					if q.Get("account_id") != "42" || q.Get("timezone") != "UTC" || q.Get("nocache") != "true" || q.Get("start_date") != day || q.Get("end_date") != day {
						t.Error("incorrect cumulative sampling scope", q)
					}
					fmt.Fprintf(w, `{"code":0,"data":{%s,"total_cost":999,"total_actual_cost":70}}`, raw)
				default:
					t.Error("unexpected URL", r.URL)
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			u, err := New().MonitorMoney(context.Background(), model.Sub2Settings{URL: server.URL, Email: "fixture@example.com"}, "fixture", 42)
			wantError := raw != `"total_account_cost":350` && raw != `"total_account_cost":0`
			if (err != nil) != wantError {
				t.Fatal(u, err)
			}
			if !wantError && (*u.SevenDay.Stats.Cost != 266.41 || u.DailyCost == 999 || u.DailyCost == 70) {
				t.Fatal("mixed cost bases", u)
			}
		})
	}
}
