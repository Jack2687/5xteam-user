package nexovcc

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

func reply(data any) Response {
	raw, _ := json.Marshal(map[string]any{"code": 0, "data": data})
	return Response{Status: 200, Body: string(raw)}
}
func TestTOTPRFC6238AndNormalization(t *testing.T) {
	for _, tc := range []struct {
		at   int64
		want string
	}{{59, "287082"}, {1111111109, "081804"}, {1111111111, "050471"}, {1234567890, "005924"}, {2000000000, "279037"}, {20000000000, "353130"}} {
		got, e := TOTP(" gezd gnbv gy3t qojq gezd gnbv gy3t qojq ", time.Unix(tc.at, 0))
		if e != nil || got != tc.want {
			t.Fatal(tc, got, e)
		}
	}
	if _, e := TOTP("!invalid", time.Now()); e == nil {
		t.Fatal("invalid secret accepted")
	}
}
func TestSessionCookieTOTPRefreshAndBoundedReplay(t *testing.T) {
	now := time.Unix(59, 0)
	logins, balance, verifications := 0, 0, 0
	expired := false
	c := New(Credentials{Login: "fixture-login", Password: "fixture-password", TOTPSecret: "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ"}, func(_ context.Context, r Request) (Response, error) {
		switch r.Path {
		case "/api/login":
			logins++
			if r.Body["tel"] != "fixture-login" || r.Cookie != "" {
				t.Fatal("bad login")
			}
			v := reply(map[string]any{"user_id": 9, "need_verify": 1, "token": "not-a-bearer"})
			v.Cookie = "pre-verify"
			return v, nil
		case "/api/verifyGoogleCode":
			verifications++
			if r.Cookie != "pre-verify" || r.Body["user_id"] != "9" || r.Body["is_login"] != true {
				t.Fatal("bad TOTP")
			}
			v := reply(nil)
			v.Cookie = "verified"
			return v, nil
		default:
			balance++
			if r.Cookie != "verified" || r.Body["user_id"] != "9" {
				t.Fatal("wrong auth")
			}
			if expired {
				return Response{Status: 200, Body: `{"code":50008}`}, nil
			}
			return reply([]any{map[string]any{"money_id": 3, "balance": "15.01", "freeze_balance": "2.00"}}), nil
		}
	})
	c.now = func() time.Time { return now }
	for i := 0; i < 2; i++ {
		b, e := c.Balance(context.Background())
		if e != nil || b.Amount != "15.01" || b.Frozen != "2.00" {
			t.Fatal(b, e)
		}
	}
	if logins != 1 || verifications != 1 {
		t.Fatal("session not cached")
	}
	now = now.Add(19 * time.Minute)
	if _, e := c.Balance(context.Background()); e != nil {
		t.Fatal(e)
	}
	if logins != 2 {
		t.Fatal("no early refresh")
	}
	expired = true
	before := balance
	if _, e := c.Balance(context.Background()); e == nil {
		t.Fatal("expiration accepted")
	}
	if balance-before != 2 || logins != 3 {
		t.Fatal("unbounded replay", balance, logins)
	}
}
func TestWriteTransportUnknownNeverReplayed(t *testing.T) {
	for _, r := range []Response{{Status: 502, Body: "bad gateway"}, {Status: 200, Body: "not json"}, {Status: 200, Body: `{"data":{}}`}, {Error: "disconnected", Uncertain: true}} {
		writes := 0
		c := New(Credentials{}, func(_ context.Context, q Request) (Response, error) {
			if q.Path == "/api/login" {
				v := reply(map[string]any{"user_id": 1})
				v.Cookie = "session"
				return v, nil
			}
			writes++
			return r, nil
		})
		_, e := c.Do(context.Background(), "/api/card/createUserCard", map[string]any{"open_num": 1})
		if e == nil || !IsUnknown(e) || writes != 1 {
			t.Fatal("write was retried or marked definite", writes, e)
		}
	}
}
func TestPagesConsumeAllAndRejectRepeatedData(t *testing.T) {
	pages := 0
	repeat := false
	c := New(Credentials{}, func(_ context.Context, r Request) (Response, error) {
		if r.Path == "/api/login" {
			v := reply(map[string]any{"user_id": "u"})
			v.Cookie = "session"
			return v, nil
		}
		pages++
		page := r.Body["page"].(int)
		if r.Body["pageSize"] != 100 {
			t.Fatal("pageSize")
		}
		rows := []any{}
		count := 100
		if page == 2 && !repeat {
			count = 1
		}
		for i := 0; i < count; i++ {
			id := fmt.Sprint(i)
			if !repeat {
				id = fmt.Sprintf("%d-%d", page, i)
			}
			rows = append(rows, map[string]any{"serial_no": id})
		}
		return reply(map[string]any{"list": rows}), nil
	})
	rows, e := c.Pages(context.Background(), "/api/card/findUserCardList")
	if e != nil || len(rows) != 101 || pages != 2 {
		t.Fatal(len(rows), pages, e)
	}
	repeat = true
	if _, e = c.Pages(context.Background(), "/api/card/findUserCardList"); e == nil {
		t.Fatal("repeated full page silently accepted")
	}
	if _, e = List(map[string]any{"unexpected": []any{}}); e == nil {
		t.Fatal("unknown envelope treated as empty")
	}
}
func TestErrorsDoNotExposeCredentialsAndMoneyExact(t *testing.T) {
	c := New(Credentials{Password: "secret-long"}, nil)
	if strings.Contains(c.safe("secret-long card 4242424242424242"), "secret-long") || strings.Contains(c.safe("4242424242424242"), "4242424242424242") {
		t.Fatal("redaction")
	}
	private := c.safe(`invalid {"cvv":"987", "expire_at_str":"12/99", "card_no":"4242 4242 4242 4242"} VCC-Web=new-cookie`)
	for _, value := range []string{"987", "12/99", "new-cookie", "4242"} {
		if strings.Contains(private, value) {
			t.Fatal("upstream error leaked private value", private)
		}
	}
	for _, bad := range []string{"-1", "NaN", "1e2", "1.001", ""} {
		if _, e := Cents(bad); e == nil {
			t.Fatal("money accepted", bad)
		}
	}
	n, e := Cents("5780.48")
	if e != nil || n != 578048 || Money(n) != "5780.48" {
		t.Fatal(n, e)
	}
}

func TestListExplicitEmptyPageAndInvalidResponses(t *testing.T) {
	for _, tc := range []struct {
		name  string
		body  string
		valid bool
	}{
		{"empty card account", `{"list":null,"total":0,"page":1,"pageSize":100,"is_export_card":false,"is_open_card_mcc":false}`, true},
		{"string zero total", `{"list":null,"total":"0"}`, true},
		{"empty array", `{"list":[],"total":0}`, true},
		{"direct empty array", `[]`, true},
		{"null without total", `{"list":null}`, false},
		{"null with cards reported", `{"list":null,"total":1}`, false},
		{"null total", `{"list":null,"total":null}`, false},
		{"malformed list", `{"list":"","total":0}`, false},
		{"missing list", `{"total":0}`, false},
		{"null data", `null`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var data any
			dec := json.NewDecoder(strings.NewReader(tc.body))
			dec.UseNumber()
			if err := dec.Decode(&data); err != nil {
				t.Fatal(err)
			}
			rows, err := List(data)
			if tc.valid {
				if err != nil || rows == nil || len(rows) != 0 {
					t.Fatalf("expected empty list, got %v, %v", rows, err)
				}
			} else if err == nil {
				t.Fatal("invalid response accepted as an empty list")
			}
		})
	}
}
