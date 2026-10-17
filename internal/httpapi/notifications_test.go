package httpapi

import (
	"chapt-space-user/internal/notify"
	"chapt-space-user/internal/store"
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
