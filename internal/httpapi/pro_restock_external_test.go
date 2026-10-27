package httpapi

import (
	"chatgpt-space-merge/internal/gptpay"
	"chatgpt-space-merge/internal/model"
	"chatgpt-space-merge/internal/restock"
	"context"
	"net/http"
	"testing"
	"time"
)

func TestRestockExternalInflightTracksPaymentOutcome(t *testing.T) {
	for _, tc := range []struct {
		status, payment      string
		active, linked, want bool
	}{
		{"failed", "", false, false, false},
		{"interrupted", "", false, false, false},
		{"failed", "failed", false, true, false},
		{"failed", "historical_risk", false, true, false},
		{"failed", "risk_review", false, true, true},
		{"failed", "submission_unknown", false, true, true},
		{"stopped", "submission_unknown", false, true, true},
		{"stopped", "success", false, true, true},
		{"failed", "submission_unknown", false, false, true},
		{"running", "", true, false, true},
	} {
		t.Run(tc.status+"/"+tc.payment, func(t *testing.T) {
			s, st, email := gptPayFixture(t, func(http.ResponseWriter, *http.Request) { t.Error("unexpected supplier call") })
			s.proAutoCancel()
			s.proAutoWG.Wait()
			configureProAutoFixture(t, s)
			p, _, _, err := st.ProSettings()
			if err != nil {
				t.Fatal(err)
			}
			if tc.payment != "" {
				_, secret, err := st.GPTPayCard("test-card")
				if err != nil {
					t.Fatal(err)
				}
				order := gptpay.Order{ID: "pro-auto-external", Email: email, CardID: "test-card", Status: tc.payment, CreatedAt: time.Now()}
				if tc.payment == "historical_risk" || tc.payment == "risk_review" {
					order.Status, order.Error = "failed", "OpenAI 支付风控拦截"
				}
				if err = st.CreateGPTPayOrder(order, gptpay.Snapshot{Input: gptpay.CreateInput{CardSecret: secret}}); err != nil {
					t.Fatal(err)
				}
			}
			if _, err = st.UpdateProAccount(email, func(v *model.MailAccountProfile) {
				v.ProAuto = model.ProAutoState{ID: "external", Status: tc.status, StartedAt: time.Now(), RestockBinding: restockBinding(p)}
				v.ProAuto.PaymentReviewRequired = tc.payment == "risk_review"
				if tc.linked {
					v.ProAuto.OrderID = "pro-auto-external"
				}
			}); err != nil {
				t.Fatal(err)
			}
			if tc.active {
				s.proAutoJobs = map[string]context.CancelFunc{email: func() {}}
				defer delete(s.proAutoJobs, email)
			}
			tasks, _, err := s.restockTasks(restock.Snapshot{Binding: restockBinding(p), GroupID: 1}, p, restock.Config{GroupID: 1, LeadMinutes: 5, LeadSamples: 2})
			if err != nil {
				t.Fatal(err)
			}
			if (len(tasks) == 1) != tc.want {
				t.Fatalf("active inflight=%d want=%v", len(tasks), tc.want)
			}
			if tc.payment == "submission_unknown" && len(tasks) > 0 && tasks[0].Predictable() {
				t.Fatal("unknown payment promised future capacity")
			}
		})
	}
}
