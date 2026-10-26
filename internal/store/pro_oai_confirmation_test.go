package store

import (
	"strings"
	"testing"
	"time"

	"chatgpt-space-merge/internal/model"
)

func TestProOAISessionIsEncryptedAndGenerationGuarded(t *testing.T) {
	s := transferStore(t)
	email := "private@example.com"
	transferSource(t, s, email)
	_, err := s.UpdateProAccount(email, func(p *model.MailAccountProfile) {
		p.ProOAIConfirmations = map[string]*model.ProOAIConfirmation{"merge": {Mode: "merge", Status: "running", Generation: 4}}
	})
	if err != nil {
		t.Fatal(err)
	}
	snapshot := map[string]any{"email": email, "proxy": "http://secret:password@proxy.invalid", "cookies": "private-cookie-value"}
	if err = s.SaveProOAISession(email, "merge", 4, snapshot); err != nil {
		t.Fatal(err)
	}
	var raw, profile string
	if err = s.db.QueryRow("SELECT encrypted_session FROM pro_oai_sessions WHERE email=?", email).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if err = s.db.QueryRow("SELECT profile FROM mail_accounts WHERE email=?", email).Scan(&profile); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(raw, "private-cookie-value") || strings.Contains(profile, "private-cookie-value") || strings.Contains(profile, "secret:password") {
		t.Fatal("private session exposed")
	}
	got, err := s.ProOAISession(email, "merge")
	if err != nil || got["cookies"] != "private-cookie-value" {
		t.Fatal("cannot recover encrypted session", err)
	}
	s.UpdateProAccount(email, func(p *model.MailAccountProfile) { p.ProOAIConfirmations["merge"].Generation++ })
	if err = s.SaveProOAISession(email, "merge", 4, map[string]any{"cookies": "late"}); err == nil {
		t.Fatal("stale generation wrote cookies")
	}
	got, _ = s.ProOAISession(email, "merge")
	if got["cookies"] != "private-cookie-value" {
		t.Fatal("old worker overwrote current session")
	}
}

func completeOAIPrerequisite(p *model.MailAccountProfile, mode string) {
	now := time.Now()
	switch mode {
	case "merge":
		p.InviteStatus, p.AcceptStatus, p.TransferStatus, p.RemoveStatus = "completed", "completed", "completed", "completed"
		p.SpaceMergedOnce = true
	case "deactivate":
		p.ProDeactivation = &model.ProDeactivation{Status: "completed", SubmittedAt: &now, AcceptedAt: &now, ConfirmedAt: &now}
	case "merchant":
		p.ProMerchant = &model.ProMerchant{RequestID: "test", Status: "completed", CompletedAt: &now, RetiredAt: &now}
	case "merge_v2":
		p.ProMergeV2 = &model.ProMergeV2{Status: "completed", CompletedAt: &now}
	}
}

