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
type proOAILoginOptions struct {
	Config map[string]any
	Call   func(string, map[string]any) (any, error)
}

func oaiPrompt(email string) string {
	return "请查询订阅邮箱【" + email + "】对应的最近一笔网页订阅退款，确认是否已经发起或处理，并给我预计入账时间和发票号。请以实际查询结果为准，不要仅凭我的描述推断；这次仅查询，不要新建退款、取消订阅或删除账户申请。\n如果确认退款已发起或已处理，请在最后一行仅输入：已成功\n如果确认退款未发起或没有退款，请在最后一行仅输入：已失败\n如果无法查询或不能确认，请在最后一行仅输入：待核实"
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
		if v == nil || v.Generation != generation || v.Status == "stopped" || v.ConfirmedAt != nil || p.ProAuto.StopRequested {
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
			if prepare || v.SentAt != nil || v.CompletedAt.IsZero() {
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
			v.Status = model.ProOAIAnswerStatus(email, answer)
			v.NextAttemptAt = nil
			v.Error = ""
			v.Step("reply", "completed", now)
			v.Step("confirm", v.Status, now)
			if v.Status == "confirmed" {
				v.ConfirmedAt = &now
			} else if v.Status == "unconfirmed" && v.Attempts < v.MaxAttempts {
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
		if !v.Due(time.Now()) || (!cfg.OAIConfirmation[mode].Enabled && !v.Manual) || p.ProAuto.StopRequested || p.ProWorkflowRunning {
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
		// A definitive negative answer permits a later follow-up in the same thread.
		// An unknown send/partial stream retains the original send checkpoint.
		if v.Status == "unconfirmed" {
			v.SentAt = nil
			v.UserMessageID = ""
			v.MessageID = ""
		}
		v.Generation++
		generation = v.Generation
		v.Attempts++
		v.Status = "running"
		v.NextAttemptAt = nil
		v.Error = ""
		if v.RequestID == "" || v.SentAt == nil {
			v.RequestID = fmt.Sprintf("oai-%s-%s-%d", p.ID, mode, time.Now().UnixNano())
		}
		if v.Prompt == "" {
			v.Prompt = oaiPrompt(email)
		}
		v.Step("wait", "completed", time.Now())
		run = true
	})
	if err != nil || !run {
		return err
	}
	v := p.ProOAIConfirmations[mode]
	snapshot, err := s.store.ProOAISession(email, mode)
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
	// Two independent workers; existing per-account locks serialize all account
	// mutations. No Pro restock or disposition concurrency slot is consumed.
	for range 2 {
		s.proAutoWG.Add(1)
		go func() {
			defer s.proAutoWG.Done()
			ticker := time.NewTicker(3 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
				}
				items, err := s.store.ProOAIWork(time.Now(), false)
				if err != nil {
					continue
				}
				for _, item := range items {
					if ctx.Err() != nil {
						return
					}
					value, _ := s.proLocks.LoadOrStore("account:"+item.Email, &sync.Mutex{})
					lock := value.(*sync.Mutex)
					if !lock.TryLock() {
						continue
					}
					call, cancel := context.WithTimeout(ctx, 8*time.Minute)
					s.proOAIJobs.Store(item.Email, cancel)
					_ = s.runProOAI(call, item.Email, item.Mode)
					s.proOAIJobs.Delete(item.Email)
					cancel()
					lock.Unlock()
				}
			}
		}()
	}
}

func (s *Server) manageProOAI(w http.ResponseWriter, r *http.Request) {
	email := strings.ToLower(strings.TrimSpace(r.PathValue("email")))
	var in struct {
		Mode   string `json:"mode"`
		Action string `json:"action"`
	}
	if decodeJSON(w, r, &in, 4096) != nil {
		return
	}
	if (in.Action != "retry" && in.Action != "stop") || !validOAIMode(in.Mode) {
		writeAPI(w, 400, nil, "OAI 操作无效")
		return
	}
	cfg, _, _, err := s.store.ProSettings()
	if err != nil {
		writeAPI(w, 500, nil, err.Error())
		return
	}
	var actionErr error
	p, err := s.store.UpdateProAccount(email, func(p *model.MailAccountProfile) {
		v := p.ProOAIConfirmations[in.Mode]
		// Explicit manual catch-up is allowed for an already completed historical
		// outcome; enabling settings alone never backfills old accounts.
		if in.Action == "retry" && v == nil && p.ManagementScope == "pro" && p.OAIConfirmationPrerequisite(in.Mode) && !p.ProAuto.StopRequested {
			v = model.NewProOAIConfirmation(in.Mode, cfg.OAIConfirmation[in.Mode])
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
		if p.ManagementScope != "pro" || v == nil || v.CompletedAt.IsZero() {
			actionErr = errors.New("该流程尚未完成，没有 OAI 确认任务")
			return
		}
		if v.ConfirmedAt != nil {
			actionErr = errors.New("OAI 已确认，不再执行")
			return
		}
		if in.Action == "retry" && v.Status == "running" {
			actionErr = errors.New("OAI 确认正在执行")
			return
		}
		if in.Action == "retry" && p.ProAuto.StopRequested {
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
			if v.Status == "unconfirmed" || v.MessageID != "" {
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
	if in.Action == "stop" {
		if cancel, ok := s.proOAIJobs.Load(email); ok {
			cancel.(context.CancelFunc)()
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
