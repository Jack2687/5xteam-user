package workflow

import (
	"chatgpt-space-merge/internal/model"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestMergeV2RemovedRequiresCompleteUnambiguousLists(t *testing.T) {
	for _, body := range []string{`{}`, `{"items":[null]}`, `{"items":[],"total":1}`, `{"items":[],"has_more":true}`, `{"items":[{"email":"child@example.com","id":"child"}]}`, `{"items":[],"total":-1}`, `{"items":[],"has_more":"false"}`} {
		t.Run(body, func(t *testing.T) {
			u := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, body) }))
			defer u.Close()
			cfg := model.DefaultSettings()
			cfg.BaseURL = u.URL
			cfg.ProxyURL = u.URL
			c, err := NewClient(cfg)
			if err != nil {
				t.Fatal(err)
			}
			if err = c.ConfirmMergeV2Removed(t.Context(), "mother", "team", "child@example.com", "child"); err == nil {
				t.Fatal("incomplete evidence accepted")
			}
		})
	}
	count := 0
	u := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count++
		if r.Method != "GET" {
			t.Error("mutation during confirm")
		}
		fmt.Fprint(w, `{"items":[],"total":0}`)
	}))
	defer u.Close()
	cfg := model.DefaultSettings()
	cfg.BaseURL = u.URL
	cfg.ProxyURL = u.URL
	c, _ := NewClient(cfg)
	if err := c.ConfirmMergeV2Removed(t.Context(), "mother", "team", "child@example.com", "child"); err != nil || count != 2 {
		t.Fatal(count, err)
	}
}
