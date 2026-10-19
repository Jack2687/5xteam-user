package httpapi

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"chatgpt-space-merge/internal/model"
	"chatgpt-space-merge/internal/sub2"
)

func TestFallbackMultiGroupUnionSaveReconcileAndDrain(t *testing.T) {
	s, st, m, _ := fallbackFixture(t)
	m.memberships[1] = []int64{7, 8}
	m.group[2] = 8
	f, targetID, _ := st.ProFallbackConfig()
	f.GroupIDs = []int64{7, 8}
	v := fallbackInput{FallbackConfig: f}
	target := model.DownstreamTarget{Sub2: model.Sub2Settings{URL: f.URL, Email: f.Email}, Secret: "secret"}
	accounts, err := fallbackList(context.Background(), sub2.New(), v, target)
	if err != nil || len(accounts) != 3 || len(m.writes) != 0 {
		t.Fatal("union preview", accounts, err)
	}
	seen := map[int64]bool{}
	for _, a := range accounts {
		if seen[a.ID] {
			t.Fatal("duplicate account", a.ID)
		}
		seen[a.ID] = true
	}
	raw, _ := json.Marshal(v)
	w := httptest.NewRecorder()
	s.saveProFallback(w, httptest.NewRequest("PUT", "/", strings.NewReader(string(raw))))
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	f, _, _ = st.ProFallbackConfig()
	if len(f.GroupIDs) != 2 {
		t.Fatal("groups not saved", f)
	}
	if err = s.tickProFallback(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	if !m.on[1] || !m.on[2] || m.writes[1] != 1 || m.writes[2] != 1 || m.writes[3] != 0 {
		t.Fatal(m.on, m.writes)
	}
	// Removing one group must keep accounts still in another selected group.
	f.GroupIDs = []int64{8}
	f.AccountIDs = []int64{1}
	if _, err = st.SaveProFallbackConfig(f, targetID); err != nil {
		t.Fatal(err)
	}
	if err = s.tickProFallback(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	if !m.on[1] || m.on[2] || m.writes[1] != 1 || m.writes[2] != 2 {
		t.Fatal("incorrect drain", m.on, m.writes)
	}
	m.memberships[1] = []int64{7}
	if err = s.tickProFallback(context.Background(), true); err == nil || m.on[1] {
		t.Fatal("account outside all selected groups remained enabled", err)
	}
}

func TestFallbackMultiGroupRejectsPartialOrMissingGroup(t *testing.T) {
	s, st, m, _ := fallbackFixture(t)
	f, _, _ := st.ProFallbackConfig()
	f.GroupIDs = []int64{7, 8}
	before := f.Version
	for _, missing := range []bool{false, true} {
		m.failGroup = 8
		if missing {
			f.GroupIDs = []int64{7, 99}
		}
		raw, _ := json.Marshal(fallbackInput{FallbackConfig: f})
		w := httptest.NewRecorder()
		s.saveProFallback(w, httptest.NewRequest("PUT", "/", strings.NewReader(string(raw))))
		current, _, _ := st.ProFallbackConfig()
		if w.Code != 400 || current.Version != before || len(m.writes) != 0 {
			t.Fatal("partial group selection was saved", w.Code, w.Body.String())
		}
	}
}
