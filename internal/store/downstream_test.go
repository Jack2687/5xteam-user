package store

import (
	"chatgpt-space-merge/internal/model"
	"strings"
	"testing"
)

func TestDownstreamSnapshotEncryptionRotationAndRestart(t *testing.T) {
	dir := t.TempDir()
	s, e := Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	v := model.DownstreamTarget{Provider: "vm2api", Secret: "secret-original", VM2API: model.DefaultVM2APISettings()}
	v.VM2API.URL = "https://original.example"
	id, e := s.SaveDownstreamTarget(v)
	if e != nil {
		t.Fatal(e)
	}
	other := v
	other.VM2API.URL = "https://other.example"
	otherID, e := s.SaveDownstreamTarget(other)
	if e != nil {
		t.Fatal(e)
	}
	var raw string
	if e = s.db.QueryRow("SELECT encrypted_payload FROM downstream_connections WHERE id=?", id).Scan(&raw); e != nil {
		t.Fatal(e)
	}
	if strings.Contains(raw, v.Secret) || strings.Contains(raw, v.VM2API.URL) {
		t.Fatal("unencrypted snapshot")
	}
	if e = s.RotateDownstreamSecret("vm2api", v.VM2API.URL, "", "rotated-secret"); e != nil {
		t.Fatal(e)
	}
	if e = s.Close(); e != nil {
		t.Fatal(e)
	}
	s, e = Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	saved, e := s.DownstreamTarget(id)
	if e != nil || saved.Secret != "rotated-secret" || saved.VM2API.Kernel != v.VM2API.Kernel {
		t.Fatal("rotation or restart lost binding", e)
	}
	saved, e = s.DownstreamTarget(otherID)
	if e != nil || saved.Secret != v.Secret {
		t.Fatal("changed unrelated connection")
	}
}
