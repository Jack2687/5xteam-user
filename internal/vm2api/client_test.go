package vm2api

import (
	"chatgpt-space-merge/internal/model"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
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

func TestProxySelectionContractAndLegacyServer(t *testing.T) {
	for _, tc := range []struct {
		name      string
		enabled   bool
		ids, want []string
	}{
		{"off", false, []string{"px-a"}, nil},
		{"empty-default-local", true, []string{}, nil},
		{"selected", true, []string{"px-a", " px-b ", "px-a"}, []string{"px-a", "px-b"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			advanced := false
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/panel/space-capabilities" {
					fmt.Fprintf(w, `{"ok":true,"data":{"version":1,"docker_per_slot":true,"delete_account":true,"proxy_selection":%t}}`, advanced)
					return
				}
				var body struct {
					ProxyID  string   `json:"proxy_id"`
					ProxyIDs []string `json:"proxy_ids"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				if body.ProxyID != "px-local" || !reflect.DeepEqual(body.ProxyIDs, tc.want) {
					t.Errorf("unexpected proxy selection: %+v", body)
				}
				fmt.Fprint(w, `{"ok":true,"data":{}}`)
			}))
			defer srv.Close()
			cfg := model.DefaultVM2APISettings()
			cfg.URL, cfg.BindProxy, cfg.ProxyIDs = srv.URL, tc.enabled, tc.ids
			client := New(cfg, "management")
			if err := client.Check(t.Context()); (err != nil) != (len(tc.want) > 0) {
				t.Fatalf("incorrect legacy capability check: %v", err)
			}
			advanced = true
			if err := client.Check(t.Context()); err != nil {
				t.Fatal(err)
			}
			if _, err := client.Create(t.Context(), "account@example.com", "assignment-fixture"); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestProxyListStripsCredentialsAndKeepsAvailabilityCounts(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/panel/proxies" || r.Method != "GET" || r.Header.Get("Authorization") != "Bearer management" {
			t.Errorf("unexpected proxy request: %s", r.URL.Path)
		}
		fmt.Fprint(w, `{"ok":true,"data":{"proxies":[{"id":"px-a","label":"出口 A","scheme":"socks5","host":"10.0.0.1","port":1080,"enabled":true,"status":"ok","bound_count":2,"bind_limit":32,"username":"private-user","password":"private-pass","url":"socks5://private"},{"id":"px-local","scheme":"local","enabled":false,"status":"dead","bound_count":0,"bind_limit":32}]}}`)
	}))
	defer srv.Close()
	cfg := model.DefaultVM2APISettings()
	cfg.URL = srv.URL
	rows, err := New(cfg, "management").Proxies(t.Context())
	if err != nil || len(rows) != 2 || rows[0].BoundCount == nil || *rows[0].BoundCount != 2 || *rows[0].BindLimit != 32 || rows[1].Enabled {
		t.Fatalf("bad proxy list: %+v %v", rows, err)
	}
	encoded, _ := json.Marshal(rows)
	var fields []map[string]any
	_ = json.Unmarshal(encoded, &fields)
	for _, field := range []string{"username", "password", "url"} {
		if _, ok := fields[0][field]; ok {
			t.Fatalf("proxy credentials exposed: %s", field)
		}
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

func TestRuntimeCapabilityAndCreateSpec(t *testing.T) {
	for _, runtime := range []string{"", "docker", "kvm"} {
		t.Run(runtime, func(t *testing.T) {
			kvmSupported := false
			cfg := model.DefaultVM2APISettings()
			cfg.RuntimeType, cfg.Memory, cfg.VCPUs, cfg.DiskGB = runtime, "2g", 4, 40
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/panel/space-capabilities" {
					fmt.Fprintf(w, `{"ok":true,"data":{"version":1,"docker_per_slot":true,"delete_account":true,"kvm_per_slot":%t}}`, kvmSupported)
					return
				}
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				if body["execution_mode"] != ExecutionMode(cfg) || body["runtime_type"] != RuntimeType(cfg) || body["start"] != true {
					t.Error("incorrect runtime contract", body)
				}
				machine := body["machine"].(map[string]any)
				if machine["memory"] != "2g" {
					t.Error("memory lost")
				}
				if runtime == "kvm" {
					if machine["vcpus"] != float64(4) || machine["disk_gb"] != float64(40) {
						t.Error("KVM resources lost", machine)
					}
				} else if len(machine) != 1 {
					t.Error("Docker must only send memory", machine)
				}
				fmt.Fprint(w, `{"ok":true,"data":{}}`)
			}))
			defer srv.Close()
			cfg.URL = srv.URL
			client := New(cfg, "management")
			if err := client.Check(t.Context()); (err != nil) != (runtime == "kvm") {
				t.Fatalf("legacy capability: %v", err)
			}
			kvmSupported = true
			if err := client.Check(t.Context()); err != nil {
				t.Fatal(err)
			}
			if _, err := client.Create(t.Context(), "account@example.com", "assignment-test"); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestKVMResourceValidation(t *testing.T) {
	v := model.DefaultVM2APISettings()
	v.URL, v.RuntimeType = "https://vm.example.com", "kvm"
	for _, pair := range [][2]int{{1, 10}, {16, 200}, {0, 20}, {17, 20}, {2, 9}, {2, 201}} {
		v.VCPUs, v.DiskGB = pair[0], pair[1]
		want := v.VCPUs >= 1 && v.VCPUs <= 16 && v.DiskGB >= 10 && v.DiskGB <= 200
		if (Validate(v, true) == nil) != want {
			t.Errorf("unexpected validation for %v", pair)
		}
	}
	v.RuntimeType = "bad"
	if Validate(v, true) == nil {
		t.Fatal("unknown runtime accepted")
	}
}
