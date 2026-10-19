package store

import (
	"fmt"
	"reflect"
	"testing"
	"time"

	"chatgpt-space-merge/internal/gptpay"
	"chatgpt-space-merge/internal/model"
	"chatgpt-space-merge/internal/restock"
)

func saveProSortFixture(t *testing.T, s *Store, p model.MailAccountProfile) {
	t.Helper()
	p.ManagementScope = "pro"
	saved, err := s.SaveMailAccount(p, model.MailAccountCredentials{Email: p.Email})
	if err != nil {
		t.Fatal(err)
	}
	p.ID = saved.ID
	// Keep historical/import timestamps, including deliberately missing values.
	if _, err = s.db.Exec(`UPDATE mail_accounts SET profile=? WHERE email=?`, mustJSON(p), p.Email); err != nil {
		t.Fatal(err)
	}
}

func TestProAccountsSortByLifecycleBeforePagination(t *testing.T) {
	s := transferStore(t)
	base := time.Date(2026, 10, 16, 0, 0, 0, 0, time.UTC)
	for _, state := range []string{"unmerged", "in_progress", "merged"} {
		var want []string
		for i := 0; i < 13; i++ {
			entered := base.Add(time.Duration(i) * time.Minute)
			succeeded := base.Add(time.Duration(i) * time.Hour)
			if i%2 == 0 {
				entered = entered.In(time.FixedZone("UTC+8", 8*60*60))
				succeeded = succeeded.In(time.FixedZone("UTC+8", 8*60*60))
			}
			p := model.MailAccountProfile{Email: fmt.Sprintf("%s-%02d@example.com", state, i), ProManagedAt: &entered, CreatedAt: base.Add(-time.Duration(i) * time.Hour), UpdatedAt: base.Add(-time.Duration(i) * time.Minute)}
			switch state {
			case "in_progress":
				// Both automatic and manual successful activations participate.
				if i%2 == 0 {
					p.ProAuto = model.ProAutoState{ID: p.Email, Status: "waiting_quota", StageTimes: map[string]time.Time{"recharge:completed": succeeded}}
				} else {
					p.ProManualStages = map[string]model.ProStageProgress{"recharge": {Status: "completed", UpdatedAt: succeeded}}
				}
			case "merged":
				p.SpaceMergedOnce, p.SpaceMergedAt = true, &succeeded
			}
			saveProSortFixture(t, s, p)
			if state == "unmerged" {
				want = append(want, p.Email)
			} else {
				want = append([]string{p.Email}, want...)
			}
		}
		for offset := 0; offset < len(want); offset += 10 {
			items, total, _, err := s.ProAccountsPage(state+"-", state, 10, offset)
			if err != nil || total != len(want) {
				t.Fatalf("%s offset %d: total %d, err %v", state, offset, total, err)
			}
			var got []string
			for _, p := range items {
				got = append(got, p.Email)
			}
			if !reflect.DeepEqual(got, want[offset:min(offset+10, len(want))]) {
				t.Fatalf("%s offset %d: got %v, want %v", state, offset, got, want)
			}
		}
		// A later quota/status update must not alter the saved event ordering.
		if _, err := s.UpdateProAccount(want[len(want)-1], func(p *model.MailAccountProfile) { p.Label = "refreshed" }); err != nil {
			t.Fatal(err)
		}
		items, _, _, err := s.ProAccountsPage("", state, 10, 0)
		if err != nil || items[0].Email != want[0] {
			t.Fatalf("%s reordered after refresh: %v", state, err)
		}
	}
}

func TestProAccountSortMissingSuccessAndStableTies(t *testing.T) {
	s := transferStore(t)
	base := time.Date(2026, 10, 16, 0, 0, 0, 0, time.UTC)
	for _, state := range []string{"in_progress", "merged"} {
		for _, name := range []string{"b", "a", "missing", "zero"} {
			p := model.MailAccountProfile{Email: state + "-" + name + "@example.com", ProManagedAt: &base}
			at := base
			if name == "zero" {
				at = time.Time{}
			}
			if state == "in_progress" {
				p.ProAuto = model.ProAutoState{ID: name, Status: "failed"}
				if name != "missing" {
					p.ProAuto.StageTimes = map[string]time.Time{"recharge:completed": at}
				}
			} else {
				p.SpaceMergedOnce = true
				if name != "missing" {
					p.SpaceMergedAt = &at
				}
			}
			saveProSortFixture(t, s, p)
		}
		items, _, _, err := s.ProAccountsPage("", state, 10, 0)
		if err != nil {
			t.Fatal(err)
		}
		for i, name := range []string{"a", "b", "missing", "zero"} {
			if items[i].Email != state+"-"+name+"@example.com" {
				t.Fatalf("%s position %d: %s", state, i, items[i].Email)
			}
		}
	}
}

