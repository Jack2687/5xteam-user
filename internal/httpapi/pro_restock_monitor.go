package httpapi

import (
	"time"

	"chatgpt-space-merge/internal/restock"
)

type proRestockMonitorState struct {
	Running       bool      `json:"running"`
	Phase         string    `json:"phase"`
	StartedAt     time.Time `json:"started_at"`
	CollectedAt   time.Time `json:"collected_at"`
	CompletedAt   time.Time `json:"completed_at"`
	NextCheckAt   time.Time `json:"next_check_at"`
	LastSuccessAt time.Time `json:"last_success_at"`
	CollectionMS  int64     `json:"collection_ms"`
	ProcessingMS  int64     `json:"processing_ms"`
	TotalMS       int64     `json:"total_ms"`
	Error         string    `json:"error,omitempty"`
}

func nextRestockCheck(started, finished time.Time, seconds int, success bool) time.Time {
	interval := time.Duration(seconds) * time.Second
	if !success {
		return finished.Add(interval)
	}
	next := started.Add(interval)
	if next.Before(finished) {
		return finished
	}
	return next
}

func (s *Server) beginRestockCollection(at time.Time) {
	s.proRestock.mu.Lock()
	defer s.proRestock.mu.Unlock()
	s.proRestock.monitorState = proRestockMonitorState{
		Running: true, Phase: "preparing", StartedAt: at,
		LastSuccessAt: s.proRestock.monitorState.LastSuccessAt,
	}
}

func (s *Server) setRestockMonitorPhase(phase string) {
	s.proRestock.mu.Lock()
	s.proRestock.monitorState.Phase = phase
	s.proRestock.mu.Unlock()
}

func (s *Server) publishRestockCollection(snap restock.Snapshot) {
	s.proRestock.mu.Lock()
	defer s.proRestock.mu.Unlock()
	s.proRestock.liveSnapshot = snap
	m := &s.proRestock.monitorState
	m.Phase, m.CollectedAt = "reconciling", time.Now()
	m.CollectionMS = m.CollectedAt.Sub(m.StartedAt).Milliseconds()
	m.Error = snap.Error
	if snap.Valid && snap.LoadValid && !snap.At.IsZero() {
		m.LastSuccessAt = snap.At
	}
}

func (s *Server) finishRestockCollection(at, next time.Time, err error) {
	s.proRestock.mu.Lock()
	defer s.proRestock.mu.Unlock()
	m := &s.proRestock.monitorState
	m.Running, m.Phase, m.CompletedAt, m.NextCheckAt = false, "idle", at, next
	m.TotalMS = at.Sub(m.StartedAt).Milliseconds()
	if !m.CollectedAt.IsZero() {
		m.ProcessingMS = at.Sub(m.CollectedAt).Milliseconds()
	}
	if err != nil {
		m.Error = err.Error()
	}
}
