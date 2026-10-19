package store

import (
	"chatgpt-space-merge/internal/model"
	"chatgpt-space-merge/internal/restock"
	"reflect"
	"strings"
	"testing"
)

func TestFallbackStorageEncryptionAndRestart(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	c, target, err := s.ProFallbackConfig()
	if err != nil || c.Enabled || target != "" {
		t.Fatal(c, target, err)
	}
	target, err = s.SaveDownstreamTarget(model.DownstreamTarget{Scope: "pro-fallback", Provider: "sub2", Sub2: model.Sub2Settings{URL: "https://example.test", Email: "test@example.test"}, Secret: "fallback-secret-never-public"})
	if err != nil {
		t.Fatal(err)
	}
	c = restock.DefaultFallbackConfig()
	c.URL = "https://example.test"
	c.Email = "test@example.test"
	c.AccountIDs = []int64{10}
	c.GroupID = 1
	c.Enabled = true
	saved, err := s.SaveProFallbackConfig(c, target)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SaveProFallbackConfig(c, target); err == nil {
		t.Fatal("stale version accepted")
	}
	var raw, encrypted string
	if err = s.db.QueryRow(`SELECT payload FROM pro_fallback_config WHERE id=1`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if err = s.db.QueryRow(`SELECT encrypted_payload FROM downstream_connections WHERE id=?`, target).Scan(&encrypted); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(raw, "fallback-secret") || strings.Contains(encrypted, "fallback-secret") {
		t.Fatal("plaintext credential leak")
	}
	b := FallbackBinding{ConnectionID: target, AccountID: 10, GroupID: 1, Desired: true}
	if err = s.SaveProFallbackBinding(b); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	c, target, err = s.ProFallbackConfig()
	if err != nil || c.Version != saved.Version || !c.PasswordPresent {
		t.Fatal(c, err)
	}
	connection, err := s.DownstreamTarget(target)
	if err != nil || connection.Secret != "fallback-secret-never-public" {
		t.Fatal("encrypted connection not restored")
	}
	if err = s.RotateDownstreamSecret("sub2", c.URL, c.Email, "unrelated-push-password"); err != nil {
		t.Fatal(err)
	}
	connection, err = s.DownstreamTarget(target)
	if err != nil || connection.Secret != "fallback-secret-never-public" {
		t.Fatal("push settings changed fallback secret")
	}
	bindings, err := s.ProFallbackBindings()
	if err != nil || len(bindings) != 1 || !bindings[0].Desired || bindings[0].Observed != nil {
		t.Fatal(bindings, err)
	}
	if err = s.DeleteProFallbackBinding(b); err != nil {
		t.Fatal(err)
	}
}

func TestFallbackLegacyReadAndMultiGroupPersistence(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	legacy := `{"version":4,"group_id":7,"enabled":false,"account_ids":[1],"check_seconds":3}`
	if _, err = s.db.Exec(`INSERT INTO pro_fallback_config VALUES(1,?,?)`, legacy, "target"); err != nil {
		t.Fatal(err)
	}
	c, target, err := s.ProFallbackConfig()
	if err != nil || !reflect.DeepEqual(c.GroupIDs, []int64{7}) {
		t.Fatal(c, err)
	}
	var raw string
	s.db.QueryRow(`SELECT payload FROM pro_fallback_config WHERE id=1`).Scan(&raw)
	if raw != legacy {
		t.Fatal("reading config unexpectedly mutated storage")
	}
	c.GroupIDs = []int64{7, 8}
	c, err = s.SaveProFallbackConfig(c, target)
	if err != nil {
		t.Fatal(err)
	}
	c, _, err = s.ProFallbackConfig()
	if err != nil || !reflect.DeepEqual(c.GroupIDs, []int64{7, 8}) {
		t.Fatal(c, err)
	}
	c.GroupIDs = []int64{}
	if _, err = s.SaveProFallbackConfig(c, target); err != nil {
		t.Fatal(err)
	}
	c, _, err = s.ProFallbackConfig()
	if err != nil || len(c.GroupIDs) != 0 || c.GroupID != 0 {
		t.Fatal("deselection restored legacy group", c, err)
	}
}
