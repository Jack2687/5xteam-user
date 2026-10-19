package store

import (
	"chatgpt-space-merge/internal/model"
	"database/sql"
	"encoding/json"
	"errors"
)

// Pro credentials are separate from Team's vm2api settings.
func (s *Store) ProVM2APISettings() (model.VM2APISettings, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v := model.DefaultProSettings()
	var raw, encrypted string
	err := s.db.QueryRow("SELECT profile, COALESCE((SELECT encrypted_key FROM pro_vm2api_secret WHERE id=1),'') FROM pro_settings WHERE id=1").Scan(&raw, &encrypted)
	if errors.Is(err, sql.ErrNoRows) {
		return v.VM2API, "", nil
	}
	if err != nil {
		return v.VM2API, "", err
	}
	if err = json.Unmarshal([]byte(raw), &v); err != nil {
		return v.VM2API, "", err
	}
	v.VM2API.KeyPresent = encrypted != ""
	secret, err := s.decryptOptional(encrypted)
	return v.VM2API, secret, err
}

// Update Pro snapshots only. Team may use a different management credential
// for the same endpoint and must not inherit changes from the Pro form.
func (s *Store) rotateProVMSecretTx(tx *sql.Tx, endpoint, secret string) error {
	rows, err := tx.Query(`SELECT DISTINCT d.id,d.encrypted_payload FROM downstream_connections d JOIN mail_accounts m ON json_extract(m.profile,'$.vm2api.connection_id')=d.id`)
	if err != nil {
		return err
	}
	changes := map[string]string{}
	for rows.Next() {
		var id, encrypted string
		if err = rows.Scan(&id, &encrypted); err != nil {
			break
		}
		var raw string
		raw, err = s.decrypt(encrypted)
		if err != nil {
			break
		}
		var target model.DownstreamTarget
		if err = json.Unmarshal([]byte(raw), &target); err != nil {
			break
		}
		if target.Provider != "vm2api" || target.VM2API.URL != endpoint {
			continue
		}
		target.Secret = secret
		var encoded []byte
		encoded, err = json.Marshal(target)
		if err != nil {
			break
		}
		changes[id], err = s.encrypt(string(encoded))
		if err != nil {
			break
		}
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return err
	}
	for id, encrypted := range changes {
		if _, err = tx.Exec("UPDATE downstream_connections SET encrypted_payload=? WHERE id=?", encrypted, id); err != nil {
			return err
		}
	}
	return nil
}
