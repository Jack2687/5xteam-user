package nexovcc

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestRequestSpacingIncludesLoginAndPagination(t *testing.T) {
	now := time.Unix(1000, 0)
	var calls []time.Time
	c := New(Credentials{}, func(_ context.Context, r Request) (Response, error) {
		calls = append(calls, now)
		if r.Path == "/api/login" {
			v := reply(map[string]any{"user_id": 1})
			v.Cookie = "session"
			return v, nil
		}
		rows := []any{}
		if r.Body["page"] == 1 {
			for i := 0; i < 100; i++ {
				rows = append(rows, map[string]any{"id": i})
			}
		}
		return reply(map[string]any{"list": rows}), nil
	})
	c.now = func() time.Time { return now }
	c.wait = func(_ context.Context, d time.Duration) error { now = now.Add(d); return nil }
	c.SetRequestInterval(3 * time.Second)
	if _, err := c.Pages(context.Background(), "/api/card/findUserCardList"); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 3 {
		t.Fatal(calls)
	}
	for i := 1; i < len(calls); i++ {
		if calls[i].Sub(calls[i-1]) < 3*time.Second {
			t.Fatal("requests burst", calls)
		}
	}
}

func TestProviderRetryAfterAndTimeoutClassification(t *testing.T) {
	for _, code := range []int{429, 500, 522} {
		for _, header := range []string{"90", time.Unix(1090, 0).UTC().Format(http.TimeFormat), "invalid", "-1"} {
			now := time.Unix(1000, 0)
			calls := 0
			c := New(Credentials{}, func(context.Context, Request) (Response, error) {
				calls++
				return Response{Status: code, RetryAfter: header}, nil
			})
			c.now = func() time.Time { return now }
			c.until = now.Add(time.Hour)
			_, err := c.Do(context.Background(), "/api/card/userCardWithdraw", nil)
			var remote *Error
			delay := 90 * time.Second
			if header == "invalid" || header == "-1" {
				delay = 30 * time.Second
			}
			if !errors.As(err, &remote) || remote.Code != code || !remote.RetryAt.Equal(now.Add(delay)) || !IsUnknown(err) {
				t.Fatal(remote, err)
			}
			_, err = c.Do(context.Background(), "/api/getBalanceInfo", nil)
			if !errors.As(err, &remote) || remote.Code != code || calls != 1 {
				t.Fatal("replayed during cooldown", calls, err)
			}
			if code == 522 && !strings.Contains(err.Error(), "连接超时") {
				t.Fatal("522 mislabeled as rate limit", err)
			}
		}
	}
}

func TestRateLimitAnd522DoNotReplayAndBackOff(t *testing.T) {
	for _, response := range []Response{{Status: 200, Body: `{"code":500,"message":"请求过于频繁，稍后重试"}`}, {Status: 429}, {Status: 500}, {Status: 522}} {
		now := time.Unix(1000, 0)
		calls := 0
		c := New(Credentials{}, func(_ context.Context, r Request) (Response, error) { calls++; return response, nil })
		c.now = func() time.Time { return now }
		c.until = now.Add(time.Hour)
		for attempt, delay := range []time.Duration{30 * time.Second, 60 * time.Second, 120 * time.Second} {
			_, err := c.Do(context.Background(), "/api/card/createUserCard", nil)
			var remote *Error
			if !errors.As(err, &remote) || !remote.RetryAt.Equal(now.Add(delay)) || calls != attempt+1 {
				t.Fatal("wrong backoff or replay", calls, err, c.RetryAt())
			}
			if (response.Status == 500 || response.Status == 522) && !IsUnknown(err) {
				t.Fatal("uncertain write became definite")
			}
			if _, err = c.Do(context.Background(), "/api/getBalanceInfo", nil); err == nil || calls != attempt+1 {
				t.Fatal("cooldown sent another request", calls, err)
			}
			now = now.Add(delay)
		}
	}
}

func TestCancelledSpacingDoesNotSubmitPayment(t *testing.T) {
	calls := 0
	c := New(Credentials{}, func(context.Context, Request) (Response, error) { calls++; return reply(nil), nil })
	c.until = time.Now().Add(time.Hour)
	c.nextRequest = time.Now().Add(time.Hour)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := c.Do(ctx, "/api/card/createUserCard", nil)
	if err == nil || calls != 0 || IsUnknown(err) {
		t.Fatal("cancelled request was sent or treated as submitted", calls, err)
	}
}

func TestManualRequestsBypassPacingAndBackoffOnlyForExplicitCall(t *testing.T) {
	now := time.Unix(1000, 0)
	calls, waits := 0, 0
	c := New(Credentials{}, func(context.Context, Request) (Response, error) {
		calls++
		return Response{Status: 522, RetryAfter: "120"}, nil
	})
	c.now = func() time.Time { return now }
	c.until = now.Add(time.Hour)
	c.nextRequest, c.retryAt = now.Add(time.Hour), now.Add(time.Hour)
	c.retryCode = 522
	c.wait = func(context.Context, time.Duration) error { waits++; return nil }
	_, err := c.Do(WithImmediateRequests(context.Background()), "/api/card/createUserCard", nil)
	if err == nil || calls != 1 || waits != 0 || !IsUnknown(err) {
		t.Fatal("manual request waited, replayed, or lost uncertainty", calls, waits, err)
	}
	if _, err = c.Do(context.Background(), "/api/getBalanceInfo", nil); err == nil || calls != 1 {
		t.Fatal("manual request disabled background backoff", calls, err)
	}
	ctx, cancel := context.WithCancel(WithImmediateRequests(context.Background()))
	cancel()
	if _, err = c.Do(ctx, "/api/card/createUserCard", nil); err == nil || calls != 1 {
		t.Fatal("manual bypass ignored cancellation", calls, err)
	}
}

func TestPacedLoginGeneratesOTPAfterWaiting(t *testing.T) {
	now := time.Unix(29, 0)
	secret := "JBSWY3DPEHPK3PXP" // Public test fixture.
	verified := false
	c := New(Credentials{TOTPSecret: secret}, func(_ context.Context, r Request) (Response, error) {
		if r.Path == "/api/login" {
			v := reply(map[string]any{"user_id": 1, "need_verify": true})
			v.Cookie = "session"
			return v, nil
		}
		if r.Path == "/api/verifyGoogleCode" {
			want, _ := TOTP(secret, now)
			if r.Body["verify_code"] != want || now.Unix() != 32 {
				t.Fatal("expired paced OTP", now)
			}
			verified = true
		}
		return reply(nil), nil
	})
	c.now = func() time.Time { return now }
	c.wait = func(_ context.Context, delay time.Duration) error { now = now.Add(delay); return nil }
	c.SetRequestInterval(3 * time.Second)
	if _, err := c.Do(context.Background(), "/api/getBalanceInfo", nil); err != nil || !verified {
		t.Fatal(err, verified)
	}
}
