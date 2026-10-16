package store

import (
	"chapt-space-user/internal/restock"
	"database/sql"
	"encoding/json"
	"time"
)

func replacementTasks(tx *sql.Tx) ([]restock.Task, error) {
	rows, err := tx.Query(`SELECT json_set(t.payload,'$.force_merge_source',coalesce(r.source,'')) FROM pro_restock_tasks t LEFT JOIN pro_restock_replacements r ON r.task_id=t.id WHERE t.status NOT IN ('failed','cancelled') OR r.source IS NOT NULL ORDER BY t.created_at,t.id`)
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

// Reconcile links while holding the same SQLite write lock used by reservation.
// Confirmed failures release coverage; unknown payments retain it without ETA.
func (s *Store) ReconcileForceMerge(c restock.Config, snap restock.Snapshot, tasks []restock.Task) ([]restock.Task, error) {
	if !snap.Valid || !snap.LoadValid || snap.At.IsZero() || time.Since(snap.At) > time.Duration(c.FreshSeconds)*time.Second || snap.At.After(time.Now().Add(5*time.Second)) {
		return tasks, nil
	}
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
	currentConfig, err := readRestockConfig(tx)
	if err != nil {
		return nil, err
	}
	if currentConfig.Version != c.Version {
		return tasks, nil
	}
	persisted, err := replacementTasks(tx)
	if err != nil {
		return nil, err
	}
	sources := map[string]bool{}
	ready := map[int64]bool{}
	for _, a := range snap.Accounts {
		sources[a.ForceMergeKey] = true
		ready[a.ID] = a.Ready
	}

	for i, t := range persisted {
		if t.ForceMergeSource == "" {
			continue
		}

		if t.GroupID != snap.GroupID || (t.Binding != snap.Binding && !sources[t.ForceMergeSource]) {
			continue
		}
		if t.Status == "failed" || t.Status == "cancelled" || (t.Status == "ready" && (!sources[t.ForceMergeSource] || !ready[t.AccountID])) {
			if _, err = tx.Exec("DELETE FROM pro_restock_replacements WHERE source=?", t.ForceMergeSource); err != nil {
				return nil, err
			}
			t.ForceMergeSource = ""
		}
		persisted[i] = t
	}
	stats := restock.ForceMergeSummary(c, snap, persisted, snap.At)
	if c.Enabled && c.Mode == "auto" && stats.Triggered {
		// Adopt verified same-target automatic work so its eventual readiness remains
		// linked after restart. It consumes inflight, never this policy's purchase budget.
		available := 0
		known := map[string]bool{}
		for _, t := range persisted {
			known[t.ID] = true
			known[t.Email] = true
			if t.Active() && t.ForceMergeSource == "" && t.Binding == snap.Binding && t.GroupID == snap.GroupID {
				available++
			}
		}
		for _, t := range tasks {
			if available >= len(stats.Sources) {
				break
			}
			if known[t.ID] || known[t.Email] || !t.Active() || t.Binding != snap.Binding || t.GroupID != snap.GroupID || t.DecisionID != "" {
				continue
			}
			t.ID = "adopted:" + t.ID
			t.DecisionID = t.ID
			t.ManagedExternally = true
			t.Cost = 0
			if t.CreatedAt.IsZero() {
				t.CreatedAt = time.Now()
			}
			if _, err = tx.Exec("INSERT INTO pro_restock_tasks VALUES(?,?,?,?,?,?)", t.ID, t.Email, t.Status, formatTime(t.CreatedAt), 0, mustJSON(t)); err != nil {
				return nil, err
			}
			persisted = append(persisted, t)
			known[t.Email] = true
			available++
		}
		next := 0
		for i, t := range persisted {
			if next >= len(stats.Sources) {
				break
			}
			if !t.Active() || t.ForceMergeSource != "" || t.Binding != snap.Binding || t.GroupID != snap.GroupID {
				continue
			}
			if t.Status == "reserved" && (t.Version != c.Version || t.Plan != snap.NewPlan) {
				continue
			}
			t.ForceMergeSource = stats.Sources[next]
			if _, err = tx.Exec("INSERT INTO pro_restock_replacements(source,task_id) VALUES(?,?)", t.ForceMergeSource, t.ID); err != nil {
				return nil, err
			}
			persisted[i] = t
			next++
		}
	}
	links := map[string]string{}
	for _, t := range persisted {
		links[t.ID] = t.ForceMergeSource
	}
	adopted := map[string]restock.Task{}
	for _, t := range persisted {
		if t.ManagedExternally {
			adopted[t.Email] = t
		}
	}
	for i := range tasks {
		if t, ok := adopted[tasks[i].Email]; ok && tasks[i].DecisionID == "" {
			tasks[i] = t
		}
		tasks[i].ForceMergeSource = links[tasks[i].ID]
	}
	return tasks, tx.Commit()
}
