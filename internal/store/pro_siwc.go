package store

import (
	"chatgpt-space-merge/internal/model"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
)

func (s *Store) ProSIWCSettings() (model.ProSIWCSettings, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v := model.DefaultProSIWCSettings()
	var raw, enc string
	err := s.db.QueryRow("SELECT payload,encrypted_password FROM pro_siwc_settings WHERE id=1").Scan(&raw, &enc)
	if errors.Is(err, sql.ErrNoRows) {
		return v, "", nil
	}
	if err != nil {
		return v, "", err
	}
	if err = json.Unmarshal([]byte(raw), &v); err != nil {
		return v, "", err
	}
	secret, err := s.decryptOptional(enc)
	v.Sub2.PasswordPresent = enc != ""
	return v, secret, err
}
func (s *Store) SaveProSIWCSettings(v model.ProSIWCSettings, secret string) (model.ProSIWCSettings, error) {
	if err := v.Validate(); err != nil {
		return v, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	normalizeSub2Settings(&v.Sub2)
	v.Sub2.AuthMode = "siwc"
	v.Sub2.Provider = "sub2"
	var enc string
	err := s.db.QueryRow("SELECT encrypted_password FROM pro_siwc_settings WHERE id=1").Scan(&enc)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return v, err
	}
	if strings.TrimSpace(secret) != "" {
		enc, err = s.encrypt(secret)
		if err != nil {
			return v, err
		}
	}
	v.Sub2.PasswordPresent = enc != ""
	_, err = s.db.Exec("INSERT INTO pro_siwc_settings(id,payload,encrypted_password) VALUES(1,?,?) ON CONFLICT(id) DO UPDATE SET payload=excluded.payload,encrypted_password=excluded.encrypted_password", mustJSON(v), enc)
	return v, err
}
