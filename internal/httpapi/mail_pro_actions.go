package httpapi

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"sync"

	"chatgpt-space-merge/internal/model"
	"chatgpt-space-merge/internal/store"
	"chatgpt-space-merge/internal/workflow"
)

func mailFinalMode(p model.MailAccountProfile, cfg model.ProSettings) string {
	if p.MailProActions != nil && p.MailProActions.FinalMode != "" {
		return p.MailProActions.FinalMode
	}
	// A mailbox returned from Pro must retain its existing disposition.
	if p.MergeV2Started() {
		return "merge_v2"
	}
	if p.MerchantStarted() {
		return "merchant"
	}
	if p.ProDeactivation != nil {
		return "deactivate"
	}
	if proMergeTargetPinned(p) {
		return "merge"
	}
	if cfg.AutoMergeEnabled || cfg.AutoDeleteEnabled || cfg.AutoMerchantEnabled || cfg.AutoMergeV2Enabled {
		return cfg.Disposition()
	}
	return ""
}

func (s *Server) mailActionProfile(w http.ResponseWriter, r *http.Request) (model.MailAccountProfile, bool) {
	p, _, err := s.store.MailAccountCredential(r.PathValue("email"))
	if err != nil || p.ManagementScope != "mail" {
		writeAPI(w, 404, nil, "邮件账号不存在或已移入 Pro 管理")
		return p, false
	}
	return p, true
}

func (s *Server) mailProActionState(w http.ResponseWriter, r *http.Request) {
	p, ok := s.mailActionProfile(w, r)
	if !ok {
		return
	}
	cfg, _, _, err := s.store.ProSettings()
	if err != nil {
		writeAPI(w, 500, nil, "读取 Pro 配置失败")
		return
	}
	mode := mailFinalMode(p, cfg)
	oaiMode := mode
	if oaiMode == "" {
		oaiMode = cfg.Disposition()
	}
	writeAPI(w, 200, map[string]any{"account": p, "mode": mode, "oai_mode": oaiMode, "auto_paused": cfg.AutoDeletePaused, "final_completed": p.OAIConfirmationPrerequisite(mode)}, "")
}

func (s *Server) checkMailActionAvailable(p model.MailAccountProfile) error {
	if p.ProWorkflowRunning || p.ProAuto.Status == "running" {
		return errors.New("账号正在执行操作，请等待完成")
	}
	if err := s.store.SeatRecoveryAvailable(p.Email); err != nil {
		return err
	}
	free, err := s.store.FreeAccountForEmail(p.Email)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if store.SeatRecoveryTeamInFlight(free) {
		return errors.New("账号正在 Team 轮转中，请先完成原流程")
	}
	if s.proLoginAlreadyRunning(p.Email) {
		return errors.New("账号正在登录或授权，请等待完成")
	}
	return nil
}

