package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"chatgpt-space-merge/internal/model"
)

type proOAILoginKey struct{}
type proOAIJob struct {
	Mode   string
	Cancel context.CancelFunc
}
type proOAILoginOptions struct {
	Config map[string]any
	Call   func(string, map[string]any) (any, error)
}

func (s *Server) proOAICheckpoint(ctx context.Context, email, mode string, generation int, prepare bool, method string, args map[string]any) (any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	str := func(key string) string { v, _ := args[key].(string); return v }
	var checkpointErr error
	var session map[string]any
	autoEnabled := true
	if method == "help_session" {
		var v map[string]any
		raw, err := json.Marshal(args["session"])
		if err != nil {
			return nil, err
		}
		if err = json.Unmarshal(raw, &v); err != nil {
			return nil, err
		}
		if !strings.EqualFold(fmt.Sprint(v["email"]), email) {
			return nil, errors.New("Help 会话账号不一致")
		}
		session = v
	}
	if method == "help_sending" {
		cfg, _, _, err := s.store.ProSettings()
		if err != nil {
			return nil, err
		}
		autoEnabled = cfg.OAIConfirmation[mode].Enabled
	}
	_, err := s.store.UpdateProAccount(email, func(p *model.MailAccountProfile) {
		v := p.ProOAIConfirmations[mode]
		if v == nil || v.Generation != generation || v.Status == "stopped" || v.ConfirmedAt != nil || p.OAIBlockedByAccountStop(mode) {
			checkpointErr = errors.New("OAI 确认任务已停止或更新")
			return
		}
		now := time.Now()
		switch method {
		case "help_stage":
			stage, status := str("stage"), str("status")
			if !strings.Contains("|login|authorize|send|reply|confirm|", "|"+stage+"|") || (status != "running" && status != "completed") {
				checkpointErr = errors.New("OAI 步骤无效")
				return
			}
			if prepare && stage != "login" && stage != "authorize" {
				checkpointErr = errors.New("预备会话禁止提问")
				return
			}
			v.Step(stage, status, now)
		case "help_session":
			v.PreparedAt = &now
		case "help_prepare_failed":
			v.Step("authorize", "failed", now)
			v.Error = redactSensitiveText(str("error"))
		case "help_sending":
			if !autoEnabled && !v.Manual {
				checkpointErr = errors.New("OAI 询问开关已关闭")
				return
			}
			if prepare || v.SentAt != nil || !v.InquiryReady() {
				checkpointErr = errors.New("已有提问记录或流程未完成，禁止重复发送")
				return
			}
			v.SentAt = &now
			v.Step("send", "running", now)
		case "help_thread":
			tid := str("conversation_id")
			if prepare || v.SentAt == nil || tid == "" || len(tid) > 160 || v.ConversationID != "" && v.ConversationID != tid {
				checkpointErr = errors.New("Help 对话标识不匹配")
				return
			}
			v.ConversationID = tid
			if id := str("user_message_id"); id != "" {
				v.UserMessageID = id
			}
		case "help_reply":
			answer, id := str("answer"), str("message_id")
			if prepare || v.SentAt == nil || v.UserMessageID == "" || v.ConversationID == "" || v.ConversationID != str("conversation_id") || id == "" || len(id) > 160 || len(answer) > 65536 || strings.TrimSpace(answer) == "" {
				checkpointErr = errors.New("Help 回答缺少匹配的提问记录")
				return
			}
			v.Answer, v.MessageID, v.CheckedAt = answer, id, &now
			v.Status = model.ProOAIAnswerStatus(email, answer, model.ProOAIInquiry{SuccessMarker: v.SuccessMarker, FailureMarker: v.FailureMarker, ReviewMarker: v.ReviewMarker})
			v.NextAttemptAt = nil
			v.Error = ""
			v.Step("reply", "completed", now)
			v.Step("confirm", v.Status, now)
			if v.Status == "confirmed" {
				v.ConfirmedAt = &now
				v.ConfirmationSource = "help"
			} else if (v.Status == "unconfirmed" || v.Status == "review") && v.Attempts < v.MaxAttempts {
				next := now.Add(time.Duration(v.RetryIntervalSeconds) * time.Second)
				v.NextAttemptAt = &next
			}
			v.Replies = append(v.Replies, model.ProOAIReply{At: now, MessageID: id, Answer: answer, Status: v.Status})
			if len(v.Replies) > 10 {
				v.Replies = v.Replies[len(v.Replies)-10:]
			}
		default:
			checkpointErr = errors.New("OAI 检查点无效")
		}
	})
	if err = errors.Join(err, checkpointErr); err == nil && session != nil {
		err = s.store.SaveProOAISession(email, mode, generation, session)
		if err == nil {
			_, err = s.store.UpdateProAccount(email, func(p *model.MailAccountProfile) {
				if v := p.ProOAIConfirmations[mode]; v != nil && v.Generation == generation && v.Status != "stopped" && v.ConfirmedAt == nil {
					v.FreshLogin = false
				}
			})
		}
	}
	return true, err
}

