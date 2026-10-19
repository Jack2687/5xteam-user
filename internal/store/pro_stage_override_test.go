package store

import (
	"chatgpt-space-merge/internal/model"
	"errors"
	"testing"
)

func TestProStageOverrideVersionAndRestart(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	const email = "override@example.com"
	if _, err = s.SaveMailAccount(model.MailAccountProfile{Email: email}, model.MailAccountCredentials{}); err != nil {
		t.Fatal(err)
	}
	p, err := s.UpdateMailAccountManagementScope(email, "pro")
	if err != nil {
		t.Fatal(err)
	}
	saved, err := s.SetProStageOverride(email, "quota", "completed", p.UpdatedAt)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetProStageOverride(email, "quota", "failed", p.UpdatedAt); !errors.Is(err, ErrProStageConflict) {
		t.Fatal("stale write accepted", err)
	}
	if _, err = s.SaveMailAccount(model.MailAccountProfile{Email: email}, model.MailAccountCredentials{AccessToken: "new-at"}); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	p, _, err = s.MailAccountCredential(email)
	if err != nil {
		t.Fatal(err)
	}
	if p.ProStageOverrides["quota"].Status != saved.ProStageOverrides["quota"].Status || !p.ProStageOverrides["quota"].UpdatedAt.Equal(saved.ProStageOverrides["quota"].UpdatedAt) || p.ProStages().Steps["quota"] != "completed" || p.ProAuto.ID != "" {
		t.Fatal("save/restart lost edit or created automation")
	}
}
