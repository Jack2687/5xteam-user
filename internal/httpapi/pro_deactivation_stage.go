package httpapi

import (
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"chatgpt-space-merge/internal/model"
	"chatgpt-space-merge/internal/store"
)

func (s *Server) updateProDeletionStage(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Stage             string    `json:"stage"`
		Status            string    `json:"status"`
		ExpectedStatus    *string   `json:"expected_status"`
		ExpectedUpdatedAt time.Time `json:"expected_updated_at"`
	}
	if decodeJSON(w, r, &input, 8<<10) != nil {
		return
	}
	label := map[string]string{"credentials": "凭据", "verify": "验证", "submit": "提交", "confirm": "确认"}[input.Stage]
	valid := false
	switch input.Status {
	case "not_started", "pending", "completed", "failed", "interrupted", "unknown":
		valid = true
	case "waiting":
		valid = input.Stage == "confirm"
	}
	if label == "" || !valid || input.ExpectedStatus == nil || input.ExpectedUpdatedAt.IsZero() {
		writeAPI(w, 400, nil, "无效的删除阶段、状态或版本，请刷新后重试")
		return
	}
	email := strings.ToLower(strings.TrimSpace(r.PathValue("email")))
	value, _ := s.proLocks.LoadOrStore("account:"+email, &sync.Mutex{})
	lock := value.(*sync.Mutex)
	if !lock.TryLock() {
		writeAPI(w, 409, nil, "该账号正在执行操作，请完成后再修改状态")
		return
	}
	defer lock.Unlock()
	p, _, err := s.store.MailAccountCredential(email)
	if err != nil || p.ManagementScope != "pro" {
		writeAPI(w, 404, nil, "Pro 账号不存在")
		return
	}
	if s.proAutomationRunning(email) || s.proLoginAlreadyRunning(email) || p.ProWorkflowRunning || p.ProAuto.Status == "running" {
		writeAPI(w, 409, nil, "该账号正在执行操作，请完成后再修改状态")
		return
	}
	for _, progress := range p.ProManualStages {
		if progress.Status == "running" {
			writeAPI(w, 409, nil, "该账号有未结束的操作，请先核验结果")
			return
		}
	}
	previous := ""
	if p.ProDeactivation != nil {
		previous = p.ProDeactivation.Steps[input.Stage]
	}
	if previous != *input.ExpectedStatus {
		writeAPI(w, 409, nil, "步骤状态已变化，请刷新后重新修改")
		return
	}
	settings, _, _, err := s.proAccountPushSettings(p)
	if err != nil {
		writeAPI(w, 500, nil, "读取删除配置失败")
		return
	}
	saved, err := s.store.UpdateProAccountIfUnchanged(email, input.ExpectedUpdatedAt, func(p *model.MailAccountProfile) {
		now := time.Now()
		if p.ProDeactivation == nil {
			p.ProDeactivation = &model.ProDeactivation{StartedAt: now}
		}
		d := p.ProDeactivation
		if d.Steps == nil {
			d.Steps = map[string]string{}
		}
		if d.Manual == nil {
			d.Manual = map[string]model.ProStageOverride{}
		}
		d.Steps[input.Stage] = input.Status
		d.Manual[input.Stage] = model.ProStageOverride{Status: input.Status, UpdatedAt: now}
		d.UpdatedAt, d.Error, p.ProLastError = now, "", ""
		d.Message = "手动修正删除账户" + label + "状态"
		if input.Status == "completed" && (input.Stage == "submit" || input.Stage == "confirm") {
			wasAccepted := d.AcceptedAt != nil
			// Operator-confirmed outcomes are checkpoints too. Never erase the
			// submission guard, even if a later edit changes a displayed stage.
			if d.SubmittedAt == nil {
				d.SubmittedAt = &now
			}
			if d.AcceptedAt == nil {
				d.AcceptedAt = &now
			}
			d.Steps["submit"] = "completed"
			if input.Stage == "confirm" {
				if d.ConfirmedAt == nil {
					d.ConfirmedAt = &now
				}
				d.ConfirmationSource = "manual"
				d.Message = "手动确认删除成功，已停止所有定时续跑"
				if !wasAccepted {
					d.Manual["submit"] = model.ProStageOverride{Status: "completed", UpdatedAt: now}
				}
			} else if d.ConfirmedAt == nil {
				d.Steps["confirm"] = "waiting"
				d.Message = "手动确认删除已提交，等待结果核验"
			}
			if p.DailyMergeMatches() {
				p.ProDailyMerge.Completed = true
			}
			if p.ProPostMergeGroups == nil && p.VM2 == nil && settings.Provider == "sub2" && len(settings.PostMergeGroupIDs) > 0 {
				p.ProPostMergeGroups = &model.ProPostMergeGroups{Action: "deactivate", Status: "pending", GroupIDs: append([]int64(nil), settings.PostMergeGroupIDs...), AccountID: p.Sub2AccountID, Downstream: model.ProDownstream(settings), UpdatedAt: now}
			}
		} else if input.Stage == "confirm" {
			// Reopening confirmation may only query the original request; it
			// cannot turn the account into a new deletion or provisioning job.
			d.ConfirmedAt, d.ConfirmationSource = nil, ""
		}
		switch {
		case d.ConfirmedAt != nil:
			d.Status, d.Stage = "completed", "confirm"
		case d.AcceptedAt != nil:
			d.Status, d.Stage = "queued", "confirm"
		case d.SubmittedAt != nil:
			d.Status, d.Stage = "unknown", "submit"
		default:
			d.Status, d.Stage = input.Status, input.Stage
			if input.Status == "completed" {
				// Completing credentials or verification is not deletion success.
				d.Status = "pending"
			}
		}
		d.NextCheckAt = nil
		if p.DeletionNeedsContinuation() {
			next := now.Add(settings.DeletionCheckInterval())
			if p.DeletionAccepted() && (p.ProPostMergeGroupsPending() || p.ProVM2CleanupPending()) {
				next = now
			}
			d.NextCheckAt = &next
		}
	})
	if errors.Is(err, store.ErrProStageConflict) {
		writeAPI(w, 409, nil, err.Error())
		return
	}
	if err != nil {
		writeAPI(w, 500, nil, "保存删除账户状态失败")
		return
	}
	s.enqueueAuditEvent(model.AutoRotationEvent{AccountID: saved.ID, Email: saved.Email, Type: "manual_stage", Source: "pro_management", Operation: "deletion_stage", Stage: input.Stage, FromStatus: previous, ToStatus: input.Status, Message: "手动修正删除账户" + label + "状态", Details: map[string]any{"remote_request_sent": false, "confirmation_source": saved.ProDeactivation.ConfirmationSource}})
	writeAPI(w, 200, saved, "")
}
