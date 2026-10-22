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

func (s *Server) updateProAutoStage(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Stage             string    `json:"stage"`
		Status            string    `json:"status"`
		ExpectedStatus    *string   `json:"expected_status"`
		ExpectedUpdatedAt time.Time `json:"expected_updated_at"`
	}
	if decodeJSON(w, r, &input, 8<<10) != nil {
		return
	}
	label := map[string]string{"login": "登录", "recharge": "开通", "oauth": "RT/AT", "push": "推送", "quota": "额度", "merge": "合并", "deactivate": "删除"}[input.Stage]
	valid := false
	switch input.Status {
	case "not_started", "pending", "completed", "failed", "skipped", "interrupted", "unknown":
		valid = true
	case "waiting":
		valid = input.Stage == "quota" || input.Stage == "deactivate"
	}
	if label == "" || !valid || input.ExpectedStatus == nil || input.ExpectedUpdatedAt.IsZero() {
		writeAPI(w, 400, nil, "无效的全自动阶段、状态或版本，请刷新后重试")
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
	rows, err := s.proAccountListWithCards([]model.MailAccountProfile{p})
	if err != nil {
		writeAPI(w, 500, nil, "读取当前阶段失败")
		return
	}
	previous := rows[0].StageProgress.Steps[input.Stage]
	if previous != *input.ExpectedStatus {
		writeAPI(w, 409, nil, "步骤状态已变化，请刷新后重新修改")
		return
	}
	saved, err := s.store.SetProStageOverride(email, input.Stage, input.Status, input.ExpectedUpdatedAt)
	if errors.Is(err, store.ErrProStageConflict) {
		writeAPI(w, 409, nil, err.Error())
		return
	}
	if err != nil {
		writeAPI(w, 500, nil, "保存阶段状态失败")
		return
	}
	s.enqueueAuditEvent(model.AutoRotationEvent{AccountID: saved.ID, Email: saved.Email, Type: "manual_stage", Source: "pro_management", Operation: "auto_stage", Stage: input.Stage, FromStatus: previous, ToStatus: input.Status, Message: "手动修正 Pro 全自动" + label + "状态", Details: map[string]any{"remote_request_sent": false, "execution_checkpoints_changed": false}})
	writeAPI(w, 200, saved.ProStages(), "")
}
