package store

import (
	"chatgpt-space-merge/internal/model"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// Save only the web session used for deletion. OAuth credentials and all
// payment/history records remain intact even when login reports a dead user.
func (s *Store) SaveProDeletionSession(email, session string) error {
	if !json.Valid([]byte(session)) {
		return errors.New("登录 Session 格式无效")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var raw, sealed string
	if err := s.db.QueryRow("SELECT profile,encrypted_credentials FROM mail_accounts WHERE email=?", strings.ToLower(email)).Scan(&raw, &sealed); err != nil {
		return err
	}
	var p model.MailAccountProfile
	var c model.MailAccountCredentials
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		return err
	}
	if !p.ProFinalStepAllowed("deactivate") {
		return errors.New("账号不在 Pro 管理中")
	}
	plain, err := s.decrypt(sealed)
	if err != nil {
		return err
	}
	if err = json.Unmarshal([]byte(plain), &c); err != nil {
		return err
	}
	c.ChatGPTSession = session
	p.ChatGPTSessionPresent = true
	sealed, err = s.encrypt(mustJSON(c))
	if err != nil {
		return err
	}
	_, err = s.db.Exec("UPDATE mail_accounts SET profile=?,encrypted_credentials=? WHERE email=?", mustJSON(p), sealed, strings.ToLower(email))
	return err
}
func (s *Store) DueProDeactivations(now time.Time) ([]model.MailAccountProfile, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.db.Query(`SELECT profile FROM mail_accounts WHERE (json_extract(profile,'$.management_scope')='pro' OR (json_extract(profile,'$.management_scope')='mail' AND json_type(profile,'$.mail_pro_actions')='object')) AND json_extract(profile,'$.pro_deactivation.submitted_at') IS NOT NULL AND COALESCE(json_extract(profile,'$.pro_workflow_running'),0)=0 AND COALESCE(json_extract(profile,'$.pro_auto.stop_requested'),0)=0 AND COALESCE(json_extract(profile,'$.pro_migration.paused'),0)=0 AND julianday(json_extract(profile,'$.pro_deactivation.next_check_at'))<=julianday(?) ORDER BY julianday(json_extract(profile,'$.pro_deactivation.next_check_at'))`, now.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.MailAccountProfile{}
	for rows.Next() {
		var raw string
		var p model.MailAccountProfile
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		if err = json.Unmarshal([]byte(raw), &p); err != nil {
			return nil, err
		}
		if p.DeletionNeedsContinuation() {
			out = append(out, p)
			// Apply the limit after completion filtering, so stale completed
			// clocks cannot starve accounts that still need confirmation.
			if len(out) == 10 {
				break
			}
		}
	}
	return out, rows.Err()
}

// Apply an edited interval to existing waits without restarting stopped work
// or changing a request already in flight. Configuration and clocks commit together.
func rescheduleProDeactivationsTx(tx *sql.Tx, interval time.Duration) error {
	rows, err := tx.Query(`SELECT email,profile FROM mail_accounts WHERE (json_extract(profile,'$.management_scope')='pro' OR (json_extract(profile,'$.management_scope')='mail' AND json_type(profile,'$.mail_pro_actions')='object')) AND json_extract(profile,'$.pro_deactivation.submitted_at') IS NOT NULL AND COALESCE(json_extract(profile,'$.pro_workflow_running'),0)=0 AND COALESCE(json_extract(profile,'$.pro_auto.status'),'')<>'running'`)
	if err != nil {
		return err
	}
	type update struct{ email, profile string }
	var updates []update
	next := time.Now().Add(interval)
	for rows.Next() {
		var email, raw string
		var p model.MailAccountProfile
		if err = rows.Scan(&email, &raw); err != nil {
			break
		}
		if err = json.Unmarshal([]byte(raw), &p); err != nil {
			break
		}
		p.ProDeactivation.NextCheckAt = nil
		if p.DeletionNeedsContinuation() && !p.ProAuto.StopRequested {
			p.ProDeactivation.NextCheckAt = &next
		}
		updates = append(updates, update{email, mustJSON(p)})
	}
	err = errors.Join(err, rows.Err(), rows.Close())
	if err != nil {
		return err
	}
	for _, u := range updates {
		if _, err = tx.Exec("UPDATE mail_accounts SET profile=? WHERE email=?", u.profile, u.email); err != nil {
			return err
		}
	}
	return nil
}
