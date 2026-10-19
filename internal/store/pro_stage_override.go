package store

import (
	"encoding/json"
	"errors"
	"strings"
	"time"

	"chatgpt-space-merge/internal/model"
)

var ErrProStageConflict = errors.New("账号进度已变化，请刷新后重新修改")

// Save only the annotation. Do not reconcile workflows or release card orders.
func (s *Store) SetProStageOverride(email, stage, status string, expected time.Time) (model.MailAccountProfile, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var p model.MailAccountProfile
	var raw string
	email = strings.ToLower(strings.TrimSpace(email))
	if err := s.db.QueryRow("SELECT profile FROM mail_accounts WHERE email=?", email).Scan(&raw); err != nil {
		return p, err
	}
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		return p, err
	}
	if p.ManagementScope != "pro" || !p.UpdatedAt.Equal(expected) {
		return p, ErrProStageConflict
	}
	if p.ProStageOverrides == nil {
		p.ProStageOverrides = map[string]model.ProStageOverride{}
	}
	p.UpdatedAt = time.Now()
	p.ProStageOverrides[stage] = model.ProStageOverride{Status: status, UpdatedAt: p.UpdatedAt}
	encoded, err := json.Marshal(p)
	if err != nil {
		return p, err
	}
	_, err = s.db.Exec("UPDATE mail_accounts SET profile=?,updated_at=? WHERE email=?", string(encoded), formatTime(p.UpdatedAt), email)
	return p, err
}
