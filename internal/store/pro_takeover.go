package store

import (
	"chatgpt-space-merge/internal/restock"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

func (s *Store) TakeoverInstanceID() string {
	return fmt.Sprintf("space-%x", sha256.Sum256(append(append([]byte{}, s.key...), []byte("/takeover-instance/v1")...)))
}
func readTakeoverConfig(q proCapacityQuery) (restock.TakeoverConfig, string, error) {
	c := restock.DefaultTakeoverConfig()
	var raw, secret string
	err := q.QueryRow(`SELECT payload,encrypted_password FROM pro_takeover_config WHERE id=1`).Scan(&raw, &secret)
	if errors.Is(err, sql.ErrNoRows) {
		return c, "", nil
	}
	if err == nil {
		err = json.Unmarshal([]byte(raw), &c)
	}
	return c, secret, err
}
func readTakeoverState(q proCapacityQuery) (restock.TakeoverState, error) {
	var v restock.TakeoverState
	var raw string
	e := q.QueryRow(`SELECT payload FROM pro_takeover_state WHERE id=1`).Scan(&raw)
	if errors.Is(e, sql.ErrNoRows) {
		return v, nil
	}
	if e == nil {
		e = json.Unmarshal([]byte(raw), &v)
	}
	return v, e
}
func (s *Store) ProTakeoverConfig() (restock.TakeoverConfig, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, encrypted, e := readTakeoverConfig(s.db)
	if e != nil {
		return c, "", e
	}
	secret, e := s.decryptOptional(encrypted)
	return c, secret, e
}
func (s *Store) SaveProTakeoverConfig(c restock.TakeoverConfig, password string) (restock.TakeoverConfig, error) {
	if e := c.Validate(); e != nil {
		return c, e
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	old, encrypted, e := readTakeoverConfig(s.db)
	if e != nil {
		return c, e
	}
	if old.Version != c.Version {
		return c, errors.New("接管配置已变化，请刷新重试")
	}
	state, e := readTakeoverState(s.db)
	if e != nil {
		return c, e
	}
	// Changing a connection during an unfinished handoff would orphan its
	// memberships. Disabling is a drain request, never an immediate removal.
	if state.Active() && (c.Role != old.Role || c.PeerID != old.PeerID || c.PeerURL != old.PeerURL || mustJSON(c.Groups) != mustJSON(old.Groups)) {
		return c, errors.New("正在接管或交还，请等待临时分组撤回完成后再修改角色、对方或分组")
	}
	if password != "" {
		encrypted, e = s.encrypt(password)
		if e != nil {
			return c, e
		}
	}
	if c.Enabled && encrypted == "" {
		return c, errors.New("请填写对方登录密码")
	}
	c.PasswordPresent = encrypted != ""
	c.Version++
	_, e = s.db.Exec(`INSERT INTO pro_takeover_config VALUES(1,?,?) ON CONFLICT(id) DO UPDATE SET payload=excluded.payload,encrypted_password=excluded.encrypted_password`, mustJSON(c), encrypted)
	return c, e
}
func (s *Store) ProTakeoverState() (restock.TakeoverState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return readTakeoverState(s.db)
}
func (s *Store) SaveProTakeoverState(v restock.TakeoverState) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, e := s.db.Exec(`INSERT INTO pro_takeover_state VALUES(1,?) ON CONFLICT(id) DO UPDATE SET payload=excluded.payload`, mustJSON(v))
	return e
}

type TakeoverBinding struct {
	AccountID int64   `json:"account_id"`
	TargetID  string  `json:"target_id"`
	Added     []int64 `json:"added"`
	Epoch     string  `json:"epoch"`
}

func (s *Store) ProTakeoverBindings() ([]TakeoverBinding, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, e := s.db.Query(`SELECT payload FROM pro_takeover_bindings ORDER BY account_id`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []TakeoverBinding{}
	for rows.Next() {
		var raw string
		var b TakeoverBinding
		if e = rows.Scan(&raw); e != nil {
			return nil, e
		}
		if e = json.Unmarshal([]byte(raw), &b); e != nil {
			return nil, e
		}
		out = append(out, b)
	}
	return out, rows.Err()
}
func (s *Store) SaveProTakeoverBinding(b TakeoverBinding) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, e := s.db.Exec(`INSERT INTO pro_takeover_bindings VALUES(?,?,?) ON CONFLICT(target_id,account_id) DO UPDATE SET payload=excluded.payload`, b.TargetID, b.AccountID, mustJSON(b))
	return e
}
func (s *Store) DeleteProTakeoverBinding(b TakeoverBinding) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, e := s.db.Exec(`DELETE FROM pro_takeover_bindings WHERE target_id=? AND account_id=?`, b.TargetID, b.AccountID)
	return e
}

// Recheck ownership under the same transaction that reserves/claims a
// purchase. A snapshot or a queued task cannot outlive a grant/phase change.
func takeoverPurchaseGate(q proCapacityQuery, token string, now time.Time) error {
	c, _, e := readTakeoverConfig(q)
	if e != nil {
		return e
	}
	v, e := readTakeoverState(q)
	if e != nil {
		return e
	}
	if reason := v.Block(c, now); reason != "" {
		return errors.New(reason)
	}
	if token != v.Token(c) {
		return errors.New("跨项目接管阶段已变化，等待新快照重新判断")
	}
	return nil
}