func (s *Server) executeProOAI(ctx context.Context, email string, opts *proOAILoginOptions) (map[string]any, error) {
	ctx = context.WithValue(ctx, proOAILoginKey{}, opts)
	if s.proOAILogin != nil {
		return s.proOAILogin(ctx, email)
	}
	if snapshot, ok := opts.Config["session"].(map[string]any); ok {
		if proxy, ok := snapshot["proxy"].(string); ok && proxy != "" {
			_, c, err := s.store.MailAccountCredential(email)
			if err != nil {
				return nil, err
			}
			return s.executeOpenAILoginWithProxy(ctx, email, proxy, "Help 原会话代理", 1, "chatgpt_at", selectOAuthLogin("password_totp", c), nil, nil)
		}
	}
	return s.executeOpenAILoginContext(ctx, email, "chatgpt_at", nil, nil)
}

func (s *Server) runProOAI(ctx context.Context, email, mode string) error {
	cfg, _, _, err := s.store.ProSettings()
	if err != nil {
		return err
	}
	var run bool
	var generation int
	p, err := s.store.UpdateProAccount(email, func(p *model.MailAccountProfile) {
		v := p.ProOAIConfirmations[mode]
		if ctx.Err() != nil || !v.Due(time.Now()) || (!cfg.OAIConfirmation[mode].Enabled && !v.Manual) || p.OAIBlockedByAccountStop(mode) || p.ProWorkflowRunning {
			return
		}
		if v.MaxAttempts < 1 {
			v.MaxAttempts = 3
		}
		if v.RetryIntervalSeconds < 1 {
			v.RetryIntervalSeconds = 300
		}
		if v.Attempts >= v.MaxAttempts {
			v.NextAttemptAt = nil
			v.Status = "failed"
			v.Error = "已达到总尝试次数，请手动重试"
			return
		}
		// A complete negative or inconclusive answer permits a later follow-up.
		// An unknown send/partial stream retains the original send checkpoint.
		if v.Status == "unconfirmed" || v.Status == "review" {
			v.SentAt = nil
			v.UserMessageID = ""
			v.MessageID = ""
		}
		v.Generation++
		if v.OrderID == "" {
			v.OrderID = p.ProAuto.OrderID
		}
		generation = v.Generation
		v.Attempts++
		v.Status = "running"
		v.NextAttemptAt = nil
		v.Error = ""
		if v.RequestID == "" || v.SentAt == nil {
			v.RequestID = fmt.Sprintf("oai-%s-%s-%d", p.ID, mode, time.Now().UnixNano())
		}
		if v.Prompt == "" || v.SentAt == nil {
			inquiry := cfg.OAIInquiry.Normalized()
			v.Prompt = inquiry.Question(email)
			v.SuccessMarker, v.FailureMarker, v.ReviewMarker = inquiry.SuccessMarker, inquiry.FailureMarker, inquiry.ReviewMarker
		}
		v.Step("wait", "completed", time.Now())
		run = true
	})
	if err != nil || !run {
		return err
	}
	v := p.ProOAIConfirmations[mode]
	var snapshot map[string]any
	if !v.FreshLogin {
		snapshot, err = s.store.ProOAISession(email, mode)
	}
	if err != nil {
		return s.finishProOAIError(email, mode, generation, err)
	}
	opts := &proOAILoginOptions{Config: map[string]any{"session": snapshot, "conversation_id": v.ConversationID, "user_message_id": v.UserMessageID, "request_id": v.RequestID, "prompt": v.Prompt}}
	if v.SentAt != nil {
		opts.Config["sent_at"] = v.SentAt.UTC().Format(time.RFC3339Nano)
	}
	opts.Call = func(method string, args map[string]any) (any, error) {
		return s.proOAICheckpoint(ctx, email, mode, generation, false, method, args)
	}
	result, err := s.executeProOAI(ctx, email, opts)
	if err == nil && result["success"] != true {
		err = errors.New(firstNonEmpty(oauthErrorText(result, nil), "Help 询问未完成"))
	}
	if err == nil {
		current, _, readErr := s.store.MailAccountCredential(email)
		err = readErr
		if err == nil {
			r := current.ProOAIConfirmations[mode]
			if r != nil && r.Generation == generation && r.Status == "running" {
				err = errors.New("Help 未返回完整回答")
			}
		}
	}
	if err != nil {
		return s.finishProOAIError(email, mode, generation, err)
	}
	return nil
}

