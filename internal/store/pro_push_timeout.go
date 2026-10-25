package store

import (
	"chatgpt-space-merge/internal/model"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

// Apply an explicit settings change to unfinished purchases in the same
// transaction. First push time and all workflow checkpoints remain unchanged.
func syncProPushTimeoutTx(tx *sql.Tx, next model.ProSettings) error {
	previous := model.DefaultProSettings()
	var raw string
	err := tx.QueryRow("SELECT profile FROM pro_settings WHERE id=1").Scan(&raw)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err == nil {
		if err = json.Unmarshal([]byte(raw), &previous); err != nil {
			return err
		}
	}
	if previous.ForceMergeEnabled == next.ForceMergeEnabled && previous.ForceMergeMinutes == next.ForceMergeMinutes {
		return nil
	}
	rows, err := tx.Query("SELECT email,profile FROM mail_accounts WHERE json_extract(profile,'$.management_scope')='pro'")
	if err != nil {
		return err
	}
	var emails []string
	for rows.Next() {
		var email, profile string
		if err = rows.Scan(&email, &profile); err != nil {
			rows.Close()
			return err
		}
		var p model.MailAccountProfile
		if err = json.Unmarshal([]byte(profile), &p); err != nil {
			rows.Close()
			return err
		}
		// Unknown historical policies and work already entering disposition
		// retain their original state; saving settings never restarts a workflow.
		if p.ProPushTimeoutEnabled == nil || p.SpaceMergedOnce || p.ProWorkflowRunning || p.ProDeactivation != nil || p.MerchantStarted() ||
			p.ProAuto.Status == "completed" || p.ProAuto.Stage == "merge" || p.ProAuto.Stage == "deactivate" || p.ProMergeTrigger != "" {
			continue
		}
		started := false
		for _, status := range []string{p.InviteStatus, p.AcceptStatus, p.TransferStatus, p.RemoveStatus} {
			if status != "" && status != "not_started" {
				started = true
			}
		}
		if !started {
			emails = append(emails, email)
		}
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return err
	}
	if err = rows.Close(); err != nil {
		return err
	}
	for _, email := range emails {
		now := formatTime(time.Now())
		if _, err = tx.Exec(`UPDATE mail_accounts SET profile=json_set(profile,'$.pro_push_timeout_enabled',json(?),'$.pro_push_timeout_minutes',?,'$.updated_at',?),updated_at=? WHERE email=?`, mustJSON(next.ForceMergeEnabled), next.ForceMergeMinutes, now, now, email); err != nil {
			return err
		}
	}
	return nil
}

func captureProOrderTimeout(tx *sql.Tx, email string) error {
	var raw string
	err := tx.QueryRow("SELECT profile FROM mail_accounts WHERE email=?", email).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	var p model.MailAccountProfile
	if err = json.Unmarshal([]byte(raw), &p); err != nil {
		return err
	}
	if p.ManagementScope != "pro" || p.ProPushTimeoutEnabled != nil || p.ProPushStartedAt != nil || p.ProAuto.ID != "" {
		return nil
	}
	// Never backfill historical successful/uncertain purchases using today's switch.
	var prior int
	if err = tx.QueryRow("SELECT count(*) FROM gptpay_orders WHERE email=? AND json_extract(profile,'$.status')!='failed'", email).Scan(&prior); err != nil {
		return err
	}
	if prior > 0 {
		return nil
	}
	c := model.DefaultProSettings()
	err = tx.QueryRow("SELECT profile FROM pro_settings WHERE id=1").Scan(&raw)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err == nil {
		if err = json.Unmarshal([]byte(raw), &c); err != nil {
			return err
		}
	}
	p.CaptureProPushTimeout(c)
	_, err = tx.Exec("UPDATE mail_accounts SET profile=? WHERE email=?", mustJSON(p), email)
	return err
}

// Complete profile scan with error propagation: a read failure is never an empty pool.
func (s *Store) ProRestockProfiles() ([]model.MailAccountProfile, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.db.Query("SELECT profile FROM mail_accounts WHERE json_extract(profile,'$.management_scope')='pro'")
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
		out = append(out, p)
	}
	return out, rows.Err()
}
