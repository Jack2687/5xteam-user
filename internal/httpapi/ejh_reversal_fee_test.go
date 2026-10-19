package httpapi

import (
	"bytes"
	"chatgpt-space-merge/internal/ejh"
	"chatgpt-space-merge/internal/store"
	"encoding/json"
	"net/http/httptest"
	"testing"
)

func TestEJHReversalFeeSettingsAPI(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	s := &Server{store: st}
	raw, _ := json.Marshal(ejh.DefaultSettings())
	var body map[string]any
	if err = json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	put := func(rate any, include bool) int {
		if include {
			body["reversal_fee_percent"] = rate
		} else {
			delete(body, "reversal_fee_percent")
		}
		b, _ := json.Marshal(body)
		r := httptest.NewRequest("PUT", "/api/ejh/settings", bytes.NewReader(b))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		s.saveEJHSettings(w, r)
		return w.Code
	}
	if code := put(7.25, true); code != 200 {
		t.Fatal("save", code)
	}
	for _, rate := range []any{-1, 101, 6.001, "wrong"} {
		if code := put(rate, true); code != 400 {
			t.Fatal("invalid accepted", rate, code)
		}
	}
	if code := put(nil, false); code != 200 {
		t.Fatal("legacy save", code)
	}
	read := func(dashboard bool, want float64) {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("GET", "/api/ejh/settings", nil)
		if dashboard {
			s.ejhDashboard(w, r)
		} else {
			s.getEJHSettings(w, r)
		}
		var result struct {
			Data struct {
				Percent float64 `json:"reversal_fee_percent"`
			} `json:"data"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || w.Code != 200 || result.Data.Percent != want {
			t.Fatalf("read dashboard=%v: %d %s %v", dashboard, w.Code, w.Body.String(), err)
		}
	}
	read(false, 7.25)
	read(true, 7.25)
	if code := put(0, true); code != 200 {
		t.Fatal("zero save", code)
	}
	read(false, 0)
	read(true, 0)
}