func (s *Server) finishProOAIError(email, mode string, generation int, cause error) error {
	_, err := s.store.UpdateProAccount(email, func(p *model.MailAccountProfile) {
		v := p.ProOAIConfirmations[mode]
		if v == nil || v.Generation != generation || v.Status != "running" || v.ConfirmedAt != nil {
			return
		}
		now := time.Now()
		v.Status = "failed"
		v.Error = redactSensitiveText(cause.Error())
		if len(v.Error) > 1200 {
			v.Error = v.Error[:1200]
		}
		v.Step(v.Stage, "failed", now)
		if v.Attempts < v.MaxAttempts {
			next := now.Add(time.Duration(v.RetryIntervalSeconds) * time.Second)
			v.NextAttemptAt = &next
		}
	})
	return errors.Join(cause, err)
}

// Prepare a support session during the deletion login, before deletion makes
// the account unavailable. Failure to authorize Help does not fake confirmation.
func (s *Server) proOAIDeletionContext(ctx context.Context, email string) (context.Context, error) {
	cfg, _, _, err := s.store.ProSettings()
	if err != nil {
		return ctx, err
	}
	rule := cfg.OAIConfirmation["deactivate"]
	if !rule.Enabled {
		return ctx, nil
	}
	var gen int
	var prepare bool
	_, err = s.store.UpdateProAccount(email, func(p *model.MailAccountProfile) {
		if p.ProOAIConfirmations == nil {
			p.ProOAIConfirmations = map[string]*model.ProOAIConfirmation{}
		}
		v := p.ProOAIConfirmations["deactivate"]
		if v != nil && !v.CompletedAt.IsZero() {
			return
		}
		if v == nil {
			v = model.NewProOAIConfirmation("deactivate", rule)
			p.ProOAIConfirmations["deactivate"] = v
		}
		v.Generation++
		gen = v.Generation
		prepare = true
	})
	if err != nil || !prepare {
		return ctx, err
	}
	opts := &proOAILoginOptions{Config: map[string]any{"prepare_only": true}}
	opts.Call = func(method string, args map[string]any) (any, error) {
		return s.proOAICheckpoint(ctx, email, "deactivate", gen, true, method, args)
	}
	return context.WithValue(ctx, proOAILoginKey{}, opts), nil
}

func (s *Server) startProOAIWorker(ctx context.Context) {
	s.proAutoWG.Add(1)
	go func() {
		defer s.proAutoWG.Done()
		ticker := time.NewTicker(3 * time.Second)
		defer ticker.Stop()
		var synced time.Time
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-ticker.C:
				if now.Sub(synced) >= 30*time.Second {
					if err := s.syncProOAIRefunds(); err != nil {
						continue
					}
					synced = now
				}
				// New settings apply to admission on every sweep. Reducing the
				// limit drains running work without interrupting its conversations.
				s.dispatchProOAI(ctx)
			}
		}
	}()
}

func (s *Server) syncProOAIRefunds() error {
	s.proOAIMu.Lock()
	defer s.proOAIMu.Unlock()
	emails, err := s.store.SyncProOAIRefundConfirmations()
	if err != nil {
		return err
	}
	for _, email := range emails {
		if value, ok := s.proOAIJobs.Load(email); ok {
			job := value.(*proOAIJob)
			p, _, readErr := s.store.MailAccountCredential(email)
			if readErr != nil {
				return readErr
			}
			if v := p.ProOAIConfirmations[job.Mode]; v != nil && v.ConfirmationSource == "card_return" {
				job.Cancel()
			}
		}
	}
	return nil
}

