package httpapi

import (
	"context"
	"strings"
	"testing"

	"chatgpt-space-merge/internal/nexovcc"
)

func TestNexovccUnknownOpenQueryFindsOriginalWithoutReissuing(t *testing.T) {
	for _, mode := range []string{"found", "absent", "wrong-product", "ambiguous", "query-failed"} {
		t.Run(mode, func(t *testing.T) {
			s, f := nexFixtureServer(t)
			f.unknown = true
			transport := s.nexovcc.transport
			reads := 0
			s.nexovcc.transport = func(ctx context.Context, r nexovcc.Request) (nexovcc.Response, error) {
				if r.Path != "/api/card/findUserCardList" {
					return transport(ctx, r)
				}
				reads++
				if mode == "query-failed" {
					return nexovcc.Response{Status: 502}, nil
				}
				rows := []any{}
				if mode != "absent" {
					pid := 267
					if mode == "wrong-product" {
						pid = 999
					}
					rows = append(rows, map[string]any{"serial_no": "nex-test-serial", "pid": pid, "remark": f.remark, "status": 1, "card_no": "4242424242424242"})
				}
				if mode == "ambiguous" {
					rows = append(rows, map[string]any{"serial_no": "another-card", "pid": 267, "remark": f.remark, "status": 1, "card_no": "5555555555554444"})
				}
				return nexReply(map[string]any{"list": rows, "total": len(rows)}), nil
			}
			op, err := s.openNexovccLocked(context.Background(), "nex-originalquery1", "template", false)
			if err != nil || op.Status != "unknown" {
				t.Fatal(op, err)
			}
			err = s.recoverNexovccOpenForProRetry(context.Background())
			saved, _ := s.store.NexovccOperation(op.ID)
			if mode == "found" {
				if err != nil || saved.Status != "succeeded" || saved.Serial != "nex-test-serial" {
					t.Fatal(saved, err)
				}
			} else {
				if err == nil || saved.Status != "unknown" || saved.Serial != "" {
					t.Fatal("uncertain issuance cleared", saved, err)
				}
				if mode == "absent" || mode == "wrong-product" {
					if !strings.Contains(err.Error(), "未重新开卡") {
						t.Fatal(err)
					}
					if err = s.recoverNexovccOpenForProRetry(context.Background()); err == nil || reads != 1 {
						t.Fatal("repeated clicks polled again", reads, err)
					}
				}
			}
			if f.opens.Load() != 1 {
				t.Fatal("replayed create-card", f.opens.Load())
			}
		})
	}
}
