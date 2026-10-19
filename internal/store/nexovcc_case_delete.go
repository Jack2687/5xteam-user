package store

import (
	"database/sql"
	"time"
)

type nexovccCaseQuery interface {
	Query(string, ...any) (*sql.Rows, error)
	QueryRow(string, ...any) *sql.Row
}

func (c NexovccCase) canRemoveUnopened() bool {
	return c.State == "unopened" && c.PaymentStatus == "failed" && c.OpenedAt == nil && c.Match == nil
}

type NexovccCaseRemoval struct {
	Deleted    int      `json:"deleted"`
	DeletedIDs []string `json:"deleted_ids"`
	Skipped    int      `json:"skipped"`
}

// DeleteNexovccUnopenedCases hides confirmed failed records from return lists.
// Payment orders, card usage and financial matches remain intact. A changed
// manual revision, successful payment or new match makes the record visible again.
func (s *Store) DeleteNexovccUnopenedCases() (NexovccCaseRemoval, error) {
	out := NexovccCaseRemoval{DeletedIDs: []string{}}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	if err = lockProCardAdmission(tx); err != nil {
		return out, err
	}
	cases, err := s.nexovccCasesQuery(tx)
	if err != nil {
		return out, err
	}
	now := formatTime(time.Now())
	for _, c := range cases {
		if c.State != "unopened" || c.RemovedAt != "" {
			continue
		}
		if !c.canRemoveUnopened() {
			out.Skipped++
			continue
		}
		if _, err = tx.Exec(`INSERT INTO nexovcc_case_removals(order_id,revision,removed_at) VALUES(?,?,?) ON CONFLICT(order_id) DO UPDATE SET revision=excluded.revision,removed_at=excluded.removed_at`, c.ID, c.Revision, now); err != nil {
			return out, err
		}
		if _, err = tx.Exec(`INSERT INTO nexovcc_audit(profile) VALUES(?)`, mustJSON(map[string]any{
			"action": "delete_unopened_case", "order_id": c.ID, "email": c.Email, "card_id": c.CardID, "at": now,
		})); err != nil {
			return out, err
		}
		out.DeletedIDs = append(out.DeletedIDs, c.ID)
	}
	out.Deleted = len(out.DeletedIDs)
	return out, tx.Commit()
}
