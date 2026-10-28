package store

import (
	"encoding/json"
	"fmt"

	"chatgpt-space-merge/internal/restock"
)

// Monitoring observes a task while a separate worker may claim, start or stop
// it. Replace only the exact payload read by the monitor. On a conflict, use
// the current task for this evaluation instead of rolling its lifecycle back.
func (s *Store) SaveProRestockObservation(t *restock.Task) error {
	if t.ObservedPayload == "" {
		return fmt.Errorf("补货观察缺少原始版本，未覆盖任务")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	raw := mustJSON(*t)
	result, err := s.db.Exec(`UPDATE pro_restock_tasks SET status=?,cost=?,payload=? WHERE id=? AND payload=? AND json_extract(payload,'$.stopped_at') IS NULL`, t.Status, t.Cost, raw, t.ID, t.ObservedPayload)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		var projected string
		if err = s.db.QueryRow("SELECT "+proRestockTaskProjectionSQL+",payload FROM pro_restock_tasks WHERE id=?", t.ID).Scan(&projected, &raw); err != nil {
			return err
		}
		var current restock.Task
		if err = json.Unmarshal([]byte(projected), &current); err != nil {
			return err
		}
		*t = current
	}
	t.ObservedPayload = raw
	return nil
}
