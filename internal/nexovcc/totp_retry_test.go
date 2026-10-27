package nexovcc

import (
	"context"
	"testing"
	"time"
)

func TestRejectedTOTPNextIntervalBoundedAndCancellable(t *testing.T) {
	for _, mode := range []string{"success", "rejected", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			now := time.Unix(59, 0)
			codes := []string{}
			operations := 0
			waits := 0
			c := New(Credentials{TOTPSecret: "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ"}, func(_ context.Context, r Request) (Response, error) {
				if r.Path == "/api/login" {
					v := reply(map[string]any{"user_id": 1, "need_verify": 1})
					v.Cookie = "session"
					return v, nil
				}
				if r.Path == "/api/verifyGoogleCode" {
					codes = append(codes, r.Body["verify_code"].(string))
					if len(codes) == 1 || mode == "rejected" {
						return Response{Status: 200, Body: `{"code":10002,"msg":"invalid otp"}`}, nil
					}
					return reply(nil), nil
				}
				operations++
				return reply([]any{map[string]any{"money_id": 3, "balance": "10.00", "freeze_balance": "0.00"}}), nil
			})
			c.now = func() time.Time { return now }
			c.wait = func(_ context.Context, d time.Duration) error {
				waits++
				if mode == "cancel" {
					return context.Canceled
				}
				now = now.Add(d)
				return nil
			}
			_, err := c.Balance(context.Background())
			if mode == "success" {
				if err != nil || len(codes) != 2 || codes[0] == codes[1] || operations != 1 {
					t.Fatal(codes, operations, err)
				}
			} else if err == nil || operations != 0 {
				t.Fatal("failed auth performed operation", err, operations)
			}
			if waits != 1 || len(codes) > 2 {
				t.Fatal("unbounded auth retry", waits, codes)
			}
		})
	}
}
