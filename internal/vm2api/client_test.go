package vm2api

import (
	"chapt-space-user/internal/model"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestQuotaAuthenticationAndWindows(t *testing.T) {
	for _, tc := range []struct {
		name, body          string
		status              int
		unauthorized, valid bool
	}{
		{"management401", `{"ok":false,"error":{"code":"unauthorized"}}`, 401, false, false},
		{"account401", `{"ok":false,"error":{"code":"upstream_auth","details":{"upstream_status":401}}}`, 502, true, false},
		{"account403", `{"ok":false,"error":{"code":"upstream_auth","details":{"upstream_status":403}}}`, 502, false, false},
		{"unknown", `{"ok":true,"data":{"windows":[{"id":"5h","used_percent":null,"window_minutes":300}]}}`, 200, false, false},
		{"windows", `{"ok":true,"data":{"windows":[{"id":"5h","used_percent":90,"window_minutes":300,"reset_at":"2026-10-12T12:00:00Z"},{"id":"7d","used_percent":25,"window_minutes":10080}]}}`, 200, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer management" || r.Header.Get("X-Space-Assignment") != "cycle-1" {
					t.Error("missing management auth or assignment")
				}
				w.WriteHeader(tc.status)
				fmt.Fprint(w, tc.body)
			}))
			defer srv.Close()
			cfg := model.DefaultVM2APISettings()
			cfg.URL = srv.URL
			five, seven, e := New(cfg, "management").Quota(t.Context(), "space-123", "cycle-1")
			if UpstreamUnauthorized(e) != tc.unauthorized {
				t.Fatalf("wrong auth classification: %v", e)
			}
			if tc.valid {
				if e != nil || five.UsedPercent != 90 || five.LimitWindowSeconds != 18000 || five.ResetAt == 0 || seven.LimitWindowSeconds != 604800 {
					t.Fatalf("bad windows: %+v %+v %v", five, seven, e)
				}
			} else if e == nil {
				t.Fatal("invalid quota accepted")
			}
		})
	}
}
func TestGlobalSettingsAndRedirectSafety(t *testing.T) {
	v := model.DefaultVM2APISettings()
	v.URL = "https://vm.example.com"
	if e := Validate(v, true); e != nil {
		t.Fatal("global scheduling must not need a pool", e)
	}
	if Validate(v, false) == nil {
		t.Fatal("missing management key accepted")
	}
	leaked := false
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { leaked = true }))
	defer other.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, other.URL, 307) }))
	defer srv.Close()
	v.URL = srv.URL
	if e := New(v, "management").Check(t.Context()); e == nil || leaked {
		t.Fatal("redirect followed with management credentials")
	}
}
