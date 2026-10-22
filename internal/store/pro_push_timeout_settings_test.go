package store

import (
	"testing"
	"time"

	"chatgpt-space-merge/internal/gptpay"
	"chatgpt-space-merge/internal/model"
)

func TestProPushTimeoutSettingsUpdateInflight(t *testing.T) {
	s := transferStore(t)
	cfg := model.DefaultProSettings()
	cfg.ForceMergeEnabled, cfg.ForceMergeMinutes = true, 55
	if _, err := s.SaveProSettings(cfg, "", ""); err != nil {
		t.Fatal(err)
	}
	first := time.Now().UTC().Add(-60 * time.Minute)
	ref := model.ProDownstreamRef{Provider: "sub2", URL: "https://fixture.invalid", LoginEmail: "admin@example.com"}
	for _, name := range []string{"waiting", "opening", "paused", "merged", "deleting", "merging", "completed", "historical"} {
		email := name + "@example.com"
		if _, err := s.SaveMailAccount(model.MailAccountProfile{Email: email, ManagementScope: "pro"}, model.MailAccountCredentials{Email: email}); err != nil {
			t.Fatal(err)
		}
		if _, err := s.UpdateProAccount(email, func(p *model.MailAccountProfile) {
			p.CaptureProPushTimeout(cfg)
			p.ProPushStartedAt, p.ProPushDownstream = &first, &ref
			p.ProAuto.ID, p.ProAuto.Status, p.ProAuto.Stage = "flow-"+name, "waiting_quota", "quota"
			p.ProAuto.NextCheckAt = &first
			switch name {
			case "opening":
				p.ProPushStartedAt = nil
				p.ProAuto.Status = "running"
				p.ProAuto.Stage = "recharge"
			case "paused":
				p.ProAuto.Status = "stopped"
				p.ProAuto.StopRequested = true
			case "merged":
				p.SpaceMergedOnce = true
			case "deleting":
				p.ProDeactivation = &model.ProDeactivation{Status: "running", Stage: "credential", StartedAt: first}
			case "merging":
				p.InviteStatus = "running"
			case "completed":
				p.ProAuto.Status = "completed"
			case "historical":
				p.ProPushTimeoutEnabled = nil
			}
		}); err != nil {
			t.Fatal(err)
		}
	}
	for _, step := range []struct {
		enabled    bool
		minutes    int
		automation bool
	}{{true, 80, true}, {true, 30, false}, {false, 30, true}, {true, 90, false}} {
		cfg.ForceMergeEnabled, cfg.ForceMergeMinutes = step.enabled, step.minutes
		if step.automation {
			if _, _, err := s.SaveProAutomationSettings(cfg, gptpay.Settings{PlanCode: "pro20"}, ""); err != nil {
				t.Fatal(err)
			}
		} else if _, err := s.SaveProSettings(cfg, "", ""); err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"waiting", "opening", "paused"} {
			p, _, err := s.MailAccountCredential(name + "@example.com")
			if err != nil || p.ProPushTimeoutEnabled == nil || *p.ProPushTimeoutEnabled != step.enabled || p.ProPushTimeoutMinutes != step.minutes {
				t.Fatalf("%s did not adopt policy: %+v %v", name, p, err)
			}
			if name == "opening" {
				if p.ProPushStartedAt != nil {
					t.Fatal("opening started timer")
				}
			} else {
				if p.ProPushStartedAt == nil || !p.ProPushStartedAt.Equal(first) {
					t.Fatal("first push reset")
				}
				deadline := p.ProPushDeadline()
				if step.enabled && (deadline == nil || !deadline.Equal(first.Add(time.Duration(step.minutes)*time.Minute))) {
					t.Fatal("deadline did not follow settings")
				}
				if !step.enabled && deadline != nil {
					t.Fatal("disabled timeout still active")
				}
			}
			if p.ProPushDownstream == nil || !p.ProPushDownstream.Matches(ref) || p.ProAuto.ID != "flow-"+name {
				t.Fatal("workflow state changed")
			}
			if name != "paused" && (p.ProAuto.NextCheckAt == nil || !p.ProAuto.NextCheckAt.Equal(first)) {
				t.Fatal("existing schedule changed")
			}
			if name == "paused" && p.ProAuto.NextCheckAt != nil {
				t.Fatal("stopped clock restarted")
			}
			if name == "paused" && (p.ProAuto.Status != "stopped" || !p.ProAuto.StopRequested) {
				t.Fatal("save resumed stopped work")
			}
		}
		for _, name := range []string{"merged", "deleting", "merging", "completed", "historical"} {
			p, _, err := s.MailAccountCredential(name + "@example.com")
			if err != nil || p.ProPushTimeoutMinutes != 55 {
				t.Fatalf("terminal/historical policy changed: %s %v", name, err)
			}
			if name == "historical" && p.ProPushTimeoutEnabled != nil {
				t.Fatal("unknown history backfilled")
			}
		}
	}
	// A failure later in the settings transaction must roll back account changes.
	if _, err := s.db.Exec(`CREATE TRIGGER fail_timeout_save BEFORE UPDATE ON pro_settings BEGIN SELECT RAISE(ABORT,'fixture failure'); END`); err != nil {
		t.Fatal(err)
	}
	cfg.ForceMergeMinutes = 120
	if _, _, err := s.SaveProAutomationSettings(cfg, gptpay.Settings{PlanCode: "pro20"}, ""); err == nil {
		t.Fatal("expected save failure")
	}
	p, _, _ := s.MailAccountCredential("waiting@example.com")
	if p.ProPushTimeoutMinutes != 90 {
		t.Fatal("failed save partially updated accounts")
	}
}
