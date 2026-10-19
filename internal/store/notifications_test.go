package store

import (
	"chatgpt-space-merge/internal/notify"
	"strings"
	"testing"
)

func TestNotificationSettingsEncryptedAndPersisted(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	cfg := notify.Defaults()
	cfg.Enabled = true
	cfg.Bots = []notify.Bot{{ID: "fixture", Name: "运维", Enabled: true, Webhook: "https://open.feishu.cn/open-apis/bot/v2/hook/private-hook", Secret: "private-secret", Events: []string{"pro_start", "ejh_open"}}}
	out, err := s.SaveNotificationSettings(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if out.Bots[0].Webhook != "" || out.Bots[0].Secret != "" {
		t.Fatal("save response exposes credentials")
	}
	var sealed string
	if err = s.db.QueryRow(`SELECT sealed FROM notification_settings`).Scan(&sealed); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(sealed, "private-") {
		t.Fatal("credentials not encrypted")
	}
	s.Close()
	s, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	got, err := s.NotificationSettings()
	if err != nil || got.Bots[0].Secret != "private-secret" || !got.Enabled || strings.Join(got.Bots[0].Events, ",") != "pro_start,ejh_open" {
		t.Fatal("settings lost on restart", err)
	}
	if _, err = s.SaveNotificationSettings(out); err != nil {
		t.Fatal(err)
	}
	got, _ = s.NotificationSettings()
	if got.Bots[0].Webhook != cfg.Bots[0].Webhook {
		t.Fatal("blank credential overwrite")
	}
	out.Bots[0].Events = []string{}
	if _, err = s.SaveNotificationSettings(out); err != nil {
		t.Fatal(err)
	}
	got, err = s.NotificationSettings()
	if err != nil || got.Bots[0].Events == nil || len(got.Public().Bots[0].Events) != 0 {
		t.Fatal("empty subscriptions lost", err)
	}
}

func TestEJHRechargeMonthDoesNotChangeMoneyOrEvents(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	before, err := s.SaveEJHRechargeLedger("3000")
	if err != nil {
		t.Fatal(err)
	}
	events, _ := EJHRecords[EJHRechargeEvent](s, "finance_recharge")
	after, err := s.SaveEJHRechargeHistoryMonth("2026-10")
	if err != nil {
		t.Fatal(err)
	}
	if after.Amount != "3000.00" || after.HistoryMonth != "2026-10" || !after.UpdatedAt.Equal(*before.UpdatedAt) {
		t.Fatal("month assignment changed ledger", after)
	}
	newEvents, _ := EJHRecords[EJHRechargeEvent](s, "finance_recharge")
	if len(events) != len(newEvents) {
		t.Fatal("month assignment recorded another recharge")
	}
	if _, err = s.SaveEJHRechargeHistoryMonth("2026-13"); err == nil {
		t.Fatal("invalid month accepted")
	}
	updated, err := s.SaveEJHRechargeLedger("3100")
	if err != nil || updated.HistoryMonth != "2026-10" {
		t.Fatal("new recharge lost historical attribution", err)
	}
}
