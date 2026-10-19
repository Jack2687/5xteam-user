package store

import (
	"chatgpt-space-merge/internal/model"
	"testing"
	"time"
)

func TestDueProAutomationComparesInstantsAcrossTimezones(t *testing.T) {
	s := transferStore(t)
	email := "due@example.com"
	transferSource(t, s, email)
	now := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	for _, offset := range []int{8, -7, 0} {
		for _, due := range []bool{true, false} {
			next := now.Add(-time.Minute)
			if !due {
				next = now.Add(time.Minute)
			}
			next = next.In(time.FixedZone("fixture", offset*3600))
			if _, err := s.UpdateProAccount(email, func(p *model.MailAccountProfile) {
				p.ProAuto = model.ProAutoState{ID: "due", Status: "waiting_quota", NextCheckAt: &next}
			}); err != nil {
				t.Fatal(err)
			}
			items, err := s.DueProAutomations(now)
			if err != nil {
				t.Fatal(err)
			}
			if (len(items) == 1) != due {
				t.Errorf("offset %d due=%v: %d tasks", offset, due, len(items))
			}
		}
	}
}
