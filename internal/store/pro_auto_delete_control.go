package store

import (
	"database/sql"
	"encoding/json"
	"errors"

	"chatgpt-space-merge/internal/model"
)

// The legacy field now gates all automatic final steps. Retain its storage key
// so existing paused installations stay paused across upgrades and rollbacks.
// Never reset quota clocks or credentials.
func (s *Store) SetProAutoDeletePaused(paused bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	v := model.DefaultProSettings()
	var raw string
	err := s.db.QueryRow("SELECT profile FROM pro_settings WHERE id=1").Scan(&raw)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if raw != "" {
		if err = json.Unmarshal([]byte(raw), &v); err != nil {
			return err
		}
	}
	v.AutoDeletePaused = paused
	_, err = s.db.Exec(`INSERT INTO pro_settings(id,profile,encrypted_sub2_password,encrypted_cpa_key) VALUES(1,?,'','') ON CONFLICT(id) DO UPDATE SET profile=excluded.profile`, mustJSON(v))
	return err
}
