package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"chatgpt-space-merge/internal/model"
	"chatgpt-space-merge/internal/restock"
	"chatgpt-space-merge/internal/store"
)

func TestGlobalPaginationRestockAndRecovery(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	s, err := New(st, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	settings := st.Settings()
	settings.DefaultPageSize = 50
	if err := st.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 512; i++ {
		id := fmt.Sprintf("pagination-%04d", i)
		if _, err := st.SaveMailAccount(model.MailAccountProfile{Email: id + "@example.com"}, model.MailAccountCredentials{Email: id + "@example.com", AccessToken: "fixture"}); err != nil {
			t.Fatal(err)
		}
		if err := st.SaveProRestockDecision(restock.Decision{ID: id, At: time.Now()}); err != nil {
			t.Fatal(err)
		}
		if err := st.CreateSeatRecoveryTask(model.SeatRecoveryTask{ID: id, Email: id + "@example.com", TeamID: "team"}); err != nil {
			t.Fatal(err)
		}
	}
	mux := http.NewServeMux()
	s.registerProRestock(mux)
	for _, target := range []struct {
		path    string
		handler http.HandlerFunc
	}{
		{"/api/pro-restock/records", mux.ServeHTTP}, {"/api/seat-recovery/tasks", s.listSeatRecovery},
	} {
		for _, size := range []int{10, 50, 100, 500} {
			seen := map[string]bool{}
			for page := 1; page <= (512+size-1)/size; page++ {
				w := httptest.NewRecorder()
				target.handler(w, httptest.NewRequest("GET", fmt.Sprintf("%s?page=%d&page_size=%d", target.path, page, size), nil))
				var result struct {
					Data struct {
						Items       []struct{ ID string }
						Total, Page int
						PageSize    int `json:"page_size"`
					}
				}
				if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
					t.Fatal(err)
				}
				data := result.Data
				if w.Code != 200 || data.Total != 512 || data.PageSize != size || data.Page != page || len(data.Items) != min(size, 512-(page-1)*size) {
					t.Fatalf("%s: %s", target.path, w.Body.String())
				}
				for _, item := range data.Items {
					if seen[item.ID] {
						t.Fatalf("duplicate %s", item.ID)
					}
					seen[item.ID] = true
				}
			}
			if len(seen) != 512 {
				t.Fatalf("pagination lost rows: %d", len(seen))
			}
		}
		w := httptest.NewRecorder()
		target.handler(w, httptest.NewRequest("GET", target.path+"?page=2", nil))
		var envelope struct{ Data json.RawMessage }
		if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil {
			t.Fatal(err)
		}
		var rows []json.RawMessage
		if target.path == "/api/pro-restock/records" {
			if err := json.Unmarshal(envelope.Data, &rows); err != nil {
				t.Fatal(err)
			}
		} else {
			var data struct{ Items []json.RawMessage }
			if err := json.Unmarshal(envelope.Data, &data); err != nil {
				t.Fatal(err)
			}
			rows = data.Items
		}
		if len(rows) != 50 {
			t.Fatalf("missing size did not inherit global setting: %s", w.Body.String())
		}
	}
}

func TestAdminMemberDisplayPaginationKeepsSupplierLimit(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	calls := 0
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		if limit < 1 || limit > 25 {
			t.Errorf("supplier limit=%d", limit)
		}
		items := []map[string]any{}
		for i := offset; i < min(offset+limit, 625); i++ {
			items = append(items, map[string]any{"id": fmt.Sprint(i), "email": fmt.Sprintf("member-%d@example.com", i)})
		}
		json.NewEncoder(w).Encode(map[string]any{"items": items, "total": 625})
	}))
	defer proxy.Close()
	settings := model.DefaultSettings()
	settings.BaseURL = "http://127.0.0.1:1/backend-api"
	settings.DefaultPageSize = 50
	if err := st.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	mother := saveTestAdmin(t, st, model.AdminAccountProfile{Label: "mother", TeamAccountID: "team"}, "fixture", proxy.URL)
	s, err := New(st, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, tc := range []struct {
		query                      string
		size, offset, count, calls int
		more                       bool
	}{
		{"", 50, 0, 50, 2, true}, {"limit=10&offset=10", 10, 10, 10, 1, true}, {"limit=100&offset=100", 100, 100, 100, 4, true},
		{"limit=500", 500, 0, 500, 20, true}, {"limit=500&offset=500", 500, 500, 125, 5, false},
	} {
		calls = 0
		r := httptest.NewRequest("GET", "/api/admin-accounts/"+mother.ID+"/members?"+tc.query, nil)
		r.SetPathValue("id", mother.ID)
		w := httptest.NewRecorder()
		s.adminAccountMembers(w, r)
		var out struct {
			Data struct {
				Items         []struct{ ID string }
				Limit, Offset int
				More          bool `json:"has_more"`
			}
		}
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		if w.Code != 200 || len(out.Data.Items) != tc.count || out.Data.Limit != tc.size || out.Data.Offset != tc.offset || out.Data.More != tc.more || calls != tc.calls {
			t.Fatalf("%s calls=%d: %s", tc.query, calls, w.Body.String())
		}
		for i, item := range out.Data.Items {
			if item.ID != fmt.Sprint(tc.offset+i) {
				t.Fatalf("skipped member: %+v", item)
			}
		}
	}
}
