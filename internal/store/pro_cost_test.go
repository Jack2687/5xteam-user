package store

import (
	"chatgpt-space-merge/internal/model"
	"testing"
	"time"
)

func TestProCostSurvivesRestartCredentialImportAndScopeMove(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	const email = "cost@example.test"
	if _, err = s.SaveMailAccountToPro(model.MailAccountProfile{Email: email}, model.MailAccountCredentials{}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.UpdateProAccount(email, func(p *model.MailAccountProfile) { p.PushProvider, p.Sub2AccountID = "sub2", 42 }); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if _, err = s.UpdateProAccount(email, func(p *model.MailAccountProfile) {
		p.CostProvider, p.CostConnectionIdentity, p.CostDownstreamIdentity = "sub2", "connection", "42"
		p.CostDownstreamSnapshot, p.UserCostDownstreamSnapshot, p.UserCostDownstreamIdentity, p.CostCheckedAt = 12.5, 3.25, "42", &now
	}); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SaveMailAccount(model.MailAccountProfile{Email: email}, model.MailAccountCredentials{AccessToken: "updated"}); err != nil {
		t.Fatal(err)
	}
	for _, scope := range []string{"mail", "pro"} {
		if _, err = s.UpdateMailAccountManagementScope(email, scope); err != nil {
			t.Fatal(err)
		}
	}
	p, _, err := s.MailAccountCredential(email)
	if err != nil || p.CostDownstreamSnapshot != 12.5 || p.UserCostDownstreamSnapshot != 3.25 || p.UserCostDownstreamIdentity != "42" || p.CostConnectionIdentity != "connection" || p.CostCheckedAt == nil || !p.CostCheckedAt.Equal(now) {
		t.Fatal("cost snapshot lost", err)
	}
}
