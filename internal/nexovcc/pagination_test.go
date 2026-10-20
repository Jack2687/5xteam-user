package nexovcc

import (
	"context"
	"fmt"
	"testing"
)

func TestPagesUsesSupplierTotalsAndPageCaps(t *testing.T) {
	for _, path := range []string{"/api/card/findUserCardList", "/api/product/findUserProductList"} {
		for _, tc := range []struct {
			name       string
			total, cap int
			withTotal  bool
			wantCalls  int
		}{
			{"capped short pages", 23, 10, true, 3},
			{"exact full page", 100, 100, true, 1},
			{"exact capped pages", 20, 10, true, 2},
			{"empty account", 0, 10, true, 1},
			{"legacy page size", 23, 10, false, 3},
		} {
			t.Run(path+"/"+tc.name, func(t *testing.T) {
				calls := 0
				c := New(Credentials{}, func(_ context.Context, r Request) (Response, error) {
					if r.Path == "/api/login" {
						v := reply(map[string]any{"user_id": 1})
						v.Cookie = "fixture"
						return v, nil
					}
					calls++
					page := r.Body["page"].(int)
					rows := []any{}
					for i := (page - 1) * tc.cap; i < min(page*tc.cap, tc.total); i++ {
						rows = append(rows, map[string]any{"id": i, "serial_no": fmt.Sprint(i)})
					}
					data := map[string]any{"list": rows, "page": page, "pageSize": tc.cap}
					if tc.withTotal {
						data["total"] = tc.total
					}
					return reply(data), nil
				})
				rows, err := c.Pages(context.Background(), path)
				if err != nil || len(rows) != tc.total || calls != tc.wantCalls {
					t.Fatalf("got %d records in %d calls, error %v", len(rows), calls, err)
				}
			})
		}
	}
}

func TestPagesRejectsIncompleteOrChangingMetadata(t *testing.T) {
	for _, mode := range []string{"wrong page", "changed total", "missing total", "empty early", "repeated short page", "invalid total", "invalid page size"} {
		t.Run(mode, func(t *testing.T) {
			c := New(Credentials{}, func(_ context.Context, r Request) (Response, error) {
				if r.Path == "/api/login" {
					v := reply(map[string]any{"user_id": 1})
					v.Cookie = "fixture"
					return v, nil
				}
				page := r.Body["page"].(int)
				data := map[string]any{"list": []any{map[string]any{"id": page}}, "page": page, "pageSize": 1, "total": 2}
				if page == 2 {
					switch mode {
					case "wrong page":
						data["page"] = 1
					case "changed total":
						data["total"] = 3
					case "missing total":
						delete(data, "total")
					case "empty early":
						data["list"] = []any{}
					case "repeated short page":
						data["list"] = []any{map[string]any{"id": 1}}
					case "invalid total":
						data["total"] = -1
					case "invalid page size":
						data["pageSize"] = 0
					}
				}
				return reply(data), nil
			})
			rows, err := c.Pages(context.Background(), "/api/card/findUserCardList")
			if err == nil || rows != nil {
				t.Fatalf("incomplete inventory accepted: %v, %v", rows, err)
			}
		})
	}
}
