package store

import (
	"testing"

	"chatgpt-space-merge/internal/model"
	"chatgpt-space-merge/internal/nexovcc"
	"chatgpt-space-merge/internal/restock"
)

func TestDailyEJHMergeDuringNexovccWindow(t *testing.T) {
	s := transferStore(t)
	settings, _, _ := s.EJHSettings()
	settings.SingleActiveAccount = false
	settings.DefaultMaxAccounts = 100
	if _, err := s.SaveEJHSettings(settings, ""); err != nil {
		t.Fatal(err)
	}
	ejhCard := ejhFixtureCard(t, s)
	nexCard, err := s.SaveNexovccCard(nexovcc.Card{Serial: "nex-daily", Status: "1", Balance: "10.00"}, &nexovcc.PrivateCard{Number: "5555555555554444", CVV: "123", Month: 12, Year: 2099})
	if err != nil {
		t.Fatal(err)
	}
	c := restock.DefaultConfig()
	c.NoRestockEnabled = true
	c.NoRestockWindows = []restock.DailyWindow{{Start: "02:00", End: "09:00"}, {Start: "17:00", End: "20:00", AllowedProvider: "nexovcc"}}
	c.DailyEJHMergeEnabled, c.DailyEJHMergeTime = true, "17:30"
	if _, err = s.SaveProRestockConfig(c); err != nil {
		t.Fatal(err)
	}
	for _, row := range []struct {
		name, card, at string
		timeout        bool
	}{
		{"ejh-timed", ejhCard.ID, "17:00:00", true},
		{"ejh-untimed", ejhCard.ID, "17:29:59", false},
		{"ejh-at-deadline", ejhCard.ID, "17:30:00", false},
		{"ejh-later", ejhCard.ID, "17:30:01", false},
		{"nex-before", nexCard.ID, "17:00:00", true},
	} {
		email := row.name + "@example.test"
		transferSource(t, s, email)
		o := transferOrder(email, row.name, "success")
		_, secret, err := s.GPTPayCard(row.card)
		if err != nil {
			t.Fatal(err)
		}
		o.Order.CardID = row.card
		o.Order.CreatedAt = dailyTestTime(row.at)
		o.Order.UpdatedAt = o.Order.CreatedAt
		o.Snapshot.Input.CardSecret = secret
		if err = s.CreateGPTPayOrder(o.Order, o.Snapshot); err != nil {
			t.Fatal(err)
		}
		if _, err = s.UpdateProAccount(email, func(p *model.MailAccountProfile) {
			p.PushStatus = "completed"
			p.ProAuto = model.ProAutoState{ID: row.name, OrderID: row.name, Status: "waiting_quota", Steps: map[string]string{"push": "completed"}}
			enabled := row.timeout
			p.ProPushTimeoutEnabled = &enabled
		}); err != nil {
			t.Fatal(err)
		}
	}
	if run, err := s.CaptureDailyEJHMerge(dailyTestTime("17:29:59")); err != nil || run != nil {
		t.Fatal("early merge", run, err)
	}
	run, err := s.CaptureDailyEJHMerge(dailyTestTime("17:30:00"))
	if err != nil || run == nil || run.Count != 3 {
		t.Fatal("wrong issuer cohort", run, err)
	}
	for _, name := range []string{"ejh-timed", "ejh-untimed", "ejh-at-deadline", "ejh-later", "nex-before"} {
		p, _, _ := s.MailAccountCredential(name + "@example.test")
		want := name != "ejh-later" && name != "nex-before"
		if (p.ProDailyMerge != nil) != want {
			t.Fatal("wrong merge target", name, p.ProDailyMerge)
		}
	}
	for _, at := range []string{"17:30:01", "19:59:59", "20:00:00", "20:50:00"} {
		if run, err := s.CaptureDailyEJHMerge(dailyTestTime(at)); err != nil || run != nil {
			t.Fatal("same-day repeated cohort", at, run, err)
		}
	}
	if c.EffectiveCardProvider(dailyTestTime("18:00:00"), "ejh") != "nexovcc" {
		t.Fatal("daily capture altered card routing")
	}
}
