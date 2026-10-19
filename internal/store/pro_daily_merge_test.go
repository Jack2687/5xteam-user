package store

import (
	"chatgpt-space-merge/internal/model"
	"chatgpt-space-merge/internal/restock"
	"sync"
	"testing"
	"time"
)

func dailyTestTime(value string) time.Time {
	t, _ := time.Parse(time.RFC3339, "2026-10-16T"+value+"+08:00")
	return t
}

func TestDailyEJHMergeSnapshotOnceAndLifecycle(t *testing.T) {
	s := transferStore(t)
	ensureIssuerFixtureEnabled(t, s, "ejh")
	settings, _, _ := s.EJHSettings()
	settings.SingleActiveAccount, settings.DefaultMaxAccounts = false, 100
	if _, err := s.SaveEJHSettings(settings, ""); err != nil {
		t.Fatal(err)
	}
	card := ejhFixtureCard(t, s)
	c := restock.DefaultConfig()
	c.NoRestockEnabled, c.NoRestockFallback = true, true
	c.NoRestockWindows = []restock.DailyWindow{{Start: "17:00", End: "20:50"}}
	c.DailyEJHMergeEnabled, c.DailyEJHMergeTime = true, "17:30"
	if _, err := s.SaveProRestockConfig(c); err != nil {
		t.Fatal(err)
	}
	add := func(name, at string, change func(*model.MailAccountProfile)) string {
		t.Helper()
		email := name + "@example.test"
		transferSource(t, s, email)
		o := transferOrder(email, name, "success")
		o.Order.CardID, o.Order.CreatedAt, o.Order.UpdatedAt = card.ID, dailyTestTime(at), dailyTestTime(at)
		if err := s.CreateGPTPayOrder(o.Order, o.Snapshot); err != nil {
			t.Fatal(err)
		}
		_, err := s.UpdateProAccount(email, func(p *model.MailAccountProfile) {
			p.PushStatus = "completed"
			p.ProAuto = model.ProAutoState{ID: name, OrderID: name, Status: "waiting_quota", Steps: map[string]string{"push": "completed"}}
			disabled := false
			p.ProPushTimeoutEnabled = &disabled
			if change != nil {
				change(p)
			}
		})
		if err != nil {
			t.Fatal(err)
		}
		return email
	}
	before := add("before", "17:29:59", nil)
	add("boundary", "17:30:00", nil)
	add("late", "17:30:01", nil)
	add("stopped", "16:00:00", func(p *model.MailAccountProfile) { p.ProAuto.StopRequested = true })
	add("merged", "16:00:00", func(p *model.MailAccountProfile) { p.TransferStatus = "completed"; p.RemoveStatus = "completed" })
	add("manual", "16:00:00", func(p *model.MailAccountProfile) { p.ProAuto = model.ProAutoState{} })
	add("opening", "17:29:00", func(p *model.MailAccountProfile) {
		p.PushStatus = ""
		p.ProAuto.Status = "running"
		p.ProWorkflowRunning = true
	})
	add("removed", "16:00:00", func(p *model.MailAccountProfile) { p.RemoveStatus = "team_removed" })
	add("unknown-time", "16:00:00", nil)
	if _, err := s.db.Exec(`UPDATE ejh_cases SET profile=json_remove(profile,'$.opened_at') WHERE id='unknown-time'`); err != nil {
		t.Fatal(err)
	}
	if run, err := s.CaptureDailyEJHMerge(dailyTestTime("17:29:59")); err != nil || run != nil {
		t.Fatal("ran early", run, err)
	}
	run, err := s.CaptureDailyEJHMerge(dailyTestTime("17:30:05"))
	if err != nil || run == nil || run.Count != 4 {
		t.Fatal("incorrect snapshot", run, err)
	}
	for _, name := range []string{"before", "boundary", "manual", "opening"} {
		p, _, _ := s.MailAccountCredential(name + "@example.test")
		if !p.DailyMergeMatches() {
			t.Fatal("missing eligible account", name)
		}
	}
	for _, name := range []string{"late", "stopped", "merged", "removed", "unknown-time"} {
		p, _, _ := s.MailAccountCredential(name + "@example.test")
		if p.ProDailyMerge != nil {
			t.Fatal("included excluded account", name)
		}
	}
	// Later newly recorded successes cannot expand the already claimed day.
	add("new", "18:00:00", nil)
	add("late-record", "17:00:00", nil)
	c, _ = s.ProRestockConfig()
	c.DailyEJHMergeTime = "19:00"
	if _, err = s.SaveProRestockConfig(c); err != nil {
		t.Fatal(err)
	}
	if next, err := s.CaptureDailyEJHMerge(dailyTestTime("20:00:00")); err != nil || next != nil {
		t.Fatal("duplicate capture", next, err)
	}
	latest, _ := s.LatestDailyEJHMerge()
	if latest.Count != 4 {
		t.Fatal("snapshot changed")
	}
	p, _, _ := s.MailAccountCredential(before)
	if !s.DailyEJHMergeCurrent(p) {
		t.Fatal("same lifecycle rejected")
	}
	// Editing credentials must not erase an already registered request.
	if _, err = s.SaveMailAccount(model.MailAccountProfile{Email: before}, model.MailAccountCredentials{Email: before}); err != nil {
		t.Fatal(err)
	}
	p, _, _ = s.MailAccountCredential(before)
	if p.ProDailyMerge == nil {
		t.Fatal("edit erased daily request")
	}
	p.ProAuto.ID = "new-automation"
	if s.DailyEJHMergeCurrent(p) {
		t.Fatal("new lifecycle inherited old request")
	}
	// A newer successful order invalidates a manually managed request too.
	p, _, _ = s.MailAccountCredential("manual@example.test")
	o := transferOrder(p.Email, "manual-new", "success")
	o.Order.CardID = card.ID
	o.Order.CreatedAt = dailyTestTime("18:00:00")
	o.Order.UpdatedAt = o.Order.CreatedAt
	if err = s.CreateGPTPayOrder(o.Order, o.Snapshot); err != nil {
		t.Fatal(err)
	}
	if s.DailyEJHMergeCurrent(p) {
		t.Fatal("new order inherited old request")
	}
}

func TestDailyEJHMergeEmptyRestartConcurrentAndNextDay(t *testing.T) {
	dir := t.TempDir()
	a, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	c := restock.DefaultConfig()
	c.DailyEJHMergeEnabled = true
	c.DailyEJHMergeTime = "17:30"
	if _, err = a.SaveProRestockConfig(c); err != nil {
		t.Fatal(err)
	}
	b, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	var wg sync.WaitGroup
	var mu sync.Mutex
	claimed := 0
	for _, s := range []*Store{a, b, a, b} {
		wg.Add(1)
		go func(s *Store) {
			defer wg.Done()
			run, e := s.CaptureDailyEJHMerge(dailyTestTime("17:30:00"))
			if e != nil {
				t.Error(e)
			}
			if run != nil {
				mu.Lock()
				claimed++
				mu.Unlock()
			}
		}(s)
	}
	wg.Wait()
	if claimed != 1 {
		t.Fatal("day claimed more than once", claimed)
	}
	reopened, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if run, e := reopened.CaptureDailyEJHMerge(dailyTestTime("20:50:00")); e != nil || run != nil {
		t.Fatal("empty snapshot lost across restart", run, e)
	}
	if run, e := reopened.CaptureDailyEJHMerge(dailyTestTime("17:30:00").Add(24 * time.Hour)); e != nil || run == nil || run.Day != "2026-10-17" {
		t.Fatal("next day not admitted", run, e)
	}
}
