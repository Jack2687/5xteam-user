package model

import "time"

// ConnectionID points to an encrypted immutable connection/runtime snapshot.
// AssignmentID is persisted before creation, so ambiguous retries reuse one slot.
type ProVM2Binding struct {
	ConnectionID  string     `json:"connection_id"`
	AssignmentID  string     `json:"assignment_id"`
	SlotID        string     `json:"slot_id"`
	RuntimeType   string     `json:"runtime_type"`
	ContainerName string     `json:"container_name,omitempty"`
	Running       bool       `json:"running"`
	Schedulable   bool       `json:"schedulable"`
	Stage         string     `json:"stage"`
	ReadyAt       *time.Time `json:"ready_at,omitempty"`
	SyncedAt      *time.Time `json:"synced_at,omitempty"`
	DeletedAt     *time.Time `json:"deleted_at,omitempty"`
	Error         string     `json:"error,omitempty"`
}

func (p MailAccountProfile) ProVM2CleanupPending() bool {
	return p.VM2 != nil && p.VM2.DeletedAt == nil &&
		(p.RemoveStatus == "completed" || p.RemoveStatus == "team_removed" || p.DeletionAccepted())
}
