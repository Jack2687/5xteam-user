package store

import (
	"chatgpt-space-merge/internal/model"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

func (s *Store) VM2APISettings() (model.VM2APISettings, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v := model.DefaultVM2APISettings()
	var raw, encrypted string
	err := s.db.QueryRow("SELECT profile,encrypted_key FROM vm2api_settings WHERE id=1").Scan(&raw, &encrypted)
	if errors.Is(err, sql.ErrNoRows) {
		return v, "", nil
	}
	if err != nil {
		return v, "", err
	}
	if err = json.Unmarshal([]byte(raw), &v); err != nil {
		return v, "", err
	}
	key, err := s.decryptOptional(encrypted)
	return v, key, err
}

func (s *Store) SaveVM2APISettings(v model.VM2APISettings, key string) (model.VM2APISettings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var encrypted string
	err := s.db.QueryRow("SELECT encrypted_key FROM vm2api_settings WHERE id=1").Scan(&encrypted)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return v, err
	}
	if strings.TrimSpace(key) != "" {
		encrypted, err = s.encrypt(strings.TrimSpace(key))
		if err != nil {
			return v, err
		}
	}
	v.KeyPresent = encrypted != ""
	raw, err := json.Marshal(v)
	if err != nil {
		return v, err
	}
	_, err = s.db.Exec(`INSERT INTO vm2api_settings(id,profile,encrypted_key) VALUES(1,?,?) ON CONFLICT(id) DO UPDATE SET profile=excluded.profile,encrypted_key=excluded.encrypted_key`, string(raw), encrypted)
	return v, err
}

func (s *Store) SaveDownstreamTarget(v model.DownstreamTarget) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	raw, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	h := hmac.New(sha256.New, s.key)
	_, _ = h.Write(raw)
	id := fmt.Sprintf("target-%x", h.Sum(nil))
	encrypted, err := s.encrypt(string(raw))
	if err != nil {
		return "", err
	}
	_, err = s.db.Exec("INSERT OR IGNORE INTO downstream_connections(id,encrypted_payload) VALUES(?,?)", id, encrypted)
	return id, err
}

func (s *Store) DownstreamTarget(id string) (model.DownstreamTarget, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var v model.DownstreamTarget
	var encrypted string
	if err := s.db.QueryRow("SELECT encrypted_payload FROM downstream_connections WHERE id=?", id).Scan(&encrypted); err != nil {
		return v, err
	}
	raw, err := s.decrypt(encrypted)
	if err != nil {
		return v, err
	}
	err = json.Unmarshal([]byte(raw), &v)
	return v, err
}

// Rotate only credentials for the exact saved endpoint/identity. Slot templates
// and account bindings stay fixed when the default provider changes later.
func (s *Store) RotateDownstreamSecret(provider, endpoint, email, secret string) error {
	if endpoint == "" || secret == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.db.Query("SELECT id,encrypted_payload FROM downstream_connections")
	if err != nil {
		return err
	}
	changes := map[string]string{}
	for rows.Next() {
		var id, encrypted string
		if err = rows.Scan(&id, &encrypted); err != nil {
			rows.Close()
			return err
		}
		raw, e := s.decrypt(encrypted)
		if e != nil {
			rows.Close()
			return e
		}
		var v model.DownstreamTarget
		if e = json.Unmarshal([]byte(raw), &v); e != nil {
			rows.Close()
			return e
		}
		matches := v.Provider == provider && ((provider == "sub2" && v.Sub2.URL == endpoint && v.Sub2.Email == email) || (provider == "cpa" && v.CPA.URL == endpoint) || (provider == "vm2api" && v.VM2API.URL == endpoint))
		if v.Scope == "pro" || v.Scope == "pro-fallback" || !matches || v.Secret == secret {
			continue
		}
		v.Secret = secret
		b, e := json.Marshal(v)
		if e != nil {
			rows.Close()
			return e
		}
		changes[id], e = s.encrypt(string(b))
		if e != nil {
			rows.Close()
			return e
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if len(changes) == 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for id, encrypted := range changes {
		if _, err = tx.Exec("UPDATE downstream_connections SET encrypted_payload=? WHERE id=?", encrypted, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}
