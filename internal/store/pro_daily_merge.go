package store

import (
	"chatgpt-space-merge/internal/ejh"
	"chatgpt-space-merge/internal/model"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

type ProDailyMergeRun struct {
	Day        string    `json:"day"`
	At         time.Time `json:"at"`
	CapturedAt time.Time `json:"captured_at"`
	Count      int       `json:"count"`
}

// Snapshot and day claim commit together, including an empty snapshot. Restart,
// repeated ticks and same-day configuration edits cannot expand today's cohort.
func (s *Store) CaptureDailyEJHMerge(now time.Time) (*ProDailyMergeRun, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	preview, err := readRestockConfig(s.db)
	if err != nil {
		return nil, err
	}
	deadline, due := preview.DailyEJHMergeDeadline(now)
	if !due || now.Before(deadline) {
		return nil, nil
	}
	var claimed bool
	if err = s.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM pro_daily_ejh_merge_runs WHERE day=?)`, deadline.Format("2006-01-02")).Scan(&claimed); err != nil || claimed {
		return nil, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	// Acquire the SQLite writer before reading configuration so two instances
	// cannot deadlock while upgrading deferred read transactions.
	if _, err = tx.Exec(`UPDATE pro_restock_config SET payload=payload WHERE id=1`); err != nil {
		return nil, err
	}
	c, err := readRestockConfig(tx)
	if err != nil {
		return nil, err
	}
	at, enabled := c.DailyEJHMergeDeadline(now)
	if !enabled || now.Before(at) {
		return nil, nil
	}
	day := at.Format("2006-01-02")
	result, err := tx.Exec(`INSERT INTO pro_daily_ejh_merge_runs(day,payload) VALUES(?, '{}') ON CONFLICT(day) DO NOTHING`, day)
	if err != nil {
		return nil, err
	}
	n, err := result.RowsAffected()
	if err != nil || n == 0 {
		return nil, err
	}
	rows, err := tx.Query(`SELECT m.profile,c.profile FROM mail_accounts m
 JOIN gptpay_orders o ON o.id=(SELECT id FROM gptpay_orders WHERE email=m.email
 AND json_extract(profile,'$.status')='success' AND json_extract(profile,'$.plan_code')<>'codex_point' ORDER BY created_at DESC,id DESC LIMIT 1)
 JOIN pro_card_usage u ON u.order_id=o.id JOIN pro_card_types k ON k.card_key=u.card_key AND k.card_type='ejh'
 JOIN ejh_cases c ON c.id=o.id WHERE json_extract(m.profile,'$.management_scope')='pro'
 AND NOT EXISTS(SELECT 1 FROM gptpay_orders pending WHERE pending.email=m.email AND pending.active=1)`)
	if err != nil {
		return nil, err
	}
	updates := []model.MailAccountProfile{}
	for rows.Next() {
		var raw, caseRaw string
		var p model.MailAccountProfile
		var payment ejh.Case
		if err = rows.Scan(&raw, &caseRaw); err != nil {
			break
		}
		if err = json.Unmarshal([]byte(raw), &p); err != nil {
			break
		}
		if err = json.Unmarshal([]byte(caseRaw), &payment); err != nil {
			break
		}
		// Use immutable payment success time, never order creation or last sync time.
		if payment.OpenedAt == nil || payment.OpenedAt.After(at) || payment.PaymentStatus != "success" || payment.RemovedAt != nil || payment.RequestedAt != nil {
			continue
		}
		if p.DispositionStarted() || p.TransferStatus == "completed" || p.RemoveStatus == "completed" || p.RemoveStatus == "team_removed" || p.ProAuto.StopRequested || p.ProAuto.Status == "stopped" || p.ProAuto.Status == "completed" || (p.ProMigration != nil && p.ProMigration.Paused) {
			continue
		}
		if p.ProAuto.ID != "" && p.ProAuto.OrderID != payment.ID {
			continue
		}
		if p.PushStatus != "completed" && p.ProAuto.Status != "running" && p.ProAuto.Status != "waiting_quota" && p.ProAuto.Status != "awaiting_push" {
			continue
		}
		// Pending attempts from an earlier day retain their retry budget.
		if p.DailyMergeMatches() && p.ProDailyMerge.OrderID == payment.ID {
			continue
		}
		p.ProDailyMerge = &model.ProDailyMergeRequest{At: at, OrderID: payment.ID, AutomationID: p.ProAuto.ID}
		updates = append(updates, p)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return nil, err
	}
	for _, p := range updates {
		if _, err = tx.Exec(`UPDATE mail_accounts SET profile=? WHERE email=?`, mustJSON(p), p.Email); err != nil {
			return nil, err
		}
	}
	run := ProDailyMergeRun{Day: day, At: at, CapturedAt: now, Count: len(updates)}
	if _, err = tx.Exec(`UPDATE pro_daily_ejh_merge_runs SET payload=? WHERE day=?`, mustJSON(run), day); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return &run, nil
}

func (s *Store) LatestDailyEJHMerge() (*ProDailyMergeRun, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var raw string
	err := s.db.QueryRow(`SELECT payload FROM pro_daily_ejh_merge_runs ORDER BY day DESC LIMIT 1`).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var run ProDailyMergeRun
	err = json.Unmarshal([]byte(raw), &run)
	return &run, err
}

// A new subscription on the same mailbox must never inherit an old daily request.
func (s *Store) DailyEJHMergeCurrent(p model.MailAccountProfile) bool {
	if !p.DailyMergeMatches() {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var id string
	err := s.db.QueryRow(`SELECT id FROM gptpay_orders WHERE email=? AND json_extract(profile,'$.status')='success' AND json_extract(profile,'$.plan_code')<>'codex_point' AND NOT EXISTS(SELECT 1 FROM gptpay_orders pending WHERE pending.email=? AND pending.active=1) ORDER BY created_at DESC,id DESC LIMIT 1`, p.Email, p.Email).Scan(&id)
	return err == nil && id == p.ProDailyMerge.OrderID
}
