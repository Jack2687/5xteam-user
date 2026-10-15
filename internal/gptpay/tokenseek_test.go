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

func TestTokenseekPlansCountriesAndQuantity(t *testing.T) {
	for _, plan := range []string{"go", "plus", "pro5", "pro20", "pro50", "pro5_up", "pro20_up", "pro50_up", "codex_point"} {
		for _, country := range []string{"PH", "EG", "CL", "US", "JP"} {
			cfg, err := NormalizeSettings(Settings{PlanCode: plan, Country: strings.ToLower(country), CreditQuantity: 1000})
			if err != nil || cfg.Country != country {
				t.Fatalf("%s/%s: %v", plan, country, err)
			}
			if plan != "codex_point" && cfg.CreditQuantity != 0 {
				t.Fatal("quantity leaked into subscription")
			}
		}
	}
	for _, plan := range []string{"pro50", "pro50_up", "go"} {
		cfg, err := NormalizeSettings(Settings{PlanCode: plan})
		expected := "PH"
		if plan == "pro50" {
			expected = "CL"
		}
		if err != nil || cfg.Country != expected {
			t.Fatalf("wrong default: %+v %v", cfg, err)
		}
	}
	for _, cfg := range []Settings{
		{Country: "EU"}, {Country: "USD"}, {PlanCode: "pro100"},
		{Provider: CMSNav, PlanCode: "go"},
		{PlanCode: "codex_point"}, {PlanCode: "codex_point", CreditQuantity: 251},
		{PlanCode: "codex_point", CreditQuantity: 250001},
	} {
		if _, err := NormalizeSettings(cfg); err == nil {
			t.Fatalf("accepted invalid config: %+v", cfg)
		}
	}
	for _, q := range []int{250, 250000} {
		if _, err := NormalizeSettings(Settings{PlanCode: "codex_point", CreditQuantity: q}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestTokenseekExtendedQuotesCreateAndStatus(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Query().Get("view") != "extended" || r.Header.Get("X-API-Key") != "fixture-key" {
			t.Error("missing extended view or auth")
		}
		switch r.URL.Path {
		case "/user":
			fmt.Fprint(w, `{"code":0,"data":{"defaultCountry":"PH","plans":[{"planCode":"go","successCredits":80,"failureCredits":20,"availableCountries":["PH","EG"],"defaultCountry":"PH","priceVersion":3}]}}`)
		case "/gpt-recharge/orders":
			var input CreateInput
			if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
				t.Error(err)
			}
			if input.PlanCode != "codex_point" || input.Country != "EG" || input.CreditQuantity != 1000 || input.Session.Account.PlanType != "plus" {
				t.Errorf("wrong purchase: %+v", input)
			}
			if !strings.Contains(string(input.Session.Raw), `"sessionToken":"private-cookie"`) {
				t.Error("web session token missing from new order")
			}
			fmt.Fprint(w, `{"code":0,"data":{"id":"order-fixture","status":"processing","country":"EG","creditQuantity":1000}}`)
		case "/gpt-recharge/orders/status":
			fmt.Fprint(w, `{"code":0,"data":{"orders":[{"id":"order-fixture","status":"success","country":"EG","creditQuantity":1000,"cancellationStatus":"not_applicable"},{"id":"legacy","status":"success","country":null}]}}`)
		default:
			t.Error("unexpected path")
		}
	}))
	defer server.Close()
	client := New()
	account, err := client.Account(context.Background(), server.URL, "fixture-key")
	if err != nil || len(account.Plans) != 1 || account.Plans[0].PriceVersion != 3 || len(account.Plans[0].AvailableCountries) != 2 {
		t.Fatalf("quote lost: %+v %v", account, err)
	}
	cfg, err := NormalizeSettings(Settings{URL: server.URL, PlanCode: "codex_point", Country: "EG", CreditQuantity: 1000})
	if err != nil {
		t.Fatal(err)
	}
	// The test server uses root paths, whereas settings normally append /api/v1.
	cfg.URL = server.URL
	var session Session
	if err = json.Unmarshal([]byte(`{"user":{"email":"fixture@example.com"},"account":{"id":"personal","planType":"plus"},"accessToken":"fixture-at","sessionToken":"private-cookie"}`), &session); err != nil {
		t.Fatal(err)
	}
	snapshot := NewSnapshot(cfg, "fixture-key", CardSecret{}, session)
	remote, _, err := client.Create(context.Background(), snapshot, "fixture-original-key")
	if err != nil || remote.Country != "EG" || remote.CreditQuantity != 1000 {
		t.Fatal(remote, err)
	}
	orders, err := client.Status(context.Background(), server.URL, "fixture-key", []string{"order-fixture", "legacy"})
	if err != nil || len(orders) != 2 || orders[1].Country != "" || orders[0].NeedsStatusPollForTest() {
		t.Fatal(orders, err)
	}
	if calls != 3 {
		t.Fatal(calls)
	}
}

func (o RemoteOrder) NeedsStatusPollForTest() bool {
	return (Order{Status: o.Status, Remote: o}).NeedsStatusPoll()
}

func TestTokenseekLegacyReplayOmitsPreviouslyUnusedCountry(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if strings.Contains(string(body), "country") || strings.Contains(string(body), "planType") || strings.Contains(string(body), "creditQuantity") {
			t.Error("legacy replay changed business parameters")
		}
		fmt.Fprint(w, `{"code":0,"data":{"id":"legacy","status":"processing"}}`)
	}))
	defer server.Close()
	var snapshot Snapshot
	raw := `{"country":"US","input":{"planCode":"pro5","session":{"user":{"email":"fixture@example.com"},"account":{"id":"personal","planType":"free"},"accessToken":"fixture-at"}}}`
	if err := json.Unmarshal([]byte(raw), &snapshot); err != nil {
		t.Fatal(err)
	}
	snapshot.URL = server.URL
	if _, _, err := New().Create(context.Background(), snapshot, "legacy-order-key"); err != nil {
		t.Fatal(err)
	}
}

func TestTokenseekEligibilityRetainsUnknownState(t *testing.T) {
	for _, tc := range []struct {
		product, subscription string
		reject                bool
	}{
		{"pro5_up", "free", true}, {"pro50_up", "pro", true}, {"pro20_up", "plus", false},
		{"codex_point", "free", true}, {"codex_point", "plus", false}, {"codex_point", "", false}, {"pro50", "free", false},
	} {
		var session Session
		session.Account.PlanType = tc.subscription
		err := ValidatePlanSession(Settings{PlanCode: tc.product}, session)
		if (err != nil) != tc.reject {
			t.Fatalf("%+v: %v", tc, err)
		}
	}
}
