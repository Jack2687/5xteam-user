package store

import (
	"chatgpt-space-merge/internal/nexovcc"
	"encoding/json"
)

// The unique restock link is persisted before issuance. Recovery never guesses
// ownership from creation time, card order or an otherwise available card.
func (s *Store) NexovccRestockOperation(taskID string) (nexovcc.Operation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var op nexovcc.Operation
	var raw string
	err := s.db.QueryRow(`SELECT profile FROM nexovcc_operations WHERE scope='open' AND json_extract(profile,'$.restock_task_id')=?`, taskID).Scan(&raw)
	if err == nil {
		err = json.Unmarshal([]byte(raw), &op)
	}
	return op, err
}
