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

func TestPaymentReviewRestockRequiresDefiniteSupplierFailure(t *testing.T) {
	base := Order{Status: "failed", Error: "OpenAI 支付风控拦截", Remote: RemoteOrder{ID: "supplier-order", Status: "failed", SettlementStatus: "settled", ChargedCredits: 5, ReleasedCredits: 45}}
	if base.PaymentReviewBlocksRestock() || !base.PaymentReviewRequired() {
		t.Fatal("settled failure must release queue but retain account review")
	}
	for _, change := range []func(*Order){
		func(o *Order) { o.Remote.ID = "" },
		func(o *Order) { o.Remote.Status = "processing" },
		func(o *Order) { o.Remote.Status = "success" },
		func(o *Order) { o.Remote.SettlementStatus = "" },
		func(o *Order) { o.Remote.SettlementStatus = "pending" },
		func(o *Order) { o.Provider = "cmsnav"; v := false; o.Remote.Retryable = &v },
		func(o *Order) { v := false; o.Remote.Retryable = &v },
	} {
		o := base
		change(&o)
		if !o.PaymentReviewBlocksRestock() {
			t.Fatal("uncertain or prohibited order released queue", o)
		}
	}
}
