package store

import (
	"chatgpt-space-merge/internal/model"
	"encoding/json"
)

// Includes completed removals for counters and in-progress actions for recovery.
// No credential decryption or downstream requests on page refreshes.
func (s *Store) VMQualityAccounts() ([]model.FreeAccountProfile, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.db.Query(`SELECT profile FROM free_accounts WHERE json_extract(profile,'$.push_provider')='vm2api' AND COALESCE(json_extract(profile,'$.vm2_slot_id'),'')<>''`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.FreeAccountProfile{}
	for rows.Next() {
		var raw string
		var p model.FreeAccountProfile
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		if err = json.Unmarshal([]byte(raw), &p); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