func TestProAutomaticSelectionUsesManagedTimeAndRestockClaimsSameOrder(t *testing.T) {
	s := transferStore(t)
	base := time.Date(2026, 10, 16, 0, 0, 0, 0, time.UTC)
	later := base.Add(time.Hour)
	for _, p := range []model.MailAccountProfile{
		{Email: "z-oldest@example.com", ProManagedAt: &base, CreatedAt: later},
		{Email: "a-newer@example.com", ProManagedAt: &later, CreatedAt: base.Add(-time.Hour)},
		{Email: "legacy@example.com", CreatedAt: base.Add(2 * time.Hour)},
		{Email: "unknown@example.com"},
		{Email: "busy@example.com", ProManagedAt: &base, ProAuto: model.ProAutoState{ID: "busy", Status: "running"}},
	} {
		saveProSortFixture(t, s, p)
	}
	want := []string{"z-oldest@example.com", "a-newer@example.com", "legacy@example.com", "unknown@example.com"}
	got, err := s.ProScheduleCandidates(10)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("candidate order = %v, err %v", got, err)
	}
	page, _, _, err := s.ProAccountsPage("", "unmerged", 10, 0)
	if err != nil || len(page) != len(want) {
		t.Fatalf("unmerged page: %v", err)
	}
	for i, p := range page {
		if p.Email != want[i] {
			t.Fatalf("page differs from selection: %s", p.Email)
		}
	}
	c := restock.DefaultConfig()
	c.Enabled, c.Mode, c.GroupID, c.AllowSmall = true, "auto", 1, true
	c, err = s.SaveProRestockConfig(c)
	if err != nil {
		t.Fatal(err)
	}
	tasks, err := s.ReserveProRestock(restock.Decision{ID: "oldest-first", At: time.Now(), Version: c.Version, Suggested: 2}, "target", 0)
	if err != nil || len(tasks) != 2 {
		t.Fatalf("reserve: %v %v", tasks, err)
	}
	for i, task := range tasks {
		if task.Email != want[i] {
			t.Fatalf("restock picked %s, want %s", task.Email, want[i])
		}
	}
	got, err = s.ProScheduleCandidates(10)
	if err != nil || !reflect.DeepEqual(got, want[2:]) {
		t.Fatalf("claimed accounts selected again: %v %v", got, err)
	}
}

func TestGPTPaySuccessTimeSurvivesPollingAndSortsProAccounts(t *testing.T) {
	s := transferStore(t)
	email := "paid@example.com"
	base := time.Date(2026, 10, 16, 0, 0, 0, 0, time.UTC)
	saveProSortFixture(t, s, model.MailAccountProfile{Email: email, ProManagedAt: &base})
	o := gptpay.Order{ID: "payment", Email: email, Status: "submitting", CreatedAt: base, UpdatedAt: base}
	if err := s.CreateGPTPayOrder(o, gptpay.Snapshot{}); err != nil {
		t.Fatal(err)
	}
	loaded, _, err := s.GPTPayOrder(o.ID)
	if err != nil || loaded.SucceededAt != nil {
		t.Fatal("pending order has success time", err)
	}
	o.Status = "success"
	if err = s.UpdateGPTPayOrder(o); err != nil {
		t.Fatal(err)
	}
	loaded, _, err = s.GPTPayOrder(o.ID)
	if err != nil || loaded.SucceededAt == nil || loaded.SucceededAt.IsZero() {
		t.Fatal("success time not persisted", err)
	}
	first := *loaded.SucceededAt
	later := first.Add(time.Hour)
	saveProSortFixture(t, s, model.MailAccountProfile{Email: "newer@example.com", ProManagedAt: &base, ProAuto: model.ProAutoState{ID: "newer", Status: "waiting_quota", StageTimes: map[string]time.Time{"recharge:completed": later}}})
	// Simulate status/cancellation refresh from a caller with an older snapshot.
	o.Remote.CancellationStatus = "success"
	o.SucceededAt = &later
	if err = s.UpdateGPTPayOrder(o); err != nil {
		t.Fatal(err)
	}
	loaded, _, err = s.GPTPayOrder(o.ID)
	if err != nil || loaded.SucceededAt == nil || !loaded.SucceededAt.Equal(first) {
		t.Fatal("poll changed first success time", err)
	}
	items, _, _, err := s.ProAccountsPage("", "in_progress", 10, 0)
	if err != nil || len(items) != 2 || items[0].Email != "newer@example.com" || items[1].Email != email {
		t.Fatalf("activation order changed after polling: %v %v", items, err)
	}
}
