package store

import (
	"chatgpt-space-merge/internal/model"
	"testing"
	"time"
)

func TestProPushDurationPersistsBeforeRetry(t *testing.T) {
	s := transferStore(t)
	start := time.Date(2026, 10, 21, 0, 0, 0, 0, time.UTC)
	end := start.Add(25 * time.Minute)
	email := "duration@example.com"
	saveProSortFixture(t, s, model.MailAccountProfile{Email: email, ProPushStartedAt: &start, ProManualStages: map[string]model.ProStageProgress{"merge": {StartedAt: end, Status: "failed"}}})
	p, err := s.UpdateProAccount(email, func(p *model.MailAccountProfile) { p.ProManualStages = nil; p.ProWorkflowRunning = true })
	if err != nil || p.ProPushEndedAt == nil || !p.ProPushEndedAt.Equal(end) {
		t.Fatal("retry discarded historical duration", err)
	}
	p, err = s.UpdateProAccount(email, nil)
	if err != nil || !p.ProPushEndedAt.Equal(end) {
		t.Fatal("poll moved stopped time", err)
	}
	_, err = s.SaveMailAccountToPro(model.MailAccountProfile{Email: email}, model.MailAccountCredentials{Email: email, MailPassword: "updated"})
	if err != nil {
		t.Fatal(err)
	}
	p, _, _ = s.MailAccountCredential(email)
	if p.ProPushEndedAt == nil || !p.ProPushEndedAt.Equal(end) {
		t.Fatal("credential edit discarded duration")
	}
}
