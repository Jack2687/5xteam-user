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
		{"string_empty_data", `{"code":"000000","data":""}`, 200},
		{"string_whitespace_data", `{"code":"000000","data":"  \n\t"}`, 200},
		{"string_null_data", `{"code":"000000","data":" null "}`, 200},
		{"string_invalid_json", `{"code":"000000","data":"secret-card-number"}`, 200},
		{"string_trailing_json", `{"code":"000000","data":"{} secret-card-number"}`, 200},
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

func TestClientAcceptsObjectAndStringData(t *testing.T) {
	for _, authString := range []bool{false, true} {
		for _, businessString := range []bool{false, true} {
			name := "auth_object/business_object"
			if authString {
				name = strings.Replace(name, "auth_object", "auth_string", 1)
			}
			if businessString {
				name = strings.Replace(name, "business_object", "business_string", 1)
			}
			t.Run(name, func(t *testing.T) {
				var authCalls, businessCalls atomic.Int32
				up := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					var data any
					wrapped := businessString
					if r.URL.Path == "/api/v3/authenticate" {
						authCalls.Add(1)
						data = map[string]any{"authToken": "fixture-token", "validity": 1800000}
						wrapped = authString
					} else {
						if r.URL.Path != "/api/v3/card" || r.Header.Get("Authorization") != "fixture-token" {
							t.Error("wrong business endpoint or authorization")
						}
						if businessCalls.Add(1) == 1 {
							data = map[string]any{"abroadList": []map[string]string{{"currency": "USD", "currencyName": "美元", "balance": "12.10"}}}
						} else {
							data = Card{OrderNo: "fixture-order", Status: "1"}
						}
					}
					if wrapped {
						raw, err := json.Marshal(data)
						if err != nil {
							t.Error(err)
							return
						}
						data = " \n" + string(raw) + "\t "
					}
					json.NewEncoder(w).Encode(map[string]any{"code": "000000", "data": data})
				}))
				defer up.Close()
				c := New()
				c.HTTP = up.Client()
				cfg := Settings{URL: up.URL, AppKey: "fixture"}
				var balance struct {
					Balances []struct {
						Currency string `json:"currency"`
						Name     string `json:"currencyName"`
						Balance  string `json:"balance"`
					} `json:"abroadList"`
				}
				if err := c.Call(context.Background(), cfg, "fixture-secret", "account.balance.info", map[string]any{}, &balance); err != nil {
					t.Fatal(err)
				}
				if len(balance.Balances) != 1 || balance.Balances[0].Currency != "USD" || balance.Balances[0].Name != "美元" || balance.Balances[0].Balance != "12.10" {
					t.Fatalf("wrong balance: %+v", balance)
				}
				var card Card
				if err := c.Call(context.Background(), cfg, "fixture-secret", "card.query.info", map[string]string{"orderNo": "fixture-order"}, &card); err != nil {
					t.Fatal(err)
				}
				if card.OrderNo != "fixture-order" || card.Status != "1" {
					t.Fatalf("wrong card: %+v", card)
				}
				if authCalls.Load() != 1 || businessCalls.Load() != 2 {
					t.Fatalf("unexpected authentication or retry: auth=%d business=%d", authCalls.Load(), businessCalls.Load())
				}
			})
		}
	}
}

func TestTokenDistinguishesMalformedDataAndMissingToken(t *testing.T) {
	for _, tc := range []struct {
		name, data, want string
	}{
		{"malformed_object", `{"authToken":123}`, "认证响应格式异常，无法解析认证结果"},
		{"malformed_string", `"{\"authToken\":123}"`, "认证响应格式异常，无法解析认证结果"},
		{"missing_object", `{"validity":1800000}`, "认证未返回 authToken"},
		{"missing_string", `"{\"validity\":1800000}"`, "认证未返回 authToken"},
		{"invalid_string", `"secret-card-number"`, "卡台业务结果不是有效 JSON，结果待核对"},
		{"null_string", `"null"`, "卡台缺少业务结果，结果待核对"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			up := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Write([]byte(`{"code":"000000","data":` + tc.data + `}`))
			}))
			defer up.Close()
			c := New()
			c.HTTP = up.Client()
			token, err := c.Token(context.Background(), Settings{URL: up.URL, AppKey: "fixture"}, "fixture-secret")
			if err == nil || err.Error() != tc.want || token != "" || c.token != "" {
				t.Fatalf("unexpected authentication result: error=%v", err)
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
