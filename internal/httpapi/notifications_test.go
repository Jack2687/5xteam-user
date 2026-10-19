package httpapi

import (
	"chatgpt-space-merge/internal/notify"
	"chatgpt-space-merge/internal/store"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type notificationTransport func(*http.Request) (*http.Response, error)

func (f notificationTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestNotificationDeliverySwitchesAndFailedResults(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	cfg := notify.Defaults()
	cfg.Enabled = true
	cfg.Bots = []notify.Bot{{ID: "bot", Name: "test", Enabled: true, Webhook: "https://open.feishu.cn/open-apis/bot/v2/hook/fixture"}}
	if _, err = st.SaveNotificationSettings(cfg); err != nil {
		t.Fatal(err)
	}
	calls := 0
	var text string
	s := &Server{store: st, notifications: notificationRuntime{client: &http.Client{Transport: notificationTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		var payload struct{ Content struct{ Text string } }
		_ = json.NewDecoder(r.Body).Decode(&payload)
		text = payload.Content.Text
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"code":19021}`)), Header: make(http.Header)}, nil
	})}}}
	event := notificationEvent{Kind: "restock", Title: "补货已安排", Text: "补货 2 个", At: time.Date(2026, 10, 17, 0, 0, 0, 0, time.UTC)}
	s.deliverNotification(context.Background(), event)
	logs, err := st.NotificationLogs()
	if err != nil || len(logs) != 1 || logs[0].Status != "failed" {
		t.Fatal("HTTP 200 business failure falsely marked as sent", logs, err)
	}
	if !strings.Contains(text, "2026-10-17 08:00:00") {
		t.Fatal("notification timezone not Beijing", text)
	}
	cfg.Restock = false
	if _, err = st.SaveNotificationSettings(cfg); err != nil {
		t.Fatal(err)
	}
	s.deliverNotification(context.Background(), event)
	if calls != 1 {
		t.Fatal("disabled event sent")
	}
	cfg.Restock = true
	cfg.Enabled = false
	if _, err = st.SaveNotificationSettings(cfg); err != nil {
		t.Fatal(err)
	}
	s.deliverNotification(context.Background(), event)
	if calls != 1 {
		t.Fatal("master switch ignored")
	}
}

func TestNotificationDeliveryRoutesToSelectedBots(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	cfg := notify.Settings{Enabled: true, Bots: []notify.Bot{
		{ID: "stock", Events: []string{"restock"}},
		{ID: "pro", Events: []string{"pro_start", "pro_pushed", "pro_failed"}},
		{ID: "merge", Events: []string{"merge_start", "merge_result"}},
		{ID: "card", Events: []string{"ejh_open"}},
		{ID: "empty", Events: []string{}},
		{ID: "disabled", Events: []string{"pro_start"}},
	}}
	for i := range cfg.Bots {
		b := &cfg.Bots[i]
		b.Name, b.Enabled = b.ID, b.ID != "disabled"
		b.Webhook = "https://open.feishu.cn/open-apis/bot/v2/hook/" + b.ID
	}
	if _, err = st.SaveNotificationSettings(cfg); err != nil {
		t.Fatal(err)
	}
	var recipients []string
	s := &Server{store: st, notifications: notificationRuntime{client: &http.Client{Transport: notificationTransport(func(r *http.Request) (*http.Response, error) {
		recipients = append(recipients, r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:])
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"code":0}`)), Header: make(http.Header)}, nil
	})}}}
	for _, kind := range []string{"restock", "pro_start", "pro_pushed", "pro_failed", "merge_start", "merge_result", "ejh_open", "unknown"} {
		s.deliverNotification(context.Background(), notificationEvent{Kind: kind, At: time.Now()})
	}
	if strings.Join(recipients, ",") != "stock,pro,pro,pro,merge,merge,card" {
		t.Fatal("notifications sent to wrong robots", recipients)
	}
	logs, err := st.NotificationLogs()
	if err != nil || len(logs) != 7 {
		t.Fatal("delivery log must only contain selected recipients", logs, err)
	}
}
