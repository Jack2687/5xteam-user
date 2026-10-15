package gptpay

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWebSessionTokenNewOrderAndImmutableReplay(t *testing.T) {
	for _, provider := range []string{Tokenseek, CMSNav} {
		for _, alias := range []string{"sessionToken", "session_token", "absent"} {
			t.Run(provider+"/"+alias, func(t *testing.T) {
				var bodies []string
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					b, _ := io.ReadAll(r.Body)
					bodies = append(bodies, string(b))
					if r.Header.Get("Idempotency-Key") != "original-order-key" {
						t.Error("idempotency key changed")
					}
					var body struct {
						Session map[string]any `json:"session"`
					}
					_ = json.Unmarshal(b, &body)
					if alias != "absent" && body.Session["sessionToken"] != "fixture-web-cookie" {
						t.Error("missing web token")
					}
					if alias == "absent" && body.Session["sessionToken"] != nil {
						t.Error("invented token")
					}
					for _, field := range []string{"session_token", "refresh_token", "refreshToken", "id_token", "access_token"} {
						if _, ok := body.Session[field]; ok {
							t.Errorf("unnecessary credential %s", field)
						}
					}
					if provider == CMSNav {
						fmt.Fprint(w, `{"code":0,"data":{"taskId":"remote","status":"processing"}}`)
					} else {
						fmt.Fprint(w, `{"code":0,"data":{"id":"remote","status":"processing"}}`)
					}
				}))
				defer server.Close()
				raw := fmt.Sprintf(`{"user":{"email":"fixture@example.com","name":"Fixture"},"account":{"id":"personal"},"accessToken":"fixture-at",%q:"fixture-web-cookie","refresh_token":"private-rt","id_token":"private-id"}`, alias)
				var session Session
				if err := json.Unmarshal([]byte(raw), &session); err != nil {
					t.Fatal(err)
				}
				snap := NewSnapshot(Settings{Provider: provider, URL: server.URL, Country: "PH", PlanCode: "pro5"}, "fixture-key", CardSecret{}, session)
				if _, _, err := New().Create(context.Background(), snap, "original-order-key"); err != nil {
					t.Fatal(err)
				}
				sealedInput, err := json.Marshal(snap)
				if err != nil {
					t.Fatal(err)
				}
				var replay Snapshot
				if err = json.Unmarshal(sealedInput, &replay); err != nil {
					t.Fatal(err)
				}
				if _, _, err = New().Create(context.Background(), replay, "original-order-key"); err != nil {
					t.Fatal(err)
				}
				if len(bodies) != 2 || bodies[0] != bodies[1] {
					t.Fatal("persisting snapshot changed request bytes")
				}
				if alias != "absent" && strings.Contains(Redact("failure: fixture-web-cookie fixture-at fixture-key", replay), "fixture-") {
					t.Fatal("credential in failure message")
				}
				// Existing snapshots never sent a session token, even if Raw had one.
				legacy := Snapshot{Provider: provider, URL: server.URL, Input: CreateInput{PlanCode: "pro5", Session: session}}
				encoded, _ := json.Marshal(legacy)
				var old Snapshot
				_ = json.Unmarshal(encoded, &old)
				if strings.Contains(string(encoded), "fixture-web-cookie") && alias != "absent" {
					t.Fatal("legacy snapshot changed")
				}
				if old.WebSessionToken != "" {
					t.Fatal("legacy opted into new wire contract")
				}
			})
		}
	}
}

func TestCMSNavAsyncFailureAndVerification(t *testing.T) {
	for _, tc := range []struct{ name, body, want, reason string }{
		{"async failure", `{"order":{"status":"processing"},"progress":{"status":"failed","retryable":true,"retryHint":"Session has expired"}}`, "failed", "Session has expired"},
		{"payment verification", `{"order":{"status":"failed"},"progress":{"status":"failed","failureCategory":"payment_verification","retryable":false}}`, "failed", "不要重复提交"},
		{"renewal failure", `{"order":{"status":"processing"},"progress":{"status":"failed","rechargeStatus":"success","renewalStatus":"failed"}}`, "success", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var v cmsnavOrder
			if err := json.Unmarshal([]byte(tc.body), &v); err != nil {
				t.Fatal(err)
			}
			got := v.remote("original")
			if got.Status != tc.want || !strings.Contains(got.FailureReason, tc.reason) {
				t.Fatalf("%+v", got)
			}
		})
	}
	e := &APIError{HTTPStatus: 400, ErrorCode: "PAYMENT_VERIFICATION_REQUIRED"}
	if !strings.Contains(e.Error(), "不要重复提交") {
		t.Fatal(e)
	}
}
