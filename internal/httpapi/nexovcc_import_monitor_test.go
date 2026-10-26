package httpapi

import (
	"context"
	"testing"
	"time"

	"chatgpt-space-merge/internal/nexovcc"
)

func TestNexovccImportRecoveryIndependentOfBulkRefresh(t *testing.T) {
	for _, mode := range []string{"ready", "incomplete", "paused", "disabled", "pending_category", "closed_card", "unknown", "not_due", "busy", "restarted", "already_imported"} {
		t.Run(mode, func(t *testing.T) {
			s, f := nexFixtureServer(t)
			f.failDetail = true
			f.noSerial = mode == "unknown"
			op, err := s.openNexovccLocked(context.Background(), "nex-importworker1", "template", false)
			if err != nil {
				t.Fatal(err)
			}
			cfg, _, _ := s.store.NexovccSettings()
			cfg.CardSyncSeconds = 86400
			cfg.Enabled = mode != "disabled"
			if _, err = s.store.SaveNexovccSettings(cfg, nexovcc.Credentials{}, false); err != nil {
				t.Fatal(err)
			}
			if err = s.store.NexovccCache("sync_state", nexovccSyncState{RetryAfter: time.Now().Add(time.Hour)}); err != nil {
				t.Fatal(err)
			}
			if mode == "pending_category" || mode == "closed_card" {
				status := "1"
				if mode == "closed_card" {
					status = "4"
				}
				if _, err = s.store.SaveNexovccCard(nexovcc.Card{Serial: op.Serial, Status: status}, nil); err != nil {
					t.Fatal(err)
				}
				if mode == "pending_category" {
					if _, err = s.store.SetNexovccCardNormalPending(op.CardID, true); err != nil {
						t.Fatal(err)
					}
				}
			}
			if mode == "paused" {
				if r := payCall(t, s.controlNexovcc, "", "", map[string]any{"action": "stop"}); r.Code != 200 {
					t.Fatal(r.Code)
				}
			}
			f.failDetail = false
			if mode == "already_imported" {
				if _, err = s.refreshNexovccLocked(context.Background(), op.CardID, op.Serial); err != nil {
					t.Fatal(err)
				}
			}
			base := s.nexovcc.transport
			queries := 0
			transport := func(ctx context.Context, req nexovcc.Request) (nexovcc.Response, error) {
				if req.Path == "/api/card/getUserCardDetail" {
					queries++
					if req.Body["serial_no"] != op.Serial {
						t.Fatal("queried a different card")
					}
					if mode == "incomplete" {
						return nexReply(map[string]any{"serial_no": op.Serial, "status": "1"}), nil
					}
				} else if req.Path != "/api/login" && req.Path != "/api/card/getUserCardBalance" {
					t.Fatalf("unexpected endpoint %s", req.Path)
				}
				return base(ctx, req)
			}
			s.nexovcc.client = nil
			s.nexovcc.transport = transport
			if mode == "restarted" {
				s = &Server{store: s.store}
				s.nexovcc.transport = transport
			}
			if mode == "busy" {
				s.nexovcc.mu.Lock()
			}
			now := time.Now().Add(5 * time.Second)
			if mode == "not_due" {
				now = time.Now()
			}
			if err = s.retryNexovccImport(context.Background(), now); err != nil {
				t.Fatal(err)
			}
			if mode == "busy" {
				s.nexovcc.mu.Unlock()
			}
			got, err := s.store.NexovccOperation(op.ID)
			if err != nil {
				t.Fatal(err)
			}
			wantQueries := 0
			if mode == "ready" || mode == "restarted" || mode == "incomplete" {
				wantQueries = 1
			}
			if queries != wantQueries || f.opens.Load() != 1 {
				t.Fatalf("queries=%d opens=%d", queries, f.opens.Load())
			}
			if mode == "ready" || mode == "restarted" || mode == "already_imported" {
				if got.Status != "succeeded" {
					t.Fatal(got.Status)
				}
				if err = s.retryNexovccImport(context.Background(), now.Add(time.Hour)); err != nil || queries != wantQueries {
					t.Fatal("completed import repeated", err)
				}
			} else if got.Status != op.Status {
				t.Fatalf("unexpected completion %s", got.Status)
			}
		})
	}
}

func TestNexovccManualOpenWaitsForIssuedCard(t *testing.T) {
	s, f := nexFixtureServer(t)
	base := s.nexovcc.transport
	details := 0
	s.nexovcc.transport = func(ctx context.Context, req nexovcc.Request) (nexovcc.Response, error) {
		if req.Path == "/api/card/getUserCardDetail" {
			details++
			if details == 1 {
				return nexReply(map[string]any{"serial_no": "nex-test-serial", "status": "1"}), nil
			}
		}
		return base(ctx, req)
	}
	r := payCall(t, s.runNexovcc, "", "", map[string]any{"action": "open", "request_id": "nex-manualwait1", "template_id": "template", "confirm": true})
	if r.Code != 202 {
		t.Fatal(r.Code, r.Body.String())
	}
	s.nexovcc.wg.Wait()
	op, err := s.store.NexovccOperation("nex-manualwait1")
	if err != nil || op.Status != "succeeded" || f.opens.Load() != 1 || details != 2 {
		t.Fatal(op.Status, err, details)
	}
	if s.nexovcc.job.Status != "completed" {
		t.Fatal(s.nexovcc.job.Status)
	}
}
