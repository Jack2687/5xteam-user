package nexovcc

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestHTTPErrorReadRetryBudgetAndInterval(t *testing.T) {
	for _, status := range []int{500, 522} {
		t.Run(fmt.Sprintf("HTTP%d", status), func(t *testing.T) {
			for _, manual := range []bool{false, true} {
				for _, failures := range []int{0, 1, 2, 3} {
					t.Run(fmt.Sprintf("manual=%v/failures=%d", manual, failures), func(t *testing.T) {
						now := time.Unix(1000, 0)
						calls, waits, messages := 0, 0, 0
						c := New(Credentials{}, func(_ context.Context, r Request) (Response, error) {
							calls++
							if r.Body["card_no"] != "4242424242424242" || r.Body["page"] != 2 {
								t.Fatal("retry changed card or page")
							}
							if calls <= failures {
								return Response{Status: status}, nil
							}
							return reply(nil), nil
						})
						c.now = func() time.Time { return now }
						c.until = now.Add(time.Hour)
						c.SetRequestInterval(20 * time.Second)
						c.SetHTTP522Retry(2, 7*time.Second, func(string) { messages++ })
						c.wait = func(_ context.Context, delay time.Duration) error {
							if delay != 7*time.Second {
								t.Fatal("retry added old pacing or backoff", delay)
							}
							waits++
							now = now.Add(delay)
							return nil
						}
						ctx := context.Background()
						if manual {
							ctx = WithImmediateRequests(ctx)
						}
						_, err := c.Do(ctx, "/api/findLogCardTrades", map[string]any{"card_no": "4242424242424242", "page": 2})
						if (err != nil) != (failures > 2) || calls != min(failures+1, 3) || waits != min(failures, 2) || messages != waits {
							t.Fatal(calls, waits, messages, err)
						}
						if failures > 2 && (RetryableHTTPStatus(err) != status || !strings.Contains(err.Error(), "已停止本次请求")) {
							t.Fatal(err)
						}
					})
				}
			}
		})
	}
}

func TestHTTPErrorDoesNotReplayWritesOrOtherErrors(t *testing.T) {
	for _, status := range []int{500, 522} {
		t.Run(fmt.Sprintf("HTTP%d", status), func(t *testing.T) {
			for _, path := range []string{"/api/card/createUserCard", "/api/card/userCardWithdraw", "/api/card/userCardDeposit", "/api/card/deleteUserCard", "/api/card/lockUserCard", "/api/card/unLockUserCard", "/api/card/updateCardRemark"} {
				calls := 0
				c := New(Credentials{}, func(context.Context, Request) (Response, error) { calls++; return Response{Status: status}, nil })
				c.until = time.Now().Add(time.Hour)
				c.SetHTTP522Retry(2, time.Second, nil)
				c.wait = func(context.Context, time.Duration) error { t.Fatal("write replay wait"); return nil }
				_, err := c.Do(context.Background(), path, nil)
				if RetryableHTTPStatus(err) != status || calls != 1 {
					t.Fatal(path, calls, err)
				}
			}
			for _, response := range []Response{{Status: 429}, {Status: 503}, {Status: 200, Body: `{"code":522,"message":"business error"}`}, {Status: 200, Body: `{"code":500,"message":"business error"}`}} {
				calls := 0
				c := New(Credentials{}, func(context.Context, Request) (Response, error) { calls++; return response, nil })
				c.until = time.Now().Add(time.Hour)
				c.SetHTTP522Retry(2, time.Second, nil)
				_, err := c.Do(context.Background(), "/api/getBalanceInfo", nil)
				if err == nil || calls != 1 {
					t.Fatal(calls, err)
				}
			}
		})
	}
}

func TestHTTPErrorDisabledAndCancelledRetry(t *testing.T) {
	for _, status := range []int{500, 522} {
		t.Run(fmt.Sprintf("HTTP%d", status), func(t *testing.T) {
			for _, scenario := range []string{"disabled", "cancelled", "probe", "cooldown"} {
				calls := 0
				c := New(Credentials{}, func(context.Context, Request) (Response, error) { calls++; return Response{Status: status}, nil })
				c.until = time.Now().Add(time.Hour)
				c.SetHTTP522Retry(2, time.Second, nil)
				ctx := context.Background()
				switch scenario {
				case "disabled":
					c.SetHTTP522Retry(0, time.Second, nil)
				case "cancelled":
					c.wait = func(context.Context, time.Duration) error { return context.Canceled }
				case "probe":
					ctx = WithOutcomeProbe(ctx)
				case "cooldown":
					c.retryAt, c.retryCode = time.Now().Add(time.Minute), status
				}
				_, err := c.Do(ctx, "/api/getBalanceInfo", nil)
				want := 1
				if scenario == "cooldown" {
					want = 0
				}
				if err == nil || calls != want || (scenario == "cancelled" && !errors.Is(err, context.Canceled)) {
					t.Fatal(scenario, calls, err)
				}
			}
		})
	}
}

func TestHTTPErrorAuthRecoveryContinuesBusinessRequest(t *testing.T) {
	for _, status := range []int{500, 522} {
		t.Run(fmt.Sprintf("HTTP%d", status), func(t *testing.T) {
			now := time.Unix(1000, 0)
			calls := map[string]int{}
			c := New(Credentials{TOTPSecret: "JBSWY3DPEHPK3PXP"}, func(_ context.Context, r Request) (Response, error) {
				calls[r.Path]++
				if (r.Path == "/api/login" || r.Path == "/api/verifyGoogleCode") && calls[r.Path] == 1 {
					return Response{Status: status}, nil
				}
				if r.Path == "/api/login" {
					r := reply(map[string]any{"user_id": 1, "need_verify": true})
					r.Cookie = "test"
					return r, nil
				}
				return reply(nil), nil
			})
			c.now = func() time.Time { return now }
			c.wait = func(_ context.Context, d time.Duration) error { now = now.Add(d); return nil }
			c.SetHTTP522Retry(2, 5*time.Second, nil)
			_, err := c.Do(context.Background(), "/api/getBalanceInfo", nil)
			if err != nil || calls["/api/login"] != 2 || calls["/api/verifyGoogleCode"] != 2 || calls["/api/getBalanceInfo"] != 1 {
				t.Fatal(calls, err)
			}
		})
	}
}

func TestHTTPErrorMixedStatusesShareRetryBudget(t *testing.T) {
	statuses := []int{500, 522, 500}
	calls, waits := 0, 0
	var progress []string
	c := New(Credentials{}, func(context.Context, Request) (Response, error) {
		if calls >= len(statuses) {
			t.Fatal("switching HTTP status reset the retry budget")
		}
		status := statuses[calls]
		calls++
		return Response{Status: status}, nil
	})
	c.until = time.Now().Add(time.Hour)
	c.SetHTTP522Retry(2, 7*time.Second, func(message string) { progress = append(progress, message) })
	c.wait = func(_ context.Context, delay time.Duration) error {
		if delay != 7*time.Second {
			t.Fatal("did not use shared retry interval", delay)
		}
		waits++
		return nil
	}
	_, err := c.Do(context.Background(), "/api/getBalanceInfo", nil)
	if calls != 3 || waits != 2 || RetryableHTTPStatus(err) != 500 || !strings.Contains(err.Error(), "HTTP 500，重试 2 次后仍失败") {
		t.Fatal(calls, waits, err)
	}
	if len(progress) != 2 || !strings.Contains(progress[0], "HTTP 500") || !strings.Contains(progress[1], "HTTP 522") {
		t.Fatal("progress lost actual HTTP status", progress)
	}
}
