package httpapi

import (
	"database/sql"
	"errors"
	"net/http"
	"time"

	"chatgpt-space-merge/internal/store"
)

type ejhFinanceRecording struct {
	StartedAt   time.Time  `json:"started_at"`
	LastSuccess *time.Time `json:"last_success,omitempty"`
	Error       string     `json:"error,omitempty"`
}

func (s *Server) captureEJHFinance() {
	var state ejhFinanceRecording
	if err := s.store.EJHGet("finance_recording", "latest", &state); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return
	}
	now := time.Now()
	if state.StartedAt.IsZero() {
		state.StartedAt = now
	}
	if err := s.store.CaptureEJHFinance(now); err != nil {
		state.Error = err.Error()
	} else {
		state.LastSuccess = &now
		state.Error = ""
	}
	_ = s.store.EJHPut("finance_recording", "latest", state)
}

func (s *Server) ejhFinance(w http.ResponseWriter, r *http.Request) {
	recharges, err := store.EJHRecords[store.EJHRechargeEvent](s.store, "finance_recharge")
	if err != nil {
		writeAPI(w, 500, nil, err.Error())
		return
	}
	usage, err := store.EJHRecords[store.EJHConsumption](s.store, "finance_usage")
	if err != nil {
		writeAPI(w, 500, nil, err.Error())
		return
	}
	days, err := store.EJHRecords[store.EJHConsumptionDay](s.store, "finance_usage_day")
	if err != nil {
		writeAPI(w, 500, nil, err.Error())
		return
	}
	snapshots, err := store.EJHRecords[store.EJHFinancialSnapshot](s.store, "finance_snapshot")
	if err != nil {
		writeAPI(w, 500, nil, err.Error())
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	var state ejhFinanceRecording
	if err = s.store.EJHGet("finance_recording", "latest", &state); err != nil && !errors.Is(err, sql.ErrNoRows) {
		writeAPI(w, 500, nil, err.Error())
		return
	}
	writeAPI(w, 200, map[string]any{"recharges": recharges, "consumption": usage, "usage_days": days, "snapshots": snapshots, "recording": state, "server_time": time.Now()}, "")
}