func (s *Server) startMailFinalStep(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Mode string `json:"mode"`
	}
	if decodeJSON(w, r, &in, 4096) != nil {
		return
	}
	p, ok := s.mailActionProfile(w, r)
	if !ok {
		return
	}
	s.proAutoMu.Lock()
	defer s.proAutoMu.Unlock()
	value, _ := s.proLocks.LoadOrStore("account:"+p.Email, &sync.Mutex{})
	lock := value.(*sync.Mutex)
	if !lock.TryLock() {
		writeAPI(w, 409, nil, "该账号正在执行操作")
		return
	}
	transferred := false
	defer func() {
		if !transferred {
			lock.Unlock()
		}
	}()
	p, ok = s.mailActionProfile(w, r)
	if !ok {
		return
	}
	if err := s.checkMailActionAvailable(p); err != nil {
		writeAPI(w, 409, nil, err.Error())
		return
	}
	for _, v := range p.ProOAIConfirmations {
		if v != nil && (v.Status == "running" || v.Status == "waiting" || v.NextAttemptAt != nil) {
			writeAPI(w, 409, nil, "OAI 确认尚未结束，请先完成或停止确认")
			return
		}
	}
	cfg, _, _, err := s.store.ProSettings()
	if err != nil {
		writeAPI(w, 500, nil, "读取 Pro 配置失败")
		return
	}
	mode := mailFinalMode(p, cfg)
	if mode == "" {
		writeAPI(w, 409, nil, "请先在 Pro 管理启用一种收尾流程")
		return
	}
	if mode != in.Mode {
		writeAPI(w, 409, nil, "收尾设置已变化，请刷新后重新确认")
		return
	}
	if p.IsProDead() || p.ChatGPTStatus == "dead" {
		writeAPI(w, 409, nil, "账号已停用，不能执行收尾")
		return
	}
	if p.OAIConfirmationPrerequisite(mode) {
		writeAPI(w, 409, nil, "该收尾流程已完成，无需重复执行")
		return
	}
	if !p.DispositionStarted() && !proMergeTargetPinned(p) {
		_, credentials, readErr := s.store.MailAccountCredential(p.Email)
		if readErr != nil {
			writeAPI(w, 500, nil, "读取账号失败")
			return
		}
		if mode != "merge" || credentials.AccessToken == "" || credentials.RefreshToken == "" {
			if credentials.GptPassword == "" || credentials.TotpSecret == "" {
				writeAPI(w, 409, nil, "请先在邮件管理补齐 ChatGPT 密码和 2FA")
				return
			}
		}
	}
	if _, err = s.store.EnableMailProActions(p.Email, mode); err != nil {
		writeAPI(w, 409, nil, err.Error())
		return
	}
	p, err = s.startProEndJob(p.Email, lock.Unlock, nil, "manual_"+mode)
	if err != nil {
		writeAPI(w, 409, nil, err.Error())
		return
	}
	transferred = true
	writeAPI(w, http.StatusAccepted, p, "")
}

func (s *Server) manageMailOAI(w http.ResponseWriter, r *http.Request) {
	s.proAutoMu.Lock()
	defer s.proAutoMu.Unlock()
	p, ok := s.mailActionProfile(w, r)
	if !ok {
		return
	}
	// Stop/restart must reach the OAI generation guard even while its worker
	// owns the account lock. The shared OAI dispatcher serializes execution.
	if _, err := s.store.EnableMailProActions(p.Email, ""); err != nil {
		writeAPI(w, 409, nil, err.Error())
		return
	}
	s.manageOAIConfirmation(w, r, true)
}

func (s *Server) stopMailFinalStep(w http.ResponseWriter, r *http.Request) {
	p, ok := s.mailActionProfile(w, r)
	if !ok {
		return
	}
	if p.MailProActions == nil {
		writeAPI(w, 409, nil, "尚未开始收尾流程")
		return
	}
	s.stopProAutomation(w, r)
}

// External mail accounts need not have passed through Pro provisioning to
// acquire the OAuth credentials required by the original four-step merge.
func (s *Server) prepareMailMergeCredential(ctx context.Context, p model.MailAccountProfile, c model.MailAccountCredentials) error {
	if p.MailProActions == nil || c.AccessToken != "" && c.RefreshToken != "" {
		return nil
	}
	result, err := s.executeOpenAILoginContext(ctx, p.Email, "codex_rt", func(message string) {
		s.auditProEvent(p, "merge", "running", "", message, nil)
	}, nil)
	if err != nil {
		return err
	}
	if result["success"] != true {
		return errors.New(firstNonEmpty(oauthErrorText(result, nil), "登录获取凭据失败"))
	}
	at, _ := result["access_token"].(string)
	rt, _ := result["refresh_token"].(string)
	id, _ := result["id_token"].(string)
	info, err := workflow.DecodeUserInfo(at)
	if err != nil || !strings.EqualFold(info.Email, p.Email) || rt == "" {
		return errors.New("登录凭据不完整或账号不匹配")
	}
	expiry, _ := workflow.AccessTokenExpiry(at)
	return s.store.SaveMailAccountOAuthBundle(p.Email, at, rt, id, info.AccountID, info.UserID, expiry)
}
