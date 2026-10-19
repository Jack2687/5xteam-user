package store

import (
	"chatgpt-space-merge/internal/restock"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

// Connections are pinned encrypted downstream targets, including while old accounts drain.
type FallbackBinding struct {
	ConnectionID string    `json:"connection_id"`
	AccountID    int64     `json:"account_id"`
	GroupID      int64     `json:"group_id"`
	Name         string    `json:"name"`
	Desired      bool      `json:"desired"`
	Observed     *bool     `json:"observed"`
	Error        string    `json:"error,omitempty"`
	CheckedAt    time.Time `json:"checked_at"`
}

func (s *Store) ProFallbackConfig() (restock.FallbackConfig, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.readFallbackConfig()
}
func (s *Store) readFallbackConfig() (restock.FallbackConfig, string, error) {
	c := restock.DefaultFallbackConfig()
	var raw, target string
	err := s.db.QueryRow(`SELECT payload,target_id FROM pro_fallback_config WHERE id=1`).Scan(&raw, &target)
	if errors.Is(err, sql.ErrNoRows) {
		return c, "", nil
	}
	if err == nil {
		err = json.Unmarshal([]byte(raw), &c)
	}
	c.NormalizeGroups()
	return c, target, err
}
func (s *Store) SaveProFallbackConfig(c restock.FallbackConfig, target string) (restock.FallbackConfig, error) {
	c.NormalizeGroups()
	if err := c.Validate(); err != nil {
		return c, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	old, _, err := s.readFallbackConfig()
	if err != nil {
		return c, err
	}
	if old.Version != c.Version {
		return c, errors.New("兜底配置已被其他页面修改，请重新载入后保存")
	}
	c.Version++
	c.PasswordPresent = target != ""
	_, err = s.db.Exec(`INSERT INTO pro_fallback_config VALUES(1,?,?) ON CONFLICT(id) DO UPDATE SET payload=excluded.payload,target_id=excluded.target_id`, mustJSON(c), target)
	return c, err
}
func (s *Store) ProFallbackBindings() ([]FallbackBinding, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.db.Query(`SELECT payload FROM pro_fallback_bindings ORDER BY json_extract(payload,'$.checked_at'),account_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []FallbackBinding{}
	for rows.Next() {
		var raw string
		var b FallbackBinding
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		if err = json.Unmarshal([]byte(raw), &b); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// Persist ownership before a request; even an ambiguous response must be reconciled after restart.
func (s *Store) SaveProFallbackBinding(b FallbackBinding) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`INSERT INTO pro_fallback_bindings VALUES(?,?,?) ON CONFLICT(target_id,account_id) DO UPDATE SET payload=excluded.payload`, b.ConnectionID, b.AccountID, mustJSON(b))
	return err
}
func (s *Store) DeleteProFallbackBinding(b FallbackBinding) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`DELETE FROM pro_fallback_bindings WHERE target_id=? AND account_id=?`, b.ConnectionID, b.AccountID)
	return err
}
