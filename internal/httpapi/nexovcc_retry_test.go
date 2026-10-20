package httpapi

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"chatgpt-space-merge/internal/nexovcc"
)

func TestNexovccHTTPErrorOpeningReconcilesWithoutReissuing(t *testing.T) {
	for _, status := range []int{500, 522} {
		t.Run(fmt.Sprintf("HTTP%d", status), func(t *testing.T) {
			for _, found := range []bool{true, false} {
				t.Run(map[bool]string{true: "found", false: "unresolved"}[found], func(t *testing.T) {
					s, f := nexFixtureServer(t)
					cfg, _, _ := s.store.NexovccSettings()
					cfg.HTTP522RetrySeconds, cfg.HTTP522Retries = 1, 2
					if _, err := s.store.SaveNexovccSettings(cfg, nexovcc.Credentials{}, false); err != nil {
						t.Fatal(err)
					}
					base := s.nexovcc.transport
					writes, queries := 0, 0
					s.nexovcc.transport = func(ctx context.Context, r nexovcc.Request) (nexovcc.Response, error) {
						if r.Path == "/api/card/createUserCard" {
							writes++
							f.remark = nexovcc.String(r.Body["remark"])
							return nexovcc.Response{Status: status}, nil
						}
						if r.Path == "/api/card/findUserCardList" {
							queries++
							if !found {
								return nexovcc.Response{Status: status}, nil
							}
							return nexReply(map[string]any{"list": []any{map[string]any{"serial_no": "nex-test-serial", "status": "1", "pid": "267", "remark": f.remark}}}), nil
						}
						return base(ctx, r)
					}
					op, err := s.openNexovccLocked(nexovcc.WithImmediateRequests(context.Background()), "nex-retry522-open1", "template", false)
					if err != nil || writes != 1 || (op.Status == "succeeded") != found || queries != map[bool]int{true: 1, false: 2}[found] {
						t.Fatal(writes, queries, op, err)
					}
					if !found && (op.Status != "unknown" || !strings.Contains(op.Error, "已停止本次请求")) {
						t.Fatal(op)
					}
					_, _ = s.openNexovccLocked(context.Background(), op.ID, "template", false)
					if writes != 1 {
						t.Fatal("original request ID issued another card")
					}
				})
			}
		})
	}
}

func TestNexovccHTTPErrorFreezeDoesNotGuessUnverifiedStatus(t *testing.T) {
	for _, status := range []int{500, 522} {
		t.Run(fmt.Sprintf("HTTP%d", status), func(t *testing.T) {
			s, f := nexFixtureServer(t)
			op, err := s.openNexovccLocked(context.Background(), "nex-freeze522-open1", "template", false)
			if err != nil {
				t.Fatal(err)
			}
			cfg, _, _ := s.store.NexovccSettings()
			cfg.HTTP522RetrySeconds, cfg.HTTP522Retries = 1, 2
			if _, err = s.store.SaveNexovccSettings(cfg, nexovcc.Credentials{}, false); err != nil {
				t.Fatal(err)
			}
			base := s.nexovcc.transport
			writes := 0
			s.nexovcc.transport = func(ctx context.Context, r nexovcc.Request) (nexovcc.Response, error) {
				if r.Path == "/api/card/lockUserCard" {
					writes++
					f.status = "2"
					return nexovcc.Response{Status: status}, nil
				}
				return base(ctx, r)
			}
			s.nexovcc.client = nil
			result, err := s.actionNexovccLocked(context.Background(), nexovccInput{Action: "freeze", RequestID: "nex-freeze522-action1", CardID: op.CardID})
			if err != nil || result.Status != "unknown" || writes != 1 || !strings.Contains(result.Error, "已停止本次请求") {
				t.Fatal(result, writes, err)
			}
		})
	}
}

func TestNexovccHTTPErrorSyncStopsAfterExhaustion(t *testing.T) {
	for _, status := range []int{500, 522} {
		t.Run(fmt.Sprintf("HTTP%d", status), func(t *testing.T) {
			s, _ := nexFixtureServer(t)
			now := time.Now()
			if err := s.store.SaveNexovccFinance(nexovcc.Finance{At: now, AccountAt: now}); err != nil {
				t.Fatal(err)
			}
			for i, serial := range []string{"first", "second"} {
				if _, err := s.store.SaveNexovccCard(nexovcc.Card{Serial: serial, Status: "1"}, &nexovcc.PrivateCard{Number: []string{"4242424242424242", "5555555555554444"}[i], CVV: "123", Month: 12, Year: 2099}); err != nil {
					t.Fatal(err)
				}
			}
			base := s.nexovcc.transport
			calls := 0
			s.nexovcc.transport = func(ctx context.Context, r nexovcc.Request) (nexovcc.Response, error) {
				if strings.Contains(r.Path, "findLog") {
					calls++
					return nexovcc.Response{Status: status}, nil
				}
				return base(ctx, r)
			}
			err := s.syncNexovccTransactionsLocked(nexovcc.WithImmediateRequests(context.Background()), "")
			if nexovcc.RetryableHTTPStatus(err) != status || calls != 1 {
				t.Fatal("sync kept sending requests after exhaustion", calls, err)
			}
		})
	}
}
