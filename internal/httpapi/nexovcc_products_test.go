package httpapi

import (
	"context"
	"fmt"
	"testing"

	"chatgpt-space-merge/internal/nexovcc"
)

func TestNexovccOpenUsesLiveOrderedProductsAndNeverReplaysUnknown(t *testing.T) {
	for _, mode := range []string{"fallback", "unavailable", "query-error", "unknown"} {
		t.Run(mode, func(t *testing.T) {
			s, _ := nexFixtureServer(t)
			cfg, _, _ := s.store.NexovccSettings()
			cfg.Templates[0].ProductIDs = []string{"999", "268", "267"}
			if _, err := s.store.SaveNexovccSettings(cfg, nexovcc.Credentials{}, false); err != nil {
				t.Fatal(err)
			}
			base := s.nexovcc.transport
			queries, opens := 0, 0
			s.nexovcc.transport = func(ctx context.Context, r nexovcc.Request) (nexovcc.Response, error) {
				switch r.Path {
				case "/api/product/findUserProductList":
					queries++
					if mode == "query-error" {
						return nexovcc.Response{Status: 503}, nil
					}
					rows := []any{}
					if mode != "unavailable" {
						rows = []any{map[string]any{"ID": 267, "status": 1, "recharge_min": "1.00"}, map[string]any{"ID": 999, "status": 0, "recharge_min": "1.00"}, map[string]any{"ID": 268, "status": 1, "recharge_min": "2.00"}}
					}
					return nexReply(map[string]any{"list": rows, "total": len(rows), "page": 1}), nil
				case "/api/card/createUserCard":
					opens++
					if fmt.Sprint(r.Body["pid"]) != "268" {
						t.Fatal("wrong product", r.Body)
					}
					if mode == "unknown" {
						return nexovcc.Response{Status: 502}, nil
					}
					return nexReply(map[string]any{"serial_no": "priority-card"}), nil
				}
				return base(ctx, r)
			}
			op, err := s.openNexovccLocked(context.Background(), "nex-product-test1", "template", false)
			if queries != 1 {
				t.Fatal("did not fetch fresh products", queries)
			}
			if mode == "query-error" || mode == "unavailable" {
				if err == nil || opens != 0 {
					t.Fatal("opened from stale cache", op, err, opens)
				}
				return
			}
			if err != nil || opens != 1 || op.ProductID != "268" || (mode == "unknown" && op.Status != "unknown") || (mode == "fallback" && op.Status != "succeeded") {
				t.Fatal(op, err, opens)
			}
			again, err := s.openNexovccLocked(context.Background(), "nex-product-test1", "template", false)
			if err != nil || again.ID != op.ID || opens != 1 || queries != 1 {
				t.Fatal("replayed existing request", again, err, opens, queries)
			}
		})
	}
}
