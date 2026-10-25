package store

import (
	"chatgpt-space-merge/internal/gptpay"
	"chatgpt-space-merge/internal/model"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestProMerchantDurableSubmissionAndSettings(t *testing.T) {
	s := transferStore(t)
	v := model.DefaultProSettings()
	v.AutoMerchantEnabled = true
	if _, _, err := s.SaveProAutomationSettings(v, gptpay.Settings{PlanCode: "pro5"}, ""); err == nil {
		t.Fatal("enabled without key")
	}
	if _, _, err := s.SaveProAutomationSettings(v, gptpay.Settings{PlanCode: "pro5"}, "", "merchant-secret"); err != nil {
		t.Fatal(err)
	}
	v.AutoMergeEnabled = true
	if _, _, err := s.SaveProAutomationSettings(v, gptpay.Settings{PlanCode: "pro5"}, "", "must-not-save"); err == nil {
		t.Fatal("accepted conflicting modes")
	}
	v.AutoMergeEnabled = false
	if _, _, err := s.SaveProAutomationSettings(v, gptpay.Settings{PlanCode: "pro5"}, "", ""); err != nil {
		t.Fatal(err)
	}
	stored, _, _, err := s.ProSettings()
	if err != nil || !stored.AutoMerchantEnabled || !stored.MerchantKeyPresent || stored.Disposition() != "merchant" {
		t.Fatal(stored, err)
	}
	email := "merchant@example.com"
	if _, err = s.SaveMailAccount(model.MailAccountProfile{Email: email}, model.MailAccountCredentials{Email: email, GptPassword: "password-secret", TotpSecret: "totp-secret"}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.UpdateMailAccountManagementScope(email, "pro"); err != nil {
		t.Fatal(err)
	}
	p, a, err := s.PrepareProMerchant(email)
	if err != nil || !p.MerchantStarted() || p.MerchantCompleted() {
		t.Fatal(p, err)
	}
	var raw, encrypted string
	if err = s.db.QueryRow("SELECT profile FROM mail_accounts WHERE email=?", email).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if err = s.db.QueryRow("SELECT encrypted_submission FROM pro_merchant_requests WHERE email=?", email).Scan(&encrypted); err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"merchant-secret", "password-secret", "totp-secret"} {
		if strings.Contains(raw, secret) || strings.Contains(encrypted, secret) {
			t.Fatal("plaintext secret")
		}
	}
	if _, _, err = s.SaveProAutomationSettings(v, gptpay.Settings{PlanCode: "pro5"}, "", "replacement-key"); err != nil {
		t.Fatal(err)
	}
	profile, creds, _ := s.MailAccountCredential(email)
	newPassword := "changed-password"
	if _, err = s.PatchMailCredentials(email, MailCredentialsPatch{Revision: s.MailCredentialsRevision(profile, creds), GPTPassword: &newPassword}); err != nil {
		t.Fatal(err)
	}
	q, b, err := s.PrepareProMerchant(email)
	if err != nil || q.ProMerchant.RequestID != p.ProMerchant.RequestID || string(a.Payload) != string(b.Payload) || b.APIKey != "merchant-secret" {
		t.Fatal("retry did not retain immutable request", err)
	}
	data, _ := json.Marshal(q)
	if strings.Contains(string(data), "password-secret") {
		t.Fatal("response leaked credentials")
	}
}

func TestProMerchantSchedulingRecoveryAndPartitions(t *testing.T) {
	s := transferStore(t)
	past := time.Now().Add(-time.Minute)
	for _, state := range []string{"pending", "completed", "stopped", "failed", "running", "paused"} {
		email := state + "@example.com"
		if _, err := s.SaveMailAccount(model.MailAccountProfile{Email: email}, model.MailAccountCredentials{Email: email}); err != nil {
			t.Fatal(err)
		}
		if _, err := s.UpdateMailAccountManagementScope(email, "pro"); err != nil {
			t.Fatal(err)
		}
		if _, err := s.UpdateProAccount(email, func(p *model.MailAccountProfile) {
			p.ProAuto = model.ProAutoState{ID: "flow", Status: "running", Steps: map[string]string{"push": "completed"}}
			p.ProMerchant = &model.ProMerchant{RequestID: "req-" + state, Status: "waiting", StartedAt: past, AcceptedAt: &past, NextCheckAt: &past}
			switch state {
			case "completed":
				p.ProMerchant.CompletedAt = &past
			case "stopped":
				p.ProAuto.StopRequested = true
			case "failed":
				p.ProMerchant.Status = "failed"
			case "running":
				p.ProWorkflowRunning = true
			case "paused":
				p.ProMigration = &model.ProMigrationState{Paused: true}
			}
		}); err != nil {
			t.Fatal(err)
		}
	}
	due, err := s.DueProMerchants(time.Now())
	if err != nil || len(due) != 1 || due[0].Email != "pending@example.com" {
		t.Fatal("incorrect due jobs", due, err)
	}
	rows, total, counts, err := s.ProAccountsPage("", "merchant", 100, 0)
	if err != nil || total != 1 || len(rows) != 1 || counts.Merchant != 1 || counts.Deleted != 0 || counts.Merged != 0 || counts.InProgress != 5 {
		t.Fatal("incorrect merchant partition", total, counts, err)
	}
	if err = s.RecoverProWorkflows(); err != nil {
		t.Fatal(err)
	}
	if _, err = s.UpdateProAccount("completed@example.com", func(p *model.MailAccountProfile) { p.CurrentPlanType = "pro"; p.ProAuto.ID = "" }); err != nil {
		t.Fatal(err)
	}
	capacity, err := s.ProCapacity()
	if err != nil || capacity.OpenedUnmerged != 0 {
		t.Fatal("completed merchant still occupies schedule population", capacity, err)
	}
	candidates, err := s.ProScheduleCandidates(100)
	if err != nil || len(candidates) != 0 {
		t.Fatal("merchant account selected for new provisioning", candidates, err)
	}
	p, _, _ := s.MailAccountCredential("completed@example.com")
	if p.ProAuto.Status != "completed" || p.ProMerchant.NextCheckAt != nil || p.DeletionAccepted() || p.SpaceMergedOnce {
		t.Fatal("completed facts changed", p)
	}
	p, _, _ = s.MailAccountCredential("stopped@example.com")
	if p.ProAuto.Status != "stopped" || p.ProMerchant.NextCheckAt != nil {
		t.Fatal("restart revived stopped flow")
	}
	p, _, _ = s.MailAccountCredential("running@example.com")
	if p.ProWorkflowRunning || p.ProAuto.Status != "awaiting_merchant" || p.ProMerchant.NextCheckAt == nil {
		t.Fatal("restart did not resume progress")
	}
}