func TestProOAIConfirmationCompletionAndHistory(t *testing.T) {
	for _, mode := range model.ProDispositionModes {
		t.Run(mode, func(t *testing.T) {
			s := transferStore(t)
			c := nexStoreCard(t, s)
			email := "confirmation@example.com"
			transferSource(t, s, email)
			o := transferOrder(email, "oai-order", "success")
			o.Order.CardID = c.ID
			if err := s.CreateGPTPayOrder(o.Order, o.Snapshot); err != nil {
				t.Fatal(err)
			}
			cfg := model.DefaultProSettings()
			cfg.OAIConfirmation[mode] = model.ProOAIConfirmationRule{Enabled: true, DelaySeconds: 301}
			if _, err := s.SaveProSettings(cfg, "", ""); err != nil {
				t.Fatal(err)
			}
			p, err := s.UpdateProAccount(email, func(p *model.MailAccountProfile) { completeOAIPrerequisite(p, mode) })
			if err != nil {
				t.Fatal(err)
			}
			confirmation := p.ProOAIConfirmations[mode]
			if confirmation == nil || confirmation.Status != "waiting" || confirmation.NotBefore == nil || confirmation.NotBefore.Sub(confirmation.CompletedAt) != 301*time.Second || confirmation.ConfirmedAt != nil {
				t.Fatal("completion must only record a delayed, unconfirmed intent", confirmation)
			}
			before := mustJSON(confirmation)
			cfg.OAIConfirmation[mode] = model.ProOAIConfirmationRule{DelaySeconds: 1}
			if _, err = s.SaveProSettings(cfg, "", ""); err != nil {
				t.Fatal(err)
			}
			if _, err = s.UpdateProAccount(email, func(p *model.MailAccountProfile) { p.QuotaStatus = "completed" }); err != nil {
				t.Fatal(err)
			}
			p, credentials, err := s.MailAccountCredential(email)
			if err != nil || mustJSON(p.ProOAIConfirmations[mode]) != before {
				t.Fatal("settings/refresh changed existing intent", err)
			}
			p.ProOAIConfirmations = nil
			if p, err = s.SaveMailAccount(p, credentials); err != nil || mustJSON(p.ProOAIConfirmations[mode]) != before {
				t.Fatal("credential save lost confirmation", err)
			}
			for _, remove := range []bool{false, true} {
				if remove {
					if _, err = s.db.Exec("DELETE FROM mail_accounts WHERE email=?", email); err != nil {
						t.Fatal(err)
					}
				}
				ejhRows, err := s.EJHCasesForEmails([]string{email})
				if err != nil || len(ejhRows) != 1 || mustJSON(ejhRows[0].ProOAIConfirmations[mode]) != before {
					t.Fatal("EJH return projection lost confirmation", err)
				}
				nexRows, err := s.NexovccCases()
				if err != nil || len(nexRows) != 1 || mustJSON(nexRows[0].ProOAIConfirmations[mode]) != before {
					t.Fatal("Global return projection lost confirmation", err)
				}
				if ejhRows[0].State == "returned" || nexRows[0].State == "returned" {
					t.Fatal("OAI intent fabricated a card refund")
				}
			}
		})
	}
}

func TestProOAIConfirmationDoesNotBackfillOrRepeat(t *testing.T) {
	s := transferStore(t)
	email := "legacy@example.com"
	transferSource(t, s, email)
	// Simulate an already completed profile created before this feature.
	p, _, _ := s.MailAccountCredential(email)
	completeOAIPrerequisite(&p, "merge")
	completeOAIPrerequisite(&p, "deactivate")
	if _, err := s.db.Exec("UPDATE mail_accounts SET profile=? WHERE email=?", mustJSON(p), email); err != nil {
		t.Fatal(err)
	}
	cfg := model.DefaultProSettings()
	cfg.OAIConfirmation["merge"] = model.ProOAIConfirmationRule{Enabled: true, DelaySeconds: 300}
	cfg.OAIConfirmation["deactivate"] = model.ProOAIConfirmationRule{Enabled: true, DelaySeconds: 300}
	if _, err := s.SaveProSettings(cfg, "", ""); err != nil {
		t.Fatal(err)
	}
	p, err := s.UpdateProAccount(email, func(p *model.MailAccountProfile) { p.QuotaStatus = "completed" })
	if err != nil || len(p.ProOAIConfirmations) != 0 {
		t.Fatal("unrelated write backfilled historical confirmations", err)
	}
	for _, running := range []bool{true, false} {
		p, err = s.UpdateProAccount(email, func(p *model.MailAccountProfile) { p.ProWorkflowRunning = running })
		if err != nil || len(p.ProOAIConfirmations) != 0 {
			t.Fatal("unrelated job flag backfilled historical confirmations", err)
		}
	}
	for _, mode := range []string{"merchant", "merge_v2"} {
		p, err = s.UpdateProAccount(email, func(p *model.MailAccountProfile) { completeOAIPrerequisite(p, mode) })
		if err != nil || p.ProOAIConfirmations[mode].Status != "disabled" {
			t.Fatal("default enabled an inquiry", err)
		}
	}
	if len(p.ProOAIConfirmations) != 2 {
		t.Fatal("different modes overwrote one another")
	}
}
