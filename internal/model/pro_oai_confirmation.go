package model

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

var ProDispositionModes = []string{"merge", "deactivate", "merchant", "merge_v2"}

type ProOAIConfirmationRule struct {
	Enabled              bool `json:"enabled"`
	DelaySeconds         int  `json:"delay_seconds"`
	RetryIntervalSeconds int  `json:"retry_interval_seconds"`
	MaxAttempts          int  `json:"max_attempts"`
}

type ProOAIConfirmationSettings map[string]ProOAIConfirmationRule

func DefaultProOAIConfirmationSettings() ProOAIConfirmationSettings {
	v := ProOAIConfirmationSettings{}
	for _, mode := range ProDispositionModes {
		v[mode] = ProOAIConfirmationRule{DelaySeconds: 300, RetryIntervalSeconds: 300, MaxAttempts: 3}
	}
	return v
}

func (v ProOAIConfirmationSettings) Validate() error {
	for mode, rule := range v {
		switch mode {
		case "merge", "deactivate", "merchant", "merge_v2":
		default:
			return fmt.Errorf("OAI 确认流程类型无效")
		}
		if rule.DelaySeconds < 1 || rule.DelaySeconds > 604800 {
			return fmt.Errorf("OAI 询问等待时间须为 1～604800 秒")
		}
		if rule.RetryIntervalSeconds < 0 || rule.RetryIntervalSeconds > 604800 || rule.MaxAttempts < 0 || rule.MaxAttempts > 10 {
			return fmt.Errorf("OAI 重试间隔须为 1～604800 秒，总尝试次数为 1～10")
		}
	}
	return nil
}

func (v ProOAIConfirmationSettings) Normalized() ProOAIConfirmationSettings {
	out := DefaultProOAIConfirmationSettings()
	for mode := range out {
		if rule, ok := v[mode]; ok {
			if rule.DelaySeconds < 1 || rule.DelaySeconds > 604800 {
				rule.DelaySeconds = 300
			}
			if rule.RetryIntervalSeconds == 0 {
				rule.RetryIntervalSeconds = 300
			}
			if rule.MaxAttempts == 0 {
				rule.MaxAttempts = 3
			}
			out[mode] = rule
		}
	}
	return out
}

// Help confirmation remains separate from disposition success and card refund evidence.
type ProOAIConfirmation struct {
	Mode                 string               `json:"mode"`
	Status               string               `json:"status"`
	DelaySeconds         int                  `json:"delay_seconds"`
	CompletedAt          time.Time            `json:"completed_at"`
	NotBefore            *time.Time           `json:"not_before,omitempty"`
	CheckedAt            *time.Time           `json:"checked_at,omitempty"`
	ConfirmedAt          *time.Time           `json:"confirmed_at,omitempty"`
	Error                string               `json:"error,omitempty"`
	Stage                string               `json:"stage,omitempty"`
	Steps                map[string]string    `json:"steps,omitempty"`
	StepTimes            map[string]time.Time `json:"step_times,omitempty"`
	Attempts             int                  `json:"attempts"`
	MaxAttempts          int                  `json:"max_attempts"`
	RetryIntervalSeconds int                  `json:"retry_interval_seconds"`
	NextAttemptAt        *time.Time           `json:"next_attempt_at,omitempty"`
	Generation           int                  `json:"generation"`
	Manual               bool                 `json:"manual,omitempty"`
	PreparedAt           *time.Time           `json:"prepared_at,omitempty"`
	SentAt               *time.Time           `json:"sent_at,omitempty"`
	RequestID            string               `json:"request_id,omitempty"`
	ConversationID       string               `json:"conversation_id,omitempty"`
	UserMessageID        string               `json:"user_message_id,omitempty"`
	MessageID            string               `json:"message_id,omitempty"`
	Prompt               string               `json:"prompt,omitempty"`
	Answer               string               `json:"answer,omitempty"`
	Replies              []ProOAIReply        `json:"replies,omitempty"`
}

type ProOAIReply struct {
	At        time.Time `json:"at"`
	MessageID string    `json:"message_id"`
	Answer    string    `json:"answer"`
	Status    string    `json:"status"`
}

func (v *ProOAIConfirmation) Step(stage, status string, now time.Time) {
	if v.Steps == nil {
		v.Steps = map[string]string{}
	}
	if v.StepTimes == nil {
		v.StepTimes = map[string]time.Time{}
	}
	v.Stage, v.Steps[stage], v.StepTimes[stage] = stage, status, now
}

