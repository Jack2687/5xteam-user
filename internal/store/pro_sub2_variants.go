package store

import (
	"chatgpt-space-merge/internal/model"
	"database/sql"
	"encoding/json"
	"errors"
)

// An inactive push mode retains its own connection/groups and encrypted secret.
func (s *Store) ProSub2Variant(mode string) (model.Sub2Settings, string, error) {
	if mode != "" && mode != "siwc" {
		return model.Sub2Settings{}, "", errors.New("推送方式无效")
	}
	active, secret, _, err := s.ProSettings()
	if err != nil || active.Sub2.AuthMode == mode {
		return active.Sub2, secret, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	v := model.DefaultSub2Settings()
	v.AuthMode = mode
	var raw, encrypted string
	err = s.db.QueryRow("SELECT payload,encrypted_password FROM pro_sub2_variants WHERE auth_mode=?", mode).Scan(&raw, &encrypted)
	if errors.Is(err, sql.ErrNoRows) {
		return v, "", nil
	}
	if err != nil {
		return v, "", err
	}
	if err = json.Unmarshal([]byte(raw), &v); err != nil {
		return v, "", err
	}
	secret, err = s.decryptOptional(encrypted)
	v.PasswordPresent = encrypted != ""
	return v, secret, err
}