func (s *Server) dispatchProOAI(ctx context.Context) {
	s.proOAIMu.Lock()
	defer s.proOAIMu.Unlock()
	cfg, _, _, err := s.store.ProSettings()
	if err != nil || ctx.Err() != nil {
		return
	}
	active := 0
	s.proOAIJobs.Range(func(_, _ any) bool { active++; return true })
	if active >= cfg.OAIConfirmationConcurrency {
		return
	}
	items, err := s.store.ProOAIWork(time.Now(), false)
	if err != nil {
		return
	}
	for _, item := range items {
		if item.Blocked || (!item.Manual && !cfg.OAIConfirmation[item.Mode].Enabled) {
			continue
		}
		if ctx.Err() != nil || active >= cfg.OAIConfirmationConcurrency {
			return
		}
		value, _ := s.proLocks.LoadOrStore("account:"+item.Email, &sync.Mutex{})
		lock := value.(*sync.Mutex)
		if !lock.TryLock() {
			continue
		}
		if s.proLoginAlreadyRunning(item.Email) {
			lock.Unlock()
			continue
		}
		call, cancel := context.WithTimeout(ctx, 8*time.Minute)
		job := &proOAIJob{Mode: item.Mode, Cancel: cancel}
		s.proOAIJobs.Store(item.Email, job)
		active++
		s.proAutoWG.Add(1)
		go func() {
			defer s.proAutoWG.Done()
			defer lock.Unlock()
			defer cancel()
			defer s.proOAIJobs.CompareAndDelete(item.Email, job)
			_ = s.runProOAI(call, item.Email, item.Mode)
		}()
	}
}

func (s *Server) manageProOAI(w http.ResponseWriter, r *http.Request) {
	s.manageOAIConfirmation(w, r, false)
}

