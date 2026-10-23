package httpapi

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"

	"chatgpt-space-merge/internal/model"
)

var errProAutoDeletePaused = errors.New("自动删除已暂停，额度检测继续")

type proAutomaticDeleteJob struct{ cancel context.CancelCauseFunc }

func (s *Server) setProAutoDeleteControl(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Paused *bool `json:"paused"`
	}
	if err := decodeJSON(w, r, &input, 4096); err != nil {
		return
	}
	if input.Paused == nil {
		writeAPI(w, 400, nil, "请选择停止或启动自动删除")
		return
	}
	s.proDeleteMu.Lock()
	defer s.proDeleteMu.Unlock()
	if err := s.store.SetProAutoDeletePaused(*input.Paused); err != nil {
		writeAPI(w, 500, nil, "保存自动删除状态失败")
		return
	}
	if *input.Paused {
		for job := range s.proDeleteJobs {
			job.cancel(errProAutoDeletePaused)
		}
	}
	// Resume only opens admission. The existing monitors evaluate their rules.
	writeAPI(w, 200, map[string]any{"auto_delete_paused": *input.Paused}, "")
}

func (s *Server) checkProAutoDeleteAllowed() error {
	cfg, _, _, err := s.store.ProSettings()
	if err != nil {
		return err
	}
	if cfg.AutoDeletePaused {
		return errProAutoDeletePaused
	}
	return nil
}

// Register deletion alone: pausing it must never cancel login/push/quota jobs.
// Registration and the durable switch share a lock, including queued work.
func (s *Server) beginProAutomaticDeletion(parent context.Context) (context.Context, func(), error) {
	s.proDeleteMu.Lock()
	defer s.proDeleteMu.Unlock()
	if err := parent.Err(); err != nil {
		return nil, nil, err
	}
	if err := s.checkProAutoDeleteAllowed(); err != nil {
		return nil, nil, err
	}
	ctx, cancel := context.WithCancelCause(parent)
	job := &proAutomaticDeleteJob{cancel: cancel}
	if s.proDeleteJobs == nil {
		s.proDeleteJobs = make(map[*proAutomaticDeleteJob]struct{})
	}
	s.proDeleteJobs[job] = struct{}{}
	return ctx, sync.OnceFunc(func() {
		s.proDeleteMu.Lock()
		delete(s.proDeleteJobs, job)
		cancel(nil)
		s.proDeleteMu.Unlock()
	}), nil
}

func (s *Server) performProAutomaticDeactivation(ctx context.Context, email string) (model.MailAccountProfile, error) {
	deleteCtx, finish, err := s.beginProAutomaticDeletion(ctx)
	if err != nil {
		return model.MailAccountProfile{}, err
	}
	defer finish()
	p, err := s.performProDeactivation(deleteCtx, email)
	if errors.Is(context.Cause(deleteCtx), errProAutoDeletePaused) {
		err = errProAutoDeletePaused
	}
	return p, err
}

// Keep the full-automation account eligible for its normal quota timer. Never
// clear upstream submission facts or restart the earlier paid provisioning.
func (s *Server) waitProAutoDeleteResume(email string) error {
	p, _, err := s.store.MailAccountCredential(email)
	if err != nil {
		return err
	}
	cfg, _, _, err := s.proAccountPushSettings(p)
	if err != nil {
		return err
	}
	_, err = s.store.UpdateProAccount(email, func(p *model.MailAccountProfile) {
		if p.DeletionSubmitted() || p.ProAuto.StopRequested {
			return
		}
		// Operator pauses are not failed attempts. Leave the original deadline
		// intact so the next normal monitor can evaluate it after resuming.
		switch p.ProMergeTrigger {
		case "push_timeout":
			p.ProForceMergeAttempts = max(0, p.ProForceMergeAttempts-1)
			p.ProForceMergeNextAt, p.ProMergeTrigger = nil, ""
		case "daily_ejh":
			if p.ProDailyMerge != nil {
				p.ProDailyMerge.Attempts = max(0, p.ProDailyMerge.Attempts-1)
				p.ProDailyMerge.NextAt = nil
			}
			p.ProMergeTrigger = ""
		}
		if p.ProAuto.ID == "" {
			return
		}
		interval := max(10, p.ProAuto.QuotaIntervalSeconds)
		if cfg.Sub2.AuthMode == "siwc" {
			interval = max(10, cfg.Sub2.QuotaCheckIntervalSeconds)
		}
		next := time.Now().Add(time.Duration(interval) * time.Second)
		p.ProAuto.Status, p.ProAuto.Stage, p.ProAuto.Error = "waiting_quota", "quota", ""
		p.ProAuto.NextCheckAt = &next
		if p.ProAuto.Steps == nil {
			p.ProAuto.Steps = map[string]string{}
		}
		p.ProAuto.Steps["quota"] = "waiting"
		if p.ProAuto.Steps["deactivate"] == "running" {
			p.ProAuto.Steps["deactivate"] = "interrupted"
		}
	})
	return err
}
