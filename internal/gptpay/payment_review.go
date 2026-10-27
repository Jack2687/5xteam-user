package gptpay

import "strings"

const PaymentReviewMessage = "付款风控待核实：已停止自动重试，保留原账号和订单，不标记死号；请核实后手动处理"

// A payment risk rejection is not evidence that the account is deactivated.
// Keep the supplier's failed order intact, but require operator review before
// automation can continue. Never reinterpret successful or uncertain payments.
func (o Order) PaymentReviewRequired() bool {
	if o.Status != "failed" || o.SucceededAt != nil {
		return false
	}
	for _, value := range []string{o.Remote.ErrorCode, o.Remote.FailureCategory} {
		switch strings.ToLower(strings.TrimSpace(value)) {
		case "payment_risk", "payment_risk_blocked", "risk_blocked", "fraudulent", "fraud_detected":
			return true
		}
	}
	message := strings.ToLower(o.Error + " " + o.Remote.FailureReason)
	for _, phrase := range []string{"支付风控拦截", "付款风控拦截", "payment blocked by risk", "payment blocked by fraud", "payment blocked due to fraud"} {
		if strings.Contains(message, phrase) {
			return true
		}
	}
	return false
}