func NewProOAIConfirmation(mode string, rule ProOAIConfirmationRule) *ProOAIConfirmation {
	rule = ProOAIConfirmationSettings{mode: rule}.Normalized()[mode]
	return &ProOAIConfirmation{Mode: mode, Status: "preparing", DelaySeconds: rule.DelaySeconds, RetryIntervalSeconds: rule.RetryIntervalSeconds, MaxAttempts: rule.MaxAttempts}
}

func (v *ProOAIConfirmation) Due(now time.Time) bool {
	if v == nil || v.CompletedAt.IsZero() || v.ConfirmedAt != nil || v.Status == "confirmed" {
		return false
	}
	if v.Status == "waiting" {
		return v.NotBefore != nil && !now.Before(*v.NotBefore)
	}
	return (v.Status == "failed" || v.Status == "unconfirmed") && v.NextAttemptAt != nil && !now.Before(*v.NextAttemptAt)
}

// Treat only the completed assistant answer as evidence. A marker in the
// user's prompt, a partial stream, or a contradictory answer cannot confirm.
var oaiRefundPositive = regexp.MustCompile(`(?:退款|退费).{0,24}(?:已发起|已处理|已完成|已成功|已退还|已退回)|已(?:发起|处理|完成).{0,16}(?:退款|退费)`)
var oaiRefundUncertain = regexp.MustCompile(`(?:无法|不能|未能|尚不能|暂不能).{0,16}(?:确认|核实|查询|查看)|(?:退款|退费).{0,18}(?:未发起|未处理|不存在|未完成)|(?:没有|未找到|查不到|无).{0,12}(?:退款|退费)(?:记录|申请)|(?:尚未|还未).{0,8}(?:发起|处理|完成)|(?:如果|假如|假设).{0,16}(?:退款|退费).{0,10}(?:已发起|已处理|已完成)`)
var oaiAnswerEmail = regexp.MustCompile(`[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}`)

func ProOAIAnswerStatus(email, answer string) string {
	lines := strings.Split(strings.TrimSpace(answer), "\n")
	last := strings.Trim(strings.TrimSpace(lines[len(lines)-1]), "* _`\r。.!！")
	if last == "已失败" {
		return "unconfirmed"
	}
	if last != "已成功" {
		return "review"
	}
	body := strings.Join(lines[:len(lines)-1], "\n")
	for _, found := range oaiAnswerEmail.FindAllString(body, -1) {
		if !strings.EqualFold(found, email) {
			return "review"
		}
	}
	if !oaiRefundPositive.MatchString(body) || oaiRefundUncertain.MatchString(body) {
		return "review"
	}
	return "confirmed"
}

func (p MailAccountProfile) OAIConfirmationPrerequisite(mode string) bool {
	switch mode {
	case "merge":
		// The shared job flag also covers quota/credential refreshes. Only the
		// persisted merge steps and downstream cleanup establish completion.
		p.ProWorkflowRunning = false
		return p.ProMergeProgress() == "completed"
	case "deactivate":
		return p.DeletionConfirmed()
	case "merchant":
		return p.MerchantCompleted() && p.ProMerchant.RetiredAt != nil
	case "merge_v2":
		return p.MergeV2Completed()
	}
	return false
}

// Capture only a new completion, not an old account touched by a settings
// change or a quota refresh. Each mode retains its own result and delay.
func (p *MailAccountProfile) PrepareOAIConfirmation(previous MailAccountProfile, settings ProOAIConfirmationSettings, now time.Time) {
	for mode, rule := range settings.Normalized() {
		if previous.OAIConfirmationPrerequisite(mode) || !p.OAIConfirmationPrerequisite(mode) || (p.ProOAIConfirmations[mode] != nil && !p.ProOAIConfirmations[mode].CompletedAt.IsZero()) {
			continue
		}
		if p.ProOAIConfirmations == nil {
			p.ProOAIConfirmations = map[string]*ProOAIConfirmation{}
		}
		v := p.ProOAIConfirmations[mode]
		if v == nil {
			v = NewProOAIConfirmation(mode, rule)
		}
		v.Status, v.CompletedAt = "disabled", now
		if rule.Enabled {
			v.Status = "waiting"
			due := now.Add(time.Duration(v.DelaySeconds) * time.Second)
			v.NotBefore = &due
			v.Step("wait", "waiting", now)
		}
		p.ProOAIConfirmations[mode] = v
	}
}
