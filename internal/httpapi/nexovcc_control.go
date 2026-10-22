package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"chatgpt-space-merge/internal/nexovcc"
)

type nexovccControlState struct {
	Paused    bool      `json:"paused"`
	UpdatedAt time.Time `json:"updated_at,omitempty"`
}

// This lock never waits for the issuer/client mutex, so stop works during I/O.
func (s *Server) loadNexovccControlLocked() error {
	if s.nexovcc.gate != nil {
		return nil
	}
	if err := s.store.NexovccCached("request_control", &s.nexovcc.control); err != nil {
		return err
	}
	s.nexovcc.gate = nexovcc.NewRequestGate(s.nexovcc.control.Paused)
	return nil
}
func (s *Server) nexovccControl() (nexovccControlState, error) {
	s.nexovcc.controlMu.Lock()
	defer s.nexovcc.controlMu.Unlock()
	err := s.loadNexovccControlLocked()
	return s.nexovcc.control, err
}
func (s *Server) nexovccOperationContext(parent context.Context) (context.Context, func(), error) {
	s.nexovcc.controlMu.Lock()
	defer s.nexovcc.controlMu.Unlock()
	if err := s.loadNexovccControlLocked(); err != nil {
		return nil, nil, err
	}
	return s.nexovcc.gate.Bind(parent)
}
func (s *Server) controlNexovcc(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Action string `json:"action"`
	}
	if decodeJSON(w, r, &input, 1024) != nil {
		return
	}
	if input.Action != "stop" && input.Action != "resume" {
		writeAPI(w, 400, nil, "请选择停止或恢复")
		return
	}
	s.nexovcc.controlMu.Lock()
	defer s.nexovcc.controlMu.Unlock()
	if err := s.loadNexovccControlLocked(); err != nil {
		writeAPI(w, 500, nil, "读取环球停止状态失败")
		return
	}
	state := nexovccControlState{Paused: input.Action == "stop", UpdatedAt: time.Now()}
	if state.Paused {
		s.nexovcc.gate.Stop()
		s.nexovcc.control = state
	} else {
		if !s.nexovcc.mu.TryLock() {
			writeAPI(w, 409, nil, "正在结束原任务，请稍后点击恢复")
			return
		}
		defer s.nexovcc.mu.Unlock()
	}
	if err := s.store.NexovccCache("request_control", state); err != nil {
		writeAPI(w, 500, nil, "保存环球停止状态失败；当前停止状态保持")
		return
	}
	if !state.Paused {
		s.nexovcc.gate.Resume()
		s.nexovcc.jobMu.Lock()
		s.nexovcc.job = nexovccJob{ID: randomRegistrationID(), Status: "completed", Message: "环球接口已恢复，后台按配置继续运行"}
		s.nexovcc.jobMu.Unlock()
	}
	s.nexovcc.control = state
	writeAPI(w, 200, state, "")
}
func (s *Server) nexovccJobSnapshot() (nexovccJob, error) {
	control, err := s.nexovccControl()
	if err != nil {
		return nexovccJob{}, err
	}
	s.nexovcc.jobMu.Lock()
	defer s.nexovcc.jobMu.Unlock()
	job := s.nexovcc.job
	job.Paused = control.Paused
	if control.Paused {
		job.Status, job.Message = "stopped", "环球接口已停止；已提交操作保留在操作记录中"
	}
	return job, nil
}
func (s *Server) checkNexovccControl() error {
	state, err := s.nexovccControl()
	if err != nil {
		return err
	}
	if state.Paused {
		return fmt.Errorf("%s", nexovcc.StoppedMessage)
	}
	return nil
}
