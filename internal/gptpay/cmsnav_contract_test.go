package gptpay

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCMSNavTermsSnapshotAndLegacyReplay(t *testing.T) {
	var payloads []map[string]json.RawMessage
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		payloads = append(payloads, body)
		fmt.Fprint(w, `{"code":0,"data":{"taskId":"contract-task","status":"reserved"}}`)
	}))
	defer server.Close()
	cfg := Settings{Provider: CMSNav, URL: server.URL, PlanCode: "pro5", Country: "PH"}
	if ValidatePurchase(cfg) == nil {
		t.Fatal("missing explicit consent allowed")
	}
	snapshot := NewSnapshot(cfg, "fixture-key", CardSecret{}, Session{})
	if _, _, err := New().Create(context.Background(), snapshot, "unaccepted-001"); err == nil || len(payloads) != 0 {
		t.Fatal("unaccepted purchase reached upstream")
	}
	cfg.TermsAccepted = true
	snapshot = NewSnapshot(cfg, "fixture-key", CardSecret{}, Session{})
	encoded, _ := json.Marshal(snapshot)
	var replay Snapshot
	if err := json.Unmarshal(encoded, &replay); err != nil {
		t.Fatal(err)
	}
	for _, s := range []Snapshot{snapshot, replay} {
		if _, _, err := New().Create(context.Background(), s, "accepted-0001"); err != nil {
			t.Fatal(err)
		}
	}
	legacy := snapshot
	legacy.TermsAccepted = nil
	if _, _, err := New().Create(context.Background(), legacy, "legacy-000001"); err != nil {
		t.Fatal(err)
	}
	if string(payloads[0]["termsAccepted"]) != "true" || string(payloads[1]["termsAccepted"]) != "true" {
		t.Fatal("consent lost on replay")
	}
	if _, ok := payloads[2]["termsAccepted"]; ok {
		t.Fatal("legacy idempotent body changed")
	}
}

func TestCMSNavLatestOrderMetadata(t *testing.T) {
	for _, tc := range []struct {
		name, body, status, amount, currency string
		minor                                int64
	}{
		{"review beats stale processing", `{"order":{"status":"processing"},"progress":{"status":"manual_review","retryable":false,"retryHint":"contact support","paymentAmount":"8919.64","paymentAmountMinor":891964,"paymentCurrency":"PHP"}}`, "manual_review", "8919.64", "PHP", 891964},
		{"unknown beats failed", `{"order":{"status":"failed"},"progress":{"status":"submission_unknown","upstreamAmountMinor":0,"upstreamCurrency":"USD"}}`, "submission_unknown", "", "USD", 0},
		{"order aliases", `{"order":{"status":"success","upstreamAmountMinor":123,"upstreamCurrency":"JPY"},"progress":{"status":"failed","renewalStatus":"failed"}}`, "success", "", "JPY", 123},
		{"successful payment failed renewal", `{"order":{"status":"processing"},"progress":{"status":"failed","rechargeStatus":"success","renewalStatus":"failed","paymentAmountMinor":1234,"paymentCurrency":"BHD"}}`, "success", "", "BHD", 1234},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var wire cmsnavOrder
			if err := json.Unmarshal([]byte(tc.body), &wire); err != nil {
				t.Fatal(err)
			}
			r := wire.remote("fixture")
			if r.Status != tc.status || r.PaymentAmount != tc.amount || r.PaymentCurrency != tc.currency || r.PaymentAmountMinor == nil || *r.PaymentAmountMinor != tc.minor {
				t.Fatalf("bad mapping: %+v", r)
			}
			if tc.status == "manual_review" && (r.Retryable == nil || *r.Retryable || r.RetryHint != "contact support" || (Order{Status: r.Status, Remote: r}).NeedsStatusPoll()) {
				t.Fatal("review policy lost")
			}
		})
	}
}

func TestCMSNavRetryMetadataOnRejection(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		w.WriteHeader(400)
		fmt.Fprint(w, `{"errorCode":"PAYMENT_VERIFICATION_REQUIRED","retryable":false,"retryHint":"contact support"}`)
	}))
	defer server.Close()
	_, _, err := New().Create(context.Background(), Snapshot{Provider: CMSNav, URL: server.URL, Input: CreateInput{PlanCode: "pro5"}}, "rejected-001")
	e, ok := err.(*APIError)
	if !ok || e.Retryable == nil || *e.Retryable || e.RetryHint != "contact support" || e.Uncertain {
		t.Fatalf("metadata lost: %v", err)
	}
}

func TestCMSNavRetryRulesKeepCorrectableValidationFailuresUsable(t *testing.T) {
	no := false
	for _, tc := range []struct {
		code, id string
		blocked  bool
	}{
		{"INSUFFICIENT_CREDITS", "", false}, {"SESSION_INVALID", "", false},
		{"PAYMENT_VERIFICATION_REQUIRED", "", true}, {"", "real-task", true},
	} {
		o := Order{Provider: CMSNav, Status: "failed", Remote: RemoteOrder{ID: tc.id, ErrorCode: tc.code, Retryable: &no}}
		if o.RetryBlocked() != tc.blocked {
			t.Fatalf("unexpected retry policy for %+v", tc)
		}
	}
}
