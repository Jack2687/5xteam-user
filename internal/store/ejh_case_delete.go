package store

import (
	"database/sql"
	"fmt"
	"time"
)

// DeleteEJHUnopenedCases removes return-list records, not payment orders. The
// marker survives unchanged order updates/backfill; a later opened/returned state remains
// visible so deleting a pending record cannot hide a subsequently paid order.
// An empty id selects all currently unopened EJH records in one transaction.
func (s *Store) DeleteEJHUnopenedCases(id string) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err = lockProCardAdmission(tx); err != nil {
		return nil, err
	}
	cases, err := s.ejhCasesQuery(tx, nil)
	if err != nil {
		return nil, err
	}
	deleted := []string{}
	found := id == ""
	now := time.Now()
	for _, c := range cases {
		if id != "" && c.ID != id {
			continue
		}
		found = true
		if c.CardType != "ejh" || c.State != "unopened" {
			if id != "" {
				return nil, fmt.Errorf("仅可删除未开通的 EJH 账号回款记录，状态可能已变化，请刷新后重试")
			}
			continue
		}
		if c.RemovedAt != nil {
			continue
		}
		if _, err = tx.Exec(`UPDATE ejh_cases SET profile=json_set(profile,'$.removed_at',?) WHERE id=?`, formatTime(now), c.ID); err != nil {
			return nil, err
		}
		if err = s.ejhPutTx(tx, "match_audit", EJHID("audit", "delete-case:"+c.ID+now.String()), map[string]any{
			"action": "delete_unopened_case", "at": now,
			"before": map[string]any{"id": c.ID, "email": c.Email, "card_id": c.EJHCardID, "state": c.State},
			"after":  map[string]any{"id": c.ID, "card_id": c.EJHCardID, "removed_at": now},
		}); err != nil {
			return nil, err
		}
		deleted = append(deleted, c.ID)
	}
	if !found {
		return nil, sql.ErrNoRows
	}
	return deleted, tx.Commit()
}