func (s *Server) manageOAIConfirmation(w http.ResponseWriter, r *http.Request, mail bool) {
	email := strings.ToLower(strings.TrimSpace(r.PathValue("email")))
	var in struct {
		Mode    string `json:"mode"`
		Action  string `json:"action"`
		OrderID string `json:"order_id"`
	}
	if decodeJSON(w, r, &in, 4096) != nil {
		return
	}
	if (in.Action != "retry" && in.Action != "stop" && in.Action != "restart") || !validOAIMode(in.Mode) {
		writeAPI(w, 400, nil, "OAI 操作无效")
		return
	}
	if err := s.syncProOAIRefunds(); err != nil {
		writeAPI(w, 500, nil, "读取回款确认状态失败")
		return
	}
	s.proOAIMu.Lock()
	defer s.proOAIMu.Unlock()
	cfg, _, _, err := s.store.ProSettings()
	if err != nil {
		writeAPI(w, 500, nil, err.Error())
		return
	}
	if mail && in.Action != "stop" {
		p, c, readErr := s.store.MailAccountCredential(email)
		if readErr != nil {
			writeAPI(w, 404, nil, "邮件账号不存在")
			return
		}
		if err := s.checkMailActionAvailable(p); err != nil {
			writeAPI(w, 409, nil, err.Error())
			return
		}
		if p.ProOAIConfirmations[in.Mode] == nil {
			mode := mailFinalMode(p, cfg)
			if mode == "" {
				mode = cfg.Disposition()
			}
			if in.Mode != mode {
				writeAPI(w, 409, nil, "确认流程设置已变化，请刷新后操作")
				return
			}
			if c.GptPassword == "" || c.TotpSecret == "" {
				writeAPI(w, 409, nil, "请先在邮件管理补齐 ChatGPT 密码和 2FA")
				return
			}
		}
	}
	var actionErr error
	p, err := s.store.UpdateProAccount(email, func(p *model.MailAccountProfile) {
		v := p.ProOAIConfirmations[in.Mode]
		if (mail && p.ManagementScope != "mail") || (!mail && p.ManagementScope != "pro") {
			actionErr = errors.New("账号已移出邮件管理，请刷新")
			return
		}
		if mail && in.Action != "stop" && (p.ProWorkflowRunning || p.ProAuto.Status == "running") {
			actionErr = errors.New("收尾流程正在执行，请完成后再确认")
			return
		}
		if in.OrderID != "" && ((v != nil && v.OrderID != "" && v.OrderID != in.OrderID) || (v == nil && p.ProAuto.OrderID != "" && p.ProAuto.OrderID != in.OrderID)) {
			actionErr = errors.New("账号已关联其他开通订单，请刷新后操作")
			return
		}
		// Explicit manual catch-up is allowed for an already completed historical
		// outcome; enabling settings alone never backfills old accounts.
		if (in.Action == "retry" || in.Action == "restart") && v == nil && p.ProActionsAllowed() && p.OAIConfirmationPrerequisite(in.Mode) && !p.ProAuto.StopRequested {
			v = model.NewProOAIConfirmation(in.Mode, cfg.OAIConfirmation[in.Mode])
			v.OrderID = p.ProAuto.OrderID
			v.CompletedAt = time.Now()
			if in.Mode == "merge_v2" && p.ProMergeV2.CompletedAt != nil {
				v.CompletedAt = *p.ProMergeV2.CompletedAt
			} else if in.Mode == "deactivate" && p.ProDeactivation.ConfirmedAt != nil {
				v.CompletedAt = *p.ProDeactivation.ConfirmedAt
			} else if in.Mode == "merchant" && p.ProMerchant.CompletedAt != nil {
				v.CompletedAt = *p.ProMerchant.CompletedAt
			}
			if p.ProOAIConfirmations == nil {
				p.ProOAIConfirmations = map[string]*model.ProOAIConfirmation{}
			}
			p.ProOAIConfirmations[in.Mode] = v
		}
		if mail && (in.Action == "retry" || in.Action == "restart") && v == nil && !p.ProWorkflowRunning && p.ProAuto.Status != "running" {
			v = model.NewProOAIConfirmation(in.Mode, cfg.OAIConfirmation[in.Mode])
			now := time.Now()
			v.RequestedAt, v.Manual = &now, true
			if p.ProOAIConfirmations == nil {
				p.ProOAIConfirmations = map[string]*model.ProOAIConfirmation{}
			}
			p.ProOAIConfirmations[in.Mode] = v
		}
		if !p.ProActionsAllowed() || (!mail && p.ManagementScope != "pro") || !v.InquiryReady() {
			actionErr = errors.New("该流程尚未完成，没有 OAI 确认任务")
			return
		}
		if v.ConfirmedAt != nil && (in.Action != "restart" || v.ConfirmationSource == "card_return") {
			actionErr = errors.New("OAI 已确认，不再执行")
			return
		}
		if in.Action == "retry" && v.Status == "running" {
			actionErr = errors.New("OAI 确认正在执行")
			return
		}
		if in.Action != "stop" && p.ProAuto.StopRequested && !mail {
			actionErr = errors.New("账号全自动已停止，请先恢复账号流程")
			return
		}
		now := time.Now()
		v.Generation++
		v.NextAttemptAt = nil
		if in.Action == "stop" {
			v.Status = "stopped"
			v.Error = "已手动停止 OAI 确认"
			v.Step(v.Stage, "interrupted", now)
		} else {
			if mail {
				v.RequestedAt = &now
			}
			if in.Action == "restart" {
				// Retain history, but acquire a fresh login and Help authorization.
				v.FreshLogin = true
				v.PreparedAt, v.SentAt, v.CheckedAt, v.ConfirmedAt = nil, nil, nil, nil
				v.ConfirmationSource, v.ConversationID, v.UserMessageID, v.MessageID, v.RequestID, v.Answer = "", "", "", "", "", ""
				v.Steps, v.StepTimes = nil, nil
				v.Step("wait", "waiting", now)
			}
			if v.Status == "unconfirmed" || v.Status == "review" || v.MessageID != "" {
				v.SentAt = nil
				v.UserMessageID = ""
				v.MessageID = ""
			}
			v.Status = "waiting"
			v.Manual = true
			v.NotBefore = &now
			v.Attempts = 0
			v.Error = ""
			rule := cfg.OAIConfirmation[in.Mode]
			v.MaxAttempts = rule.MaxAttempts
			v.RetryIntervalSeconds = rule.RetryIntervalSeconds
		}
	})
	if err = errors.Join(err, actionErr); err != nil {
		writeAPI(w, 409, nil, err.Error())
		return
	}
	if in.Action == "stop" || in.Action == "restart" {
		if value, ok := s.proOAIJobs.Load(email); ok {
			job := value.(*proOAIJob)
			if job.Mode == in.Mode {
				job.Cancel()
			}
		}
	}
	writeAPI(w, 200, p, "")
}

func validOAIMode(mode string) bool {
	for _, v := range model.ProDispositionModes {
		if mode == v {
			return true
		}
	}
	return false
}
