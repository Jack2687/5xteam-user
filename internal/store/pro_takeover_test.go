package store

import (
	"chatgpt-space-merge/internal/restock"
	"strings"
	"testing"
	"time"
)

func TestTakeoverStorageRestartEncryptionAndTransactionalGate(t *testing.T) {
	dir := t.TempDir()
	s, e := Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	c, _, e := s.ProTakeoverConfig()
	if e != nil || c.Enabled {
		t.Fatal("must default disabled", e)
	}
	id := s.TakeoverInstanceID()
	c.Role = "provider"
	c.PeerURL = "http://127.0.0.1:7896"
	c.Username = "admin"
	c.PeerID = "other"
	c.Enabled = true
	c, e = s.SaveProTakeoverConfig(c, "peer-secret-never-plain")
	if e != nil {
		t.Fatal(e)
	}
	var raw, encrypted string
	e = s.db.QueryRow(`SELECT payload,encrypted_password FROM pro_takeover_config`).Scan(&raw, &encrypted)
	if e != nil || strings.Contains(raw+encrypted, "peer-secret-never-plain") {
		t.Fatal("plaintext password", e)
	}
	now := time.Now()
	state := restock.TakeoverState{Epoch: "round", Phase: "active", CoverageReady: true, LeaseUntil: now.Add(time.Minute), ContactAt: now}
	if e = s.SaveProTakeoverState(state); e != nil {
		t.Fatal(e)
	}
	b := TakeoverBinding{AccountID: 10, TargetID: "pinned", Added: []int64{42}, Epoch: "round"}
	if e = s.SaveProTakeoverBinding(b); e != nil {
		t.Fatal(e)
	}
	if e = takeoverPurchaseGate(s.db, "", now); e == nil {
		t.Fatal("stale snapshot accepted")
	}
	if e = takeoverPurchaseGate(s.db, state.Token(c), now); e != nil {
		t.Fatal(e)
	}
	if e = takeoverPurchaseGate(s.db, state.Token(c), now.Add(2*time.Minute)); e == nil {
		t.Fatal("expired grant accepted")
	}
	changed := c
	changed.PeerID = "different"
	if _, e = s.SaveProTakeoverConfig(changed, ""); e == nil {
		t.Fatal("orphaned active memberships")
	}
	if e = s.Close(); e != nil {
		t.Fatal(e)
	}
	s, e = Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	loaded, secret, e := s.ProTakeoverConfig()
	if e != nil || secret != "peer-secret-never-plain" || loaded.Version != c.Version || s.TakeoverInstanceID() != id {
		t.Fatal("restart lost credentials/identity", e)
	}
	state, e = s.ProTakeoverState()
	if e != nil || state.Epoch != "round" {
		t.Fatal("restart lost ownership", e)
	}
	rows, e := s.ProTakeoverBindings()
	if e != nil || len(rows) != 1 || rows[0].Added[0] != 42 {
		t.Fatal("restart lost membership ledger", e)
	}
	c.Enabled = false
	if _, e = s.SaveProTakeoverConfig(c, ""); e != nil {
		t.Fatal(e)
	}
	if e = takeoverPurchaseGate(s.db, state.Token(c), now); e == nil {
		t.Fatal("disabled provider purchased")
	}
}
