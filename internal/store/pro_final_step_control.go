package store

import (
	"encoding/json"
	"time"

	"chatgpt-space-merge/internal/model"
)

// Only the global final-step pause creates this continuation marker. Ordinary
// failed/unknown merge-v2 workflows must still require explicit review.
func (s *Store) DuePausedProMergeV2(now time.Time) ([]model.MailAccountProfile, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.db.Query(`SELECT profile FROM mail_accounts WHERE (json_extract(profile,'$.management_scope')='pro' OR (json_extract(profile,'$.management_scope')='mail' AND json_type(profile,'$.mail_pro_actions')='object'))
	AND json_extract(profile,'$.pro_merge_v2.completed_at') IS NULL
	AND json_extract(profile,'$.pro_merge_v2.status')='interrupted'
	AND (json_extract(profile,'$.pro_merge_v2.submitted_at') IS NULL OR json_extract(profile,'$.pro_merge_v2.accepted_at') IS NOT NULL)
	AND COALESCE(json_extract(profile,'$.pro_workflow_running'),0)=0
	AND COALESCE(json_extract(profile,'$.pro_auto.stop_requested'),0)=0
	AND COALESCE(json_extract(profile,'$.pro_auto.status'),'')<>'stopped'
	AND COALESCE(json_extract(profile,'$.pro_migration.paused'),0)=0
	AND julianday(json_extract(profile,'$.pro_merge_v2.auto_resume_at'))<=julianday(?)
	ORDER BY julianday(json_extract(profile,'$.pro_merge_v2.auto_resume_at')) LIMIT 10`, formatTime(now))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.MailAccountProfile
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
