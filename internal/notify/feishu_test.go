package notify

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestFeishuSignatureAndDeliveryResults(t *testing.T) {
	if Sign(1599360473, "test-secret") != "wSds2BzzFIIGf/WrhUO+NI1q/9j+FRJd3JNHKAq0NZY=" {
		t.Fatal("signature differs from Feishu/transit-hub protocol")
	}
	for _, test := range []struct {
		body   string
		status int
		ok     bool
	}{{`{"code":0}`, 200, true}, {`{"StatusCode":0}`, 200, true}, {`{"code":19021,"msg":"sensitive provider text"}`, 200, false}, {`{}`, 200, false}, {`not json`, 200, false}, {`{"code":0}`, 500, false}, {`{"code":0}`, 302, false}} {
		bot := Bot{Webhook: "https://open.feishu.cn/open-apis/bot/v2/hook/test-hook", Secret: "test-secret"}
		client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if r.Method != "POST" || r.Header.Get("Content-Type") != "application/json" {
				t.Fatal("invalid HTTP request")
			}
			var payload struct {
				Timestamp, Sign string
				MsgType         string `json:"msg_type"`
				Content         struct{ Text string }
			}
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatal(err)
			}
			ts, _ := strconv.ParseInt(payload.Timestamp, 10, 64)
			if payload.Sign != Sign(ts, bot.Secret) || payload.MsgType != "text" || payload.Content.Text != "fixture" {
				t.Fatalf("invalid notification payload: %+v", payload)
			}
			return &http.Response{StatusCode: test.status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(test.body))}, nil
		})}
		err := Send(context.Background(), client, bot, "fixture")
		if (err == nil) != test.ok {
			t.Fatalf("body %s: %v", test.body, err)
		}
		if err != nil && strings.Contains(err.Error(), "sensitive") {
			t.Fatal("provider response leaked")
		}
	}
}
func TestNotificationSettingsPreserveSecretsByIdentity(t *testing.T) {
	old := Settings{Bots: []Bot{{ID: "a", Name: "A", Webhook: "https://open.feishu.cn/open-apis/bot/v2/hook/a", Secret: "a-secret"}, {ID: "b", Name: "B", Webhook: "https://open.feishu.cn/open-apis/bot/v2/hook/b", Secret: "b-secret"}}}
	input := old.Public()
	input.Bots[0], input.Bots[1] = input.Bots[1], input.Bots[0]
	got, err := MergeSettings(input, old)
	if err != nil {
		t.Fatal(err)
	}
	if got.Bots[0].Secret != "b-secret" || got.Bots[0].Webhook != old.Bots[1].Webhook {
		t.Fatal("reordering lost credentials")
	}
	input = got.Public()
	input.Bots[0].ClearSecret = true
	got, err = MergeSettings(input, got)
	if err != nil || got.Bots[0].Secret != "" || got.Bots[1].Secret != "a-secret" {
		t.Fatal("clear secret changed another bot", err)
	}
	for _, bad := range []string{"http://open.feishu.cn/open-apis/bot/v2/hook/a", "https://evil.example/open-apis/bot/v2/hook/a", "https://open.feishu.cn@evil.example/open-apis/bot/v2/hook/a", "https://open.feishu.cn/open-apis/bot/v2/hook/a?secret=bad", "https://open.feishu.cn/other"} {
		if ValidateWebhook(bad) == nil {
			t.Fatal("invalid webhook accepted", bad)
		}
	}
	pub := old.Public()
	if old.Bots[0].Secret == "" || pub.Bots[0].Secret != "" || pub.Bots[0].Webhook != "" || !pub.Bots[0].SecretPresent {
		t.Fatal("public projection modified or exposed secrets")
	}
}
