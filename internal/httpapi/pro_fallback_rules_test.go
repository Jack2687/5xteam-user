package httpapi

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"chatgpt-space-merge/internal/restock"
)

func TestFallbackRulesIndependentSharedFailureAndDrain(t *testing.T) {
	s, st, m, _ := fallbackFixture(t)
	m.sourceCounts = map[int64]int{10: 1}
	m.sourceErrors = map[int64]bool{}
	m.memberships[1] = []int64{7, 8}
	m.group[2] = 8
	f, target, _ := st.ProFallbackConfig()
	f.Rules = []restock.FallbackRule{
		{Enabled: true, MonitorGroupID: 9, GroupIDs: []int64{7}, AccountIDs: []int64{1}},
		{Enabled: true, MonitorGroupID: 10, GroupIDs: []int64{7, 8}, AccountIDs: []int64{1, 2}},
	}
	var err error
	f, err = st.SaveProFallbackConfig(f, target)
	if err != nil {
		t.Fatal(err)
	}
	tick := func(wantError bool) {
		t.Helper()
		err := s.tickProFallback(context.Background(), true)
		if (err != nil) != wantError {
			t.Fatal("unexpected reconciliation result", err)
		}
	}
	check := func(one, two bool) {
		t.Helper()
		if m.on[1] != one || m.on[2] != two || m.writes[3] != 0 {
			t.Fatal(m.on, m.writes)
		}
	}
	tick(false)
	check(true, false) // only rule 1 needs the shared account
	m.sourceCounts[10] = 0
	tick(false)
	check(true, true)
	if m.writes[1] != 1 {
		t.Fatal("shared account was toggled twice", m.writes)
	}
	m.sourceCount = 2
	tick(false)
	check(true, true) // rule 2 still needs it
	m.sourceErrors[10] = true
	tick(true)
	check(true, true) // unknown rule must not close the shared account
	m.sourceFailure = true
	m.sourceErrors[10] = false
	m.sourceCounts[10] = 1
	tick(true)
	check(true, false) // unrelated rule can still close its own account
	m.sourceFailure = false
	tick(false)
	check(false, false)
	m.sourceCounts[10] = 0
	tick(false)
	check(true, true)
	// Removing rule 2 drains its exclusive account without closing shared rule 1.
	m.sourceCount = 0
	f.Rules = f.Rules[:1]
	f, err = st.SaveProFallbackConfig(f, target)
	if err != nil {
		t.Fatal(err)
	}
	tick(false)
	check(true, false)
	// Reconfiguration remains independent of the automatic restock monitor.
	c, _ := st.ProRestockConfig()
	c.GroupID = 999
	if _, err = st.SaveProRestockConfig(c); err != nil {
		t.Fatal(err)
	}
	tick(false)
	check(true, false)
	// Restart samples both source and actual state; no redundant scheduling POST.
	restarted := &Server{store: st}
	restarted.proFallback.now = s.proFallback.now
	before := m.writes[1]
	if err = restarted.tickProFallback(context.Background(), true); err != nil || m.writes[1] != before {
		t.Fatal(err, m.writes)
	}
	// An explicit empty rule list stays empty after JSON persistence and drains.
	f.Rules = []restock.FallbackRule{}
	f, err = st.SaveProFallbackConfig(f, target)
	if err != nil {
		t.Fatal(err)
	}
	stored, _, _ := st.ProFallbackConfig()
	if stored.Rules == nil || len(stored.AccountIDs) != 0 {
		t.Fatal("removed rules restored legacy selection", stored)
	}
	tick(false)
	check(false, false)
}

func TestFallbackRulesAPIValidationPreviewAndLegacy(t *testing.T) {
	s, st, m, _ := fallbackFixture(t)
	m.sourceCounts = map[int64]int{10: 0}
	m.group[2] = 8
	get := httptest.NewRecorder()
	s.getProFallback(get, httptest.NewRequest("GET", "/", nil))
	var before struct {
		Data struct {
			Config restock.FallbackConfig `json:"config"`
		} `json:"data"`
	}
	if json.Unmarshal(get.Body.Bytes(), &before) != nil || len(before.Data.Config.Rules) != 1 || before.Data.Config.Rules[0].MonitorGroupID != 9 {
		t.Fatal("legacy display migration", get.Body.String())
	}
	monitors := httptest.NewRecorder()
	s.fallbackMonitorGroups(monitors, httptest.NewRequest("GET", "/", nil))
	if monitors.Code != 200 || !strings.Contains(monitors.Body.String(), "monitor-10") || len(m.writes) != 0 {
		t.Fatal(monitors.Body.String())
	}
	f, _, _ := st.ProFallbackConfig()
	f.Rules = []restock.FallbackRule{{Enabled: true, MonitorGroupID: 9, GroupIDs: []int64{7}, AccountIDs: []int64{1}}, {Enabled: true, MonitorGroupID: 10, GroupIDs: []int64{8}, AccountIDs: []int64{2}}}
	save := func(want int) {
		t.Helper()
		raw, _ := json.Marshal(fallbackInput{FallbackConfig: f})
		w := httptest.NewRecorder()
		s.saveProFallback(w, httptest.NewRequest("PUT", "/", strings.NewReader(string(raw))))
		if w.Code != want {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	f.Rules[1].AccountIDs = []int64{1}
	save(400) // union membership must not validate the wrong rule
	f.Rules[1].AccountIDs = []int64{2}
	f.Rules[1].MonitorGroupID = 9
	save(400)
	f.Rules[1].MonitorGroupID = 999
	save(400)
	f.Rules[1].MonitorGroupID = 10
	save(200)
	stored, _, _ := st.ProFallbackConfig()
	if len(stored.Rules) != 2 || len(stored.AccountIDs) != 2 {
		t.Fatal(stored)
	}
	if len(m.writes) != 0 {
		t.Fatal("saving or previewing switched scheduling")
	}
}
