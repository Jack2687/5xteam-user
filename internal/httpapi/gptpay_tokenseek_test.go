package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"chatgpt-space-merge/internal/gptpay"
)

func TestTokenseekCountryQuantitySnapshotAndReplay(t *testing.T) {
	var payloads []string
	s, st, email := gptPayFixture(t, func(w http.ResponseWriter, r *http.Request) {
		var raw json.RawMessage
		_ = json.NewDecoder(r.Body).Decode(&raw)
		payloads = append(payloads, string(raw))
		var input gptpay.CreateInput
		_ = json.Unmarshal(raw, &input)
		if input.Country != "JP" || input.PlanCode != "codex_point" || input.CreditQuantity != 1250 {
			t.Error("purchase fields lost")
		}
		w.WriteHeader(503)
		fmt.Fprint(w, `{"code":50301}`)
	})
	cfg, _, _ := st.GPTPaySettings()
	cfg.PlanCode, cfg.Country, cfg.CreditQuantity = "codex_point", "JP", 1250
	if _, err := st.SaveGPTPaySettings(cfg, ""); err != nil {
		t.Fatal(err)
	}
	profile, credentials, _ := st.MailAccountCredential(email)
	credentials.ChatGPTSession = fmt.Sprintf(`{"user":{"email":%q},"account":{"id":"personal-id"},"accessToken":%q,"session_token":"fixture-original-cookie"}`, email, credentials.AccessToken)
	if _, err := st.SaveMailAccount(profile, credentials); err != nil {
		t.Fatal(err)
	}
	input := map[string]any{"request_id": "country-points-fixture", "card_id": "test-card", "plan_code": "codex_point", "provider": "tokenseek", "country": "JP", "credit_quantity": 1250}
	order := payOrder(t, payCall(t, s.createGPTPayOrder, "email", email, input))
	if order.Country != "JP" || order.CreditQuantity != 1250 {
		t.Fatal("order display fields missing")
	}
	saved, snapshot, err := st.GPTPayOrder(order.ID)
	if err != nil || saved.Country != "JP" || snapshot.Input.Country != "JP" || snapshot.Input.CreditQuantity != 1250 {
		t.Fatal("snapshot fields missing", err)
	}
	if snapshot.WebSessionToken != "fixture-original-cookie" || !strings.Contains(payloads[0], `"sessionToken":"fixture-original-cookie"`) {
		t.Fatal("web session token lost across encrypted persistence")
	}
	input["country"] = "PH"
	if rec := payCall(t, s.createGPTPayOrder, "email", email, input); rec.Code != 409 {
		t.Fatal("changed country reused idempotency key")
	}
	input["country"] = "JP"
	input["credit_quantity"] = 250
	if rec := payCall(t, s.createGPTPayOrder, "email", email, input); rec.Code != 409 {
		t.Fatal("changed point quantity reused idempotency key")
	}
	cfg.PlanCode, cfg.Country, cfg.CreditQuantity = "pro50", "CL", 0
	if _, err = st.SaveGPTPaySettings(cfg, "replacement-key"); err != nil {
		t.Fatal(err)
	}
	_ = payOrder(t, payCall(t, s.retryGPTPayOrder, "id", order.ID, map[string]any{}))
	if len(payloads) != 2 || payloads[0] != payloads[1] {
		t.Fatal("retry changed frozen input")
	}
}

func TestTokenseekNewSettingsAndEligibilityBlockBeforePayment(t *testing.T) {
	for _, tc := range []struct {
		name, plan        string
		country, quantity any
		free              bool
	}{
		{"country changed", "pro20", "EG", 0, false},
		{"points changed", "codex_point", "PH", 250, false},
		{"upgrade free", "pro5_up", "PH", 0, true},
		{"points free", "codex_point", "PH", 1000, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			s, st, email := gptPayFixture(t, func(w http.ResponseWriter, r *http.Request) { calls++ })
			cfg, _, _ := st.GPTPaySettings()
			cfg.PlanCode = tc.plan
			if tc.plan == "codex_point" {
				cfg.CreditQuantity = 1000
			}
			if _, err := st.SaveGPTPaySettings(cfg, ""); err != nil {
				t.Fatal(err)
			}
			if tc.free {
				p, c, _ := st.MailAccountCredential(email)
				c.ChatGPTSession = fmt.Sprintf(`{"accessToken":%q,"user":{"email":%q},"account":{"id":"personal-id","planType":"free"}}`, c.AccessToken, email)
				if _, err := st.SaveMailAccount(p, c); err != nil {
					t.Fatal(err)
				}
			}
			rec := payCall(t, s.createGPTPayOrder, "email", email, map[string]any{"request_id": "guard-fixture-request", "card_id": "test-card", "plan_code": tc.plan, "country": tc.country, "credit_quantity": tc.quantity})
			if rec.Code != 400 && rec.Code != 409 {
				t.Fatal(rec.Body.String())
			}
			if calls != 0 {
				t.Fatal("invalid order sent to supplier")
			}
			rows, _, _ := st.GPTPayOrders(10, 0, email)
			if len(rows) != 0 {
				t.Fatal("rejected order reserved a card")
			}
			if tc.free && !strings.Contains(rec.Body.String(), "Free") && !strings.Contains(rec.Body.String(), "Plus") {
				t.Fatal("eligibility error missing")
			}
		})
	}
}
