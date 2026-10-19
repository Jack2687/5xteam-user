package store

import (
	"chatgpt-space-merge/internal/model"
	"crypto/sha256"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestProVMSettingsSecretsBindingsAndMigration(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { s.Close() }()
	v := model.DefaultProSettings()
	v.Provider = "vm2api"
	v.VM2API.URL = "https://vm.example"
	v.VM2API.RuntimeType = "kvm"
	v.VM2API.VCPUs = 4
	v.VM2API.DiskGB = 40
	if _, err = s.SaveProSettings(v, "sub-password", "cpa-key", "same-initial-key"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.SaveVM2APISettings(v.VM2API, "same-initial-key"); err != nil {
		t.Fatal(err)
	}
	const email = "pro@example.com"
	if _, err = s.SaveMailAccount(model.MailAccountProfile{Email: email}, model.MailAccountCredentials{Email: email, AccessToken: "at", RefreshToken: "rt"}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.UpdateMailAccountManagementScope(email, "pro"); err != nil {
		t.Fatal(err)
	}
	target := model.DownstreamTarget{Scope: "pro", Provider: "vm2api", VM2API: v.VM2API, Secret: "same-initial-key"}
	id, err := s.SaveDownstreamTarget(target)
	if err != nil {
		t.Fatal(err)
	}
	target.Scope = ""
	teamID, err := s.SaveDownstreamTarget(target)
	if err != nil {
		t.Fatal(err)
	}
	if teamID == id {
		t.Fatal("Pro and Team must not share mutable credential snapshots")
	}
	assignment := "space-" + strings.Repeat("a", 48)
	hash := sha256.Sum256([]byte(assignment))
	now := time.Now()
	_, err = s.UpdateProAccount(email, func(p *model.MailAccountProfile) {
		p.PushProvider, p.PushStatus = "vm2api", "completed"
		p.VM2 = &model.ProVM2Binding{ConnectionID: id, AssignmentID: assignment, SlotID: fmt.Sprintf("space-%x", hash[:12]), RuntimeType: "kvm", ReadyAt: &now, Stage: "ready", Running: true, Schedulable: true}
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SaveProSettings(v, "", "", "rotated-pro-key"); err != nil {
		t.Fatal(err)
	}
	if err = s.RotateDownstreamSecret("vm2api", v.VM2API.URL, "", "rotated-team-key"); err != nil {
		t.Fatal(err)
	}
	proTarget, _ := s.DownstreamTarget(id)
	teamTarget, _ := s.DownstreamTarget(teamID)
	if proTarget.Secret != "rotated-pro-key" || teamTarget.Secret != "rotated-team-key" {
		t.Fatal("scope credential rotation crossed bindings")
	}
	var encrypted string
	if err = s.db.QueryRow("SELECT encrypted_key FROM pro_vm2api_secret WHERE id=1").Scan(&encrypted); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(encrypted, "rotated-pro-key") {
		t.Fatal("unencrypted management key")
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	saved, password, cpa, err := s.ProSettings()
	if err != nil {
		t.Fatal(err)
	}
	_, secret, err := s.ProVM2APISettings()
	if err != nil || saved.Provider != "vm2api" || saved.VM2API.VCPUs != 4 || !saved.VM2API.KeyPresent || secret != "rotated-pro-key" || password != "sub-password" || cpa != "cpa-key" {
		t.Fatal("settings did not survive reopen")
	}
	// Reimporting credentials must keep the old runtime binding.
	if _, err = s.SaveMailAccount(model.MailAccountProfile{Email: email}, model.MailAccountCredentials{Email: email, AccessToken: "new-at"}); err != nil {
		t.Fatal(err)
	}
	p, _, _ := s.MailAccountCredential(email)
	if p.VM2 == nil || p.VM2.ConnectionID != id {
		t.Fatal("reimport lost binding")
	}
	if err = s.DeleteMailAccount(email); err == nil {
		t.Fatal("mail deletion bypassed runtime cleanup")
	}
	if _, err = s.UpdateMailAccountManagementScope(email, "mail"); err == nil {
		t.Fatal("live slot moved out of its lifecycle manager")
	}
	file, err := s.ExportProAccounts([]string{email}, model.ProDownstream(v))
	if err != nil {
		t.Fatal(err)
	}
	if file.Accounts[0].VM2Target.Secret != "rotated-pro-key" {
		t.Fatal("export did not preserve rotated credential")
	}
	dest, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer dest.Close()
	if _, err = dest.ImportProAccount(file.Accounts[0], file.ExportedAt, false, model.ProDownstream(model.DefaultProSettings())); err != nil {
		t.Fatal(err)
	}
	migrated, _, _ := dest.MailAccountCredential(email)
	bound, err := dest.DownstreamTarget(migrated.VM2.ConnectionID)
	if err != nil || bound.Secret != "rotated-pro-key" || bound.Scope != "pro" || migrated.VM2.SlotID != p.VM2.SlotID || !migrated.ProMigration.Paused {
		t.Fatal("migration lost the original slot/connection")
	}
}
