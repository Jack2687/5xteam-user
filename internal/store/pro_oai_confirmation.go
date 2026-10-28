package store

import (
	"chatgpt-space-merge/internal/model"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

type ProOAIWorkItem struct {
	Email, Mode     string
	Manual, Blocked bool
}

func (s *Store) ProOAIWork(now time.Time, recoverRunning bool) ([]ProOAIWorkItem, error) {
	s.mu.Lock()
	rows, err := s.db.Query(`SELECT m.email,j.key,j.value,
	(coalesce(json_extract(m.profile,'$.pro_auto.stop_requested'),0) AND NOT
	 (json_extract(m.profile,'$.management_scope')='mail' AND json_type(m.profile,'$.mail_pro_actions')='object'
	  AND coalesce(json_extract(j.value,'$.manual'),0) AND json_type(j.value,'$.requested_at') IS 'text'))
	OR coalesce(json_extract(m.profile,'$.pro_workflow_running'),0)
	FROM pro_oai_confirmation_accounts c JOIN mail_accounts m ON m.email=c.email JOIN json_each(c.profile) j
	WHERE (json_extract(m.profile,'$.management_scope')='pro' OR (json_extract(m.profile,'$.management_scope')='mail' AND json_type(m.profile,'$.mail_pro_actions')='object'))
	AND json_extract(j.value,'$.status') IN ('waiting','failed','unconfirmed','review','running')
	ORDER BY json_extract(j.value,'$.completed_at'),m.email,j.key`)
	if err != nil {
		s.mu.Unlock()
		return nil, err
	}
	defer s.mu.Unlock()
	defer rows.Close()
	out := []ProOAIWorkItem{}
	for rows.Next() {
		var item ProOAIWorkItem
		var raw string
		var v model.ProOAIConfirmation
		if err = rows.Scan(&item.Email, &item.Mode, &raw, &item.Blocked); err != nil {
			return nil, err
		}
		if err = json.Unmarshal([]byte(raw), &v); err != nil {
			return nil, err
		}
		if v.Due(now) || recoverRunning && v.Status == "running" {
			item.Manual = v.Manual
			out = append(out, item)
		}
	}
	return out, rows.Err()
}

func (s *Store) RecoverProOAIConfirmations() error {
	items, err := s.ProOAIWork(time.Now(), true)
	if err != nil {
		return err
	}
	for _, item := range items {
		_, err = s.UpdateProAccount(item.Email, func(p *model.MailAccountProfile) {
			v := p.ProOAIConfirmations[item.Mode]
			if v == nil || v.Status != "running" || v.ConfirmedAt != nil {
				return
			}
			now := time.Now()
			v.Status = "waiting"
			v.NotBefore = &now
			v.Generation++
			// Resume the interrupted attempt, including the last allowed attempt.
			// SentAt is retained so the adapter only reads the original conversation.
			if v.Attempts > 0 {
				v.Attempts--
			}
			v.Step(v.Stage, "interrupted", now)
			v.Error = "服务重启，恢复原 Help 对话"
		})
		if err != nil {
			return err
		}
	}
	return nil
}

// Cookies and the pinned proxy are encrypted separately and never enter public profiles.
func (s *Store) SaveProOAISession(email, mode string, generation int, session map[string]any) error {
	raw := mustJSON(session)
	if len(raw) > 256<<10 {
		return errors.New("Help 会话过大")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var profile string
	if err := s.db.QueryRow(`SELECT profile FROM mail_accounts WHERE email=?`, strings.ToLower(email)).Scan(&profile); err != nil {
		return err
	}
	var p model.MailAccountProfile
	if err := json.Unmarshal([]byte(profile), &p); err != nil {
		return err
	}
	v := p.ProOAIConfirmations[mode]
	if v == nil || v.Generation != generation || v.Status == "stopped" || v.ConfirmedAt != nil || p.OAIBlockedByAccountStop(mode) {
		return errors.New("Help 会话所属任务已停止或更新")
	}
	sealed, err := s.encrypt(raw)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`INSERT INTO pro_oai_sessions(email,mode,encrypted_session) VALUES(?,?,?) ON CONFLICT(email,mode) DO UPDATE SET encrypted_session=excluded.encrypted_session`, strings.ToLower(email), mode, sealed)
	return err
}

func (s *Store) ProOAISession(email, mode string) (map[string]any, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var sealed string
	err := s.db.QueryRow(`SELECT encrypted_session FROM pro_oai_sessions WHERE email=? AND mode=?`, strings.ToLower(email), mode).Scan(&sealed)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	raw, err := s.decrypt(sealed)
	if err != nil {
		return nil, err
	}
	var result map[string]any
	err = json.Unmarshal([]byte(raw), &result)
	return result, err
}
