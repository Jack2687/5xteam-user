package ejh

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestClientRejectsAmbiguousResponsesWithoutRetry(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
	}{
		{"http", `{}`, 503}, {"numeric_success", `{"code":0,"data":{}}`, 200},
		{"missing_data", `{"code":"000000"}`, 200}, {"null_data", `{"code":"000000","data":null}`, 200},
		{"invalid", `not json`, 200}, {"business", `{"code":"100004","msg":"secret-card-number"}`, 200},
		{"malicious_code", `{"code":"secret-card-number"}`, 200}, {"oversized", strings.Repeat("x", (8<<20)+1), 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			up := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/v3/authenticate" {
					json.NewEncoder(w).Encode(map[string]any{"code": "000000", "data": map[string]any{"authToken": "token", "validity": 60000}})
					return
				}
				calls.Add(1)
				if r.Header.Get("Authorization") != "token" {
					t.Error("wrong authorization")
				}
				w.WriteHeader(tc.status)
				w.Write([]byte(tc.body))
			}))
			defer up.Close()
			client := New()
			client.HTTP = up.Client()
			err := client.Call(context.Background(), Settings{URL: up.URL, AppKey: "fixture"}, "fixture-secret", "card.open.info", map[string]any{}, &Card{})
			if err == nil || calls.Load() != 1 || strings.Contains(err.Error(), "secret-card-number") {
				t.Fatalf("unsafe error/retry: calls=%d error=%v", calls.Load(), err)
			}
		})
	}
}
func TestTokenValidityClampedBeforeConversion(t *testing.T) {
	for _, validity := range []any{int64(9223372036854775807), "not-a-number", int64(-1), int64(60000)} {
		up := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(map[string]any{"code": "000000", "data": map[string]any{"authToken": "token", "validity": validity}})
		}))
		c := New()
		c.HTTP = up.Client()
		if _, err := c.Token(context.Background(), Settings{URL: up.URL, AppKey: "fixture"}, "fixture-secret"); err != nil {
			t.Fatal(err)
		}
		if c.expires.After(time.Now().Add(24 * time.Hour)) {
			t.Fatal("unbounded token validity")
		}
		up.Close()
	}
}
func TestOpenAndMoneyValidation(t *testing.T) {
	for _, v := range []string{"-1", "1e2", "NaN", "1/2", "0.0000001", "9999999999999"} {
		if _, err := Money(v); err == nil {
			t.Fatal("accepted invalid money", v)
		}
	}
	for _, v := range []string{"0", "37.25", "100000.000001"} {
		if _, err := Money(v); err != nil {
			t.Fatal(err)
		}
	}
	if err := Positive("0"); err == nil {
		t.Fatal("zero accepted as payment")
	}
	if _, err := Encrypt([]byte("fixture"), "short"); err == nil {
		t.Fatal("short secret accepted")
	}
	cfg := DefaultSettings()
	for _, url := range []string{"http://host", "https://user:pass@host", "https://host?secret=x", "https://host#key"} {
		cfg.URL = url
		if _, err := Normalize(cfg); err == nil {
			t.Fatal("unsafe URL", url)
		}
	}
}
