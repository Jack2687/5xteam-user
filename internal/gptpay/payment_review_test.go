package gptpay

import (
	"testing"
	"time"
)

func TestPaymentReviewClassification(t *testing.T) {
	now := time.Now()
	for _, tc := range []struct {
		name   string
		order  Order
		review bool
	}{
		{"supplier_message", Order{Status: "failed", Error: "OpenAI 支付风控拦截，请稍后重试或更换卡。"}, true},
		{"structured_code", Order{Status: "failed", Remote: RemoteOrder{ErrorCode: "PAYMENT_RISK_BLOCKED"}}, true},
		{"remote_reason", Order{Status: "failed", Remote: RemoteOrder{FailureReason: "Payment blocked by fraud screening"}}, true},
		{"network", Order{Status: "failed", Error: "CONNECT tunnel failed, response 504"}, false},
		{"ordinary_decline", Order{Status: "failed", Error: "card declined"}, false},
		{"insufficient_funds", Order{Status: "failed", Error: "insufficient funds"}, false},
		{"success_with_old_error", Order{Status: "success", Error: "支付风控拦截"}, false},
		{"unknown_with_old_error", Order{Status: "submission_unknown", Error: "支付风控拦截"}, false},
		{"once_paid", Order{Status: "failed", Error: "支付风控拦截", SucceededAt: &now}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.order.PaymentReviewRequired() != tc.review {
				t.Fatal(tc.order)
			}
		})
	}
}
