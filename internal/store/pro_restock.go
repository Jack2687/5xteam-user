package store

import (
	"chapt-space-user/internal/model"
	"chapt-space-user/internal/restock"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

func (s *Store) initializeProRestock() error {
	_, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS pro_restock_config(id INTEGER PRIMARY KEY CHECK(id=1),payload TEXT NOT NULL);
 CREATE TABLE IF NOT EXISTS pro_restock_tasks(id TEXT PRIMARY KEY,email TEXT NOT NULL COLLATE NOCASE,status TEXT NOT NULL,created_at TEXT NOT NULL,cost INTEGER NOT NULL,payload TEXT NOT NULL);
 CREATE UNIQUE INDEX IF NOT EXISTS pro_restock_account ON pro_restock_tasks(email) WHERE status NOT IN ('ready','failed','cancelled');
 CREATE TABLE IF NOT EXISTS pro_restock_decisions(id TEXT PRIMARY KEY,created_at TEXT NOT NULL,payload TEXT NOT NULL);
 CREATE TABLE IF NOT EXISTS pro_restock_snapshot(id INTEGER PRIMARY KEY CHECK(id=1),payload TEXT NOT NULL);
 CREATE TABLE IF NOT EXISTS pro_restock_clock(id INTEGER PRIMARY KEY CHECK(id=1),last_batch TEXT,reset_at TEXT);`)
	return err
}
func (s *Store) ProRestockConfig() (restock.Config, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return readRestockConfig(s.db)
}
func readRestockConfig(q proCapacityQuery) (restock.Config, error) {
	c := restock.DefaultConfig()
	var raw string
	err := q.QueryRow(`SELECT payload FROM pro_restock_config WHERE id=1`).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return c, nil
	}
	if err == nil {
		err = json.Unmarshal([]byte(raw), &c)
	}
	return c, err
}
func (s *Store) SaveProRestockConfig(c restock.Config) (restock.Config, error) {
	if err := c.Validate(); err != nil {
		return c, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	old, err := readRestockConfig(s.db)
	if err != nil {
		return c, err
	}
	if old.Version != c.Version {
		return c, errors.New("配置已被其他页面修改，请刷新后重试")
	}
	c.Version++
	_, err = s.db.Exec(`INSERT INTO pro_restock_config VALUES(1,?) ON CONFLICT(id) DO UPDATE SET payload=excluded.payload`, mustJSON(c))
	return c, err
}
func (s *Store) ProRestockTasks() ([]restock.Task, error) {
	return s.proRestockTasksWhere("status NOT IN ('ready','failed','cancelled') OR created_at>?", formatTime(time.Now().Add(-48*time.Hour)))
}
func (s *Store) ProRestockDecisionTasks(id string) ([]restock.Task, error) {
	return s.proRestockTasksWhere("json_extract(payload,'$.decision_id')=?", id)
}
func (s *Store) proRestockTasksWhere(where string, arg any) ([]restock.Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.db.Query("SELECT payload FROM pro_restock_tasks WHERE "+where+" ORDER BY created_at", arg)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []restock.Task{}
	for rows.Next() {
		var raw string
		var t restock.Task
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		if err = json.Unmarshal([]byte(raw), &t); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
func (s *Store) SaveProRestockTask(t restock.Task) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`UPDATE pro_restock_tasks SET status=?,cost=?,payload=? WHERE id=?`, t.Status, t.Cost, mustJSON(t), t.ID)
	return err
}

// ClaimProRestockDispatch is the durable boundary before any external card or
// payment action. A stale dispatcher cannot start an already claimed intent.
func (s *Store) ClaimProRestockDispatch(t restock.Task) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	result, err := s.db.Exec(`UPDATE pro_restock_tasks SET status=?,payload=? WHERE id=? AND status='reserved' AND EXISTS(SELECT 1 FROM pro_restock_config WHERE id=1 AND json_extract(payload,'$.version')=? AND json_extract(payload,'$.enabled')=1 AND json_extract(payload,'$.mode')='auto')`, t.Status, mustJSON(t), t.ID, t.Version)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return errors.New("补货意图已被领取、撤销或配置已变化")
	}
	return nil
}
func restockLimits(q proCapacityQuery, now time.Time) (restock.Limits, error) {
	var l restock.Limits
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	hour := now.Add(-time.Hour)
	err := q.QueryRow(`SELECT coalesce(sum(CASE WHEN created_at>=? THEN 1 ELSE 0 END),0),coalesce(sum(CASE WHEN created_at>=? THEN 1 ELSE 0 END),0),coalesce(sum(CASE WHEN created_at>=? OR status NOT IN ('ready','failed','cancelled') THEN cost ELSE 0 END),0),coalesce(sum(CASE WHEN created_at>=? OR status NOT IN ('ready','failed','cancelled') THEN cost ELSE 0 END),0) FROM pro_restock_tasks WHERE status!='cancelled'`, formatTime(hour), formatTime(day), formatTime(hour), formatTime(day)).Scan(&l.HourCount, &l.DayCount, &l.HourCost, &l.DayCost)
	if err != nil {
		return l, err
	}
	// Consecutive failures since the last ready task or explicit breaker reset.
	err = q.QueryRow(`SELECT count(*) FROM pro_restock_tasks WHERE status IN ('failed','attention','unknown') AND created_at>coalesce((SELECT max(created_at) FROM pro_restock_tasks WHERE status='ready'),'') AND created_at>coalesce((SELECT reset_at FROM pro_restock_clock WHERE id=1),'')`).Scan(&l.Failures)
	if err != nil {
		return l, err
	}
	var raw sql.NullString
	err = q.QueryRow(`SELECT last_batch FROM pro_restock_clock WHERE id=1`).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return l, nil
	}
	if err == nil && raw.Valid {
		t, e := time.Parse(time.RFC3339Nano, raw.String)
		if e == nil {
			l.LastBatch = &t
		}
	}
	return l, err
}
func (s *Store) ProRestockLimits(now time.Time) (restock.Limits, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return restockLimits(s.db, now)
}
func (s *Store) ResetProRestockBreaker() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, e := s.db.Exec(`INSERT INTO pro_restock_clock(id,reset_at) VALUES(1,?) ON CONFLICT(id) DO UPDATE SET reset_at=excluded.reset_at`, formatTime(time.Now()))
	return e
}

// ReserveProRestock atomically claims fresh accounts and budget. External card
// issuance and payment are deliberately outside this transaction and retain
// their existing durable intent/idempotency mechanisms.
func (s *Store) ReserveProRestock(d restock.Decision, target string, existingInflight int) ([]restock.Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`UPDATE pro_restock_config SET payload=payload WHERE id=1`); err != nil {
		return nil, err
	}
	c, err := readRestockConfig(tx)
	if err != nil {
		return nil, err
	}
	if !c.Enabled || c.Mode != "auto" || c.Version != d.Version {
		return nil, errors.New("配置版本或运行模式已变化")
	}
	l, err := restockLimits(tx, d.At)
	if err != nil {
		return nil, err
	}
	var active int
	if err = tx.QueryRow(`SELECT count(*) FROM pro_restock_tasks WHERE status NOT IN ('ready','failed','cancelled')`).Scan(&active); err != nil {
		return nil, err
	}
	n := min(d.Suggested, c.MaxBatch, c.MaxInflight-max(active, existingInflight), c.HourLimit-l.HourCount, c.DayLimit-l.DayCount, c.PoolLimit-len(d.Snapshot.Accounts)-existingInflight)
	if c.HourBudget > 0 {
		n = min(n, int(max(int64(0), c.HourBudget-l.HourCost)/max(int64(1), c.UnitCost)))
	}
	if c.DayBudget > 0 {
		n = min(n, int(max(int64(0), c.DayBudget-l.DayCost)/max(int64(1), c.UnitCost)))
	}
	cooldown := c.CooldownSeconds
	if d.Waiting >= c.EmergencyQueue {
		cooldown = c.EmergencyCooldownSeconds
	}
	if l.Failures >= c.FailureLimit || (l.LastBatch != nil && d.At.Sub(*l.LastBatch) < time.Duration(cooldown)*time.Second) {
		n = 0
	}
	if n < 1 || (!c.AllowSmall && n < c.MinBatch) {
		return nil, errors.New("事务复核：冷却、在途或预算不允许新增")
	}
	rows, err := tx.Query(proScheduleCandidateSQL, n)
	if err != nil {
		return nil, err
	}
	emails := []string{}
	for rows.Next() {
		var email string
		if err = rows.Scan(&email); err != nil {
			break
		}
		emails = append(emails, email)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return nil, err
	}
	if len(emails) == 0 || (!c.AllowSmall && len(emails) < c.MinBatch) {
		return nil, errors.New("可领取的新账号不足批量要求")
	}
	out := []restock.Task{}
	for i, email := range emails {
		t := restock.Task{ID: fmt.Sprintf("%s-%d", d.ID, i+1), Plan: d.Snapshot.NewPlan, DecisionID: d.ID, Email: email, Version: c.Version, GroupID: c.GroupID, Binding: d.Snapshot.Binding, Status: "reserved", Stage: "queued", Cost: c.UnitCost, CreatedAt: d.At, UpdatedAt: d.At, Stages: map[string]time.Time{"reserved": d.At}}
		var raw string
		var p model.MailAccountProfile
		if err = tx.QueryRow(`SELECT profile FROM mail_accounts WHERE email=?`, email).Scan(&raw); err != nil {
			return nil, err
		}
		if err = json.Unmarshal([]byte(raw), &p); err != nil {
			return nil, err
		}
		p.ProRestockTaskID = t.ID
		p.ProRestockTargetID = target
		if _, err = tx.Exec(`UPDATE mail_accounts SET profile=? WHERE email=?`, mustJSON(p), email); err != nil {
			return nil, err
		}
		if _, err = tx.Exec(`INSERT INTO pro_restock_tasks VALUES(?,?,?,?,?,?)`, t.ID, email, t.Status, formatTime(t.CreatedAt), t.Cost, mustJSON(t)); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	if _, err = tx.Exec(`INSERT INTO pro_restock_clock(id,last_batch) VALUES(1,?) ON CONFLICT(id) DO UPDATE SET last_batch=excluded.last_batch`, formatTime(d.At)); err != nil {
		return nil, err
	}
	d.Started = len(out)
	if _, err = tx.Exec(`INSERT INTO pro_restock_decisions VALUES(?,?,?) ON CONFLICT(id) DO UPDATE SET payload=excluded.payload`, d.ID, formatTime(d.At), mustJSON(d)); err != nil {
		return nil, err
	}
	return out, tx.Commit()
}
func (s *Store) CancelProRestock(t restock.Task, reason string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, e := s.db.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	var raw string
	if e = tx.QueryRow(`SELECT payload FROM pro_restock_tasks WHERE id=? AND status='reserved'`, t.ID).Scan(&raw); e != nil {
		return e
	}
	if e = json.Unmarshal([]byte(raw), &t); e != nil {
		return e
	}
	t.Status = "cancelled"
	t.Cost = 0
	t.Message = reason
	t.UpdatedAt = time.Now()
	if _, e = tx.Exec(`UPDATE pro_restock_tasks SET status='cancelled',cost=0,payload=? WHERE id=?`, mustJSON(t), t.ID); e != nil {
		return e
	}
	if _, e = tx.Exec(`UPDATE mail_accounts SET profile=json_remove(profile,'$.pro_restock_task_id','$.pro_restock_target_id') WHERE email=? AND json_extract(profile,'$.pro_restock_task_id')=? AND coalesce(json_extract(profile,'$.pro_auto.id'),'')=''`, t.Email, t.ID); e != nil {
		return e
	}
	return tx.Commit()
}
func (s *Store) ProRestockSnapshot() (restock.Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var v restock.Snapshot
	var raw string
	e := s.db.QueryRow(`SELECT payload FROM pro_restock_snapshot WHERE id=1`).Scan(&raw)
	if errors.Is(e, sql.ErrNoRows) {
		return v, nil
	}
	if e == nil {
		e = json.Unmarshal([]byte(raw), &v)
	}
	return v, e
}
func (s *Store) SaveProRestockSnapshot(v restock.Snapshot) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, e := s.db.Exec(`INSERT INTO pro_restock_snapshot VALUES(1,?) ON CONFLICT(id) DO UPDATE SET payload=excluded.payload`, mustJSON(v))
	return e
}
func (s *Store) SaveProRestockDecision(d restock.Decision) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, e := s.db.Exec(`INSERT INTO pro_restock_decisions VALUES(?,?,?) ON CONFLICT(id) DO UPDATE SET payload=excluded.payload`, d.ID, formatTime(d.At), mustJSON(d))
	return e
}
func (s *Store) ProRestockDecisions(limit, offset int) ([]restock.Decision, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, e := s.db.Query(`SELECT payload FROM pro_restock_decisions ORDER BY created_at DESC LIMIT ? OFFSET ?`, min(100, max(1, limit)), max(0, offset))
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []restock.Decision{}
	for rows.Next() {
		var raw string
		var d restock.Decision
		if e = rows.Scan(&raw); e != nil {
			return nil, e
		}
		if e = json.Unmarshal([]byte(raw), &d); e != nil {
			return nil, e
		}
		out = append(out, d)
	}
	return out, rows.Err()
}
func (s *Store) PurgeProRestock(days int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, e := s.db.Exec(`DELETE FROM pro_restock_decisions WHERE created_at<?`, formatTime(time.Now().Add(-time.Duration(days)*24*time.Hour)))
	return e
}
