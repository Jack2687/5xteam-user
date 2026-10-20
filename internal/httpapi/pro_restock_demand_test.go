package httpapi

import (
	"chatgpt-space-merge/internal/model"
	"chatgpt-space-merge/internal/restock"
	"chatgpt-space-merge/internal/store"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestRestockDemandSelectedFallbackAccounts(t *testing.T) {
	missing, stale := false, false
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var data any
		switch r.URL.Path {
		case "/api/v1/auth/login":
			data = map[string]string{"access_token": "test"}
		case "/api/v1/admin/ops/concurrency":
			at := time.Now()
			if stale {
				at = at.Add(-time.Hour)
			}
			loads := map[string]any{}
			add := func(id string, n int, used int) {
				loads[id] = map[string]int{"account_id": n, "current_in_use": used, "waiting_in_queue": 0, "max_capacity": 100}
			}
			add("1", 1, 8)
			if !missing {
				add("2", 2, 15)
			}
			add("3", 3, 999)
			data = map[string]any{"enabled": true, "timestamp": at, "account": loads}
		default:
			t.Errorf("unexpected API %s", r.URL.Path)
			http.Error(w, "unexpected", 500)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": data})
	}))
	defer up.Close()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	s := &Server{store: st}
	f := restock.DefaultFallbackConfig()
	f.Enabled = true
	f.URL = up.URL
	f.Email = "test@example.test"
	f.GroupIDs = []int64{7, 8}
	f.AccountIDs = []int64{1, 2}
	id, err := st.SaveDownstreamTarget(model.DownstreamTarget{Scope: "pro-fallback", Provider: "sub2", Sub2: model.Sub2Settings{URL: up.URL, Email: f.Email}, Secret: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = st.SaveProFallbackConfig(f, id); err != nil {
		t.Fatal(err)
	}
	c := restock.DefaultConfig()
	p := model.DefaultProSettings()
	p.Sub2.URL = up.URL + "/"
	snap := restock.Snapshot{At: time.Now(), Valid: true, LoadValid: true, Accounts: []restock.Account{{ID: 1, Used: 8}}}
	g := s.sampleRestockDemand(context.Background(), c, p, snap)
	if !g.Valid || g.MonitorUsed != 8 || g.FallbackUsed != 15 || g.TotalUsed != 23 {
		t.Fatal("unselected or duplicate counted", g)
	}
	p.Sub2.URL = "https://another.example"
	g = s.sampleRestockDemand(context.Background(), c, p, snap)
	if !g.Valid || g.TotalUsed != 31 {
		t.Fatal("different servers shared ID", g)
	}
	snap.Accounts = nil
	g = s.sampleRestockDemand(context.Background(), c, p, snap)
	if !g.Valid || g.TotalUsed != 23 {
		t.Fatal("empty primary lost fallback demand", g)
	}
	missing = true
	g = s.sampleRestockDemand(context.Background(), c, p, snap)
	if g.Valid {
		t.Fatal("missing selected account interpreted as zero")
	}
	missing = false
	stale = true
	g = s.sampleRestockDemand(context.Background(), c, p, snap)
	if g.Valid {
		t.Fatal("stale fallback accepted")
	}
}

func TestRestockDemandContinuesWithoutOrdinaryDemand(t *testing.T) {
	s, st, _, _ := fallbackFixture(t)
	f, target, _ := st.ProFallbackConfig()
	f.Enabled = false
	f.AccountIDs = nil
	if _, err := st.SaveProFallbackConfig(f, target); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	local := now.In(time.FixedZone("Asia/Shanghai", 8*3600))
	c, _ := st.ProRestockConfig()
	c.Enabled = false
	c.TimeEnabled = false
	c.ConcurrencyEnabled = false
	c.ExitEnabled = false
	c.Reserve = 0
	c.EmptyGroupCount = 0
	c.NoRestockWindows = []restock.DailyWindow{{Start: local.Add(-time.Hour).Format("15:04"), End: local.Add(time.Hour).Format("15:04"), HighDemandEnabled: true, HighDemandThreshold: 20, HighDemandConfirmations: 2}}
	if _, err := st.SaveProRestockConfig(c); err != nil {
		t.Fatal(err)
	}
	used := 25
	calls := 0
	s.proRestock.monitor = func(context.Context, restock.Config, model.ProSettings, string) (restock.Snapshot, error) {
		calls++
		return restock.Snapshot{At: time.Now(), Valid: true, LoadValid: true, Accounts: []restock.Account{{ID: 100, Used: used, Capacity: 50}}}, nil
	}
	for i := 0; i < 2; i++ {
		s.proRestock.next = time.Time{}
		if err := s.tickProRestock(context.Background(), false); err != nil {
			t.Fatal(err)
		}
	}
	c, err := st.ProRestockConfig()
	if err != nil || calls != 2 || !c.DemandStatus(time.Now()).Allowed {
		t.Fatal("monitoring waited for a replenishment trigger", calls, err, c.DemandGate)
	}
	used = 1
	s.proRestock.next = time.Time{}
	if err = s.tickProRestock(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	c, _ = st.ProRestockConfig()
	if c.DemandStatus(time.Now()).Allowed {
		t.Fatal("falling load retained permission")
	}
}
