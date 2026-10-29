package httpapi

import (
	"database/sql"
	"errors"
	"net/http"

	"chatgpt-space-merge/internal/model"
	"chatgpt-space-merge/internal/store"
)

func (s *Server) stopProRestockTask(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Confirm bool `json:"confirm"`
	}
	if decodeJSON(w, r, &input, 4096) != nil {
		return
	}
	if !input.Confirm {
		writeAPI(w, 400, nil, "请确认停止本次补货")
		return
	}
	// Same startup gate as full automation. Do not wait behind a long monitor
	// scan or supplier call: persist the stop first and cancel its local workers.
	s.proAutoMu.Lock()
	task, changed, err := s.store.StopProRestockTask(r.PathValue("id"))
	if err == nil && changed {
		var members []string
		members, err = s.store.StopProRechargeReplacementChain(task.Email)
		for _, email := range members {
			if cancel := s.proAutoJobs[email]; cancel != nil {
				cancel()
			}
		}
		if cancel := s.proRestockStartJobs[task.ID]; cancel != nil {
			cancel()
		}
		if cancel := s.proAutoJobs[task.Email]; cancel != nil {
			cancel()
		}
	}
	s.proAutoMu.Unlock()
	if err != nil {
		status := 500
		if errors.Is(err, sql.ErrNoRows) {
			status = 404
		} else if errors.Is(err, store.ErrProRestockStopConflict) {
			status = 409
		}
		writeAPI(w, status, nil, err.Error())
		return
	}
	if changed {
		s.enqueueAuditEvent(model.AutoRotationEvent{Email: task.Email, Type: "manual_stop", Source: "pro_restock", Operation: "stop", ToStatus: task.Status, Message: task.Message, Details: map[string]any{"task_id": task.ID, "order_id": task.OrderID, "card_id": task.CardID}})
	}
	writeAPI(w, 200, task, "")
}
