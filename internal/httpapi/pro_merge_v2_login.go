package httpapi

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"time"

	"chatgpt-space-merge/internal/model"
)

const (
	mergeV2TeamReloginRetries = 2
	mergeV2TeamReloginDelay   = 5 * time.Second
)

var mergeV2DiagnosticURL = regexp.MustCompile(`https?://[^\s<>"']+`)

// Every attempt creates a new protocol process/session. Keep this budget
// separate from network retries, and never recover across a submitted POST.
func (s *Server) loginProMergeV2(ctx context.Context, p model.MailAccountProfile, provider string, wait func(context.Context, time.Duration) error) (map[string]any, error) {
	executionID := randomRegistrationID()
	for attempt := 1; ; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		latest, _, err := s.store.MailAccountCredential(p.Email)
		if err != nil {
			return nil, err
		}
		if latest.ProAuto.StopRequested {
			return nil, context.Canceled
		}
		if latest.IsProDead() {
			return nil, errProAccountDead
		}
		if latest.ProMergeV2 == nil || latest.ProMergeV2.TeamID != p.ProMergeV2.TeamID {
			return nil, errors.New("合并空间2.0目标空间已变化")
		}
		if latest.MergeV2Accepted() {
			return map[string]any{"success": true}, nil
		}
		if latest.ProMergeV2.SubmittedAt != nil {
			return nil, errMergeV2Unknown
		}
		diagnostic := func(d protocolOAuthDiagnostic) {
			s.auditMergeV2Login(p, provider, executionID, attempt, d)
		}
		diagnostic(protocolOAuthDiagnostic{Stage: "merge_v2_login", Event: "team_login_start", Message: "开始团队会话登录", Details: map[string]any{"max_attempts": mergeV2TeamReloginRetries + 1}})
		var result map[string]any
		if s.proMergeV2Login != nil {
			result, err = s.proMergeV2Login(ctx, p.Email)
		} else {
			result, err = s.executeOpenAILoginContext(ctx, p.Email, "chatgpt_at", func(message string) {
				s.auditProEvent(p, "merge_v2_login", "running", provider, message, nil)
			}, diagnostic)
		}
		diagnostic(protocolOAuthDiagnostic{Stage: "merge_v2_login", Event: "team_login_result", Message: "团队登录尝试结束", Details: oauthResultDetails(result, err)})
		// Check durable state even if a broken child reports a retryable result.
		latest, _, readErr := s.store.MailAccountCredential(p.Email)
		if readErr != nil {
			return nil, readErr
		}
		if latest.MergeV2Accepted() {
			return map[string]any{"success": true}, nil
		}
		if latest.ProMergeV2 != nil && latest.ProMergeV2.SubmittedAt != nil {
			return result, errMergeV2Unknown
		}
		if latest.ProMergeV2 == nil || latest.ProMergeV2.TeamID != p.ProMergeV2.TeamID {
			return nil, errors.New("合并空间2.0目标空间已变化")
		}
		if err != nil || result["success"] == true || result["dead"] == true || result["error_code"] != "team_session_mismatch" {
			return result, err
		}
		if attempt > mergeV2TeamReloginRetries {
			diagnostic(protocolOAuthDiagnostic{Stage: "merge_v2_login", Event: "team_relogin_exhausted", Level: "error", Message: "团队会话仍不匹配，已用尽2次重新登录，保留团队成员", Details: map[string]any{"will_retry": false}})
			return result, nil
		}
		message := fmt.Sprintf("团队会话不匹配，5秒后重新登录并选择受邀团队（第 %d/%d 次恢复）", attempt, mergeV2TeamReloginRetries)
		if _, err = s.mergeV2Update(p.Email, "login", "running", message); err != nil {
			return nil, err
		}
		diagnostic(protocolOAuthDiagnostic{Stage: "merge_v2_login", Event: "team_relogin_wait", Level: "warning", Message: message,
			Details: map[string]any{"will_retry": true, "delay_seconds": int(mergeV2TeamReloginDelay / time.Second), "next_attempt": attempt + 1}})
		if err = wait(ctx, mergeV2TeamReloginDelay); err != nil {
			return nil, err
		}
	}
}

// Reuse the nonblocking audit queue. HTTP evidence is already structured by
// the protocol runner; discard credential containers and raw bodies entirely.
func (s *Server) auditMergeV2Login(p model.MailAccountProfile, provider, executionID string, attempt int, d protocolOAuthDiagnostic) {
	details := mergeV2DiagnosticFields(d.Details, "request_id", "error", "error_type", "error_code", "stage", "http_status", "retryable", "dead", "hint", "oauth_success", "round", "round_max_attempts", "quality_attempt", "quality_max_attempts", "stop_reason", "will_retry", "delay_seconds", "next_attempt", "max_attempts", "login_method", "login_auth_status", "at_team_present", "at_team_matches", "session_team_present", "session_team_matches", "email_matches", "workspace_selected", "workspace_page")
	details["execution_id"], details["team_login_attempt"], details["protocol_event"] = executionID, attempt, d.Event
	if code, ok := d.Details["curl_code"]; ok {
		switch code.(type) {
		case int, int64, float64:
			details["curl_code"] = code
		}
	}
	if first, ok := d.Details["first_failed_request"].(map[string]any); ok {
		details["first_failed_request"] = mergeV2DiagnosticFields(first, "request_id", "stage", "http_status", "url", "error", "error_code", "error_message", "body_kind")
	}
	response := mergeV2DiagnosticFields(d.Response, "http_status", "url", "location", "continue_url", "page_type", "error_code", "error_type", "error_message", "body_kind", "body_bytes", "ok", "auth_status", "exit_ip", "ip_check_status")
	if headers, ok := d.Response["headers"].(map[string]any); ok {
		response["headers"] = mergeV2DiagnosticFields(headers, "content-type", "cf-ray", "cf-mitigated", "x-request-id", "x-openai-request-id", "retry-after")
	}
	request := mergeV2DiagnosticFields(d.Request, "request_id", "method", "url", "referer", "timeout_seconds", "impersonate", "allow_redirects")
	if proxy, ok := d.Request["proxy"].(map[string]any); ok {
		request["proxy"] = mergeV2DiagnosticFields(proxy, "configured", "name", "endpoint")
	}
	s.enqueueAuditEvent(model.AutoRotationEvent{AccountID: p.ID, Email: p.Email, AdminAccountID: p.ProMergeV2.AdminID,
		Type: "pro_merge_v2_protocol", Source: "pro_management", Provider: provider, Operation: "merge_v2_login", Stage: d.Stage,
		Message: mergeV2DiagnosticText(d.Message), Level: d.Level, HTTPStatus: d.HTTPStatus, DurationMS: d.DurationMS, Attempt: attempt,
		Request: request, Response: response, Details: details})
}

func mergeV2DiagnosticText(value string) string {
	value = mergeV2DiagnosticURL.ReplaceAllStringFunc(value, func(raw string) string {
		u, err := url.Parse(raw)
		if err != nil {
			return "[url]"
		}
		u.User, u.RawQuery, u.Fragment = nil, "", ""
		return u.String()
	})
	value = redactSensitiveText(value)
	if len(value) > 1200 {
		value = value[:1200]
	}
	return value
}

func mergeV2DiagnosticFields(input map[string]any, keys ...string) map[string]any {
	out := make(map[string]any)
	for _, key := range keys {
		switch value := input[key].(type) {
		case string:
			out[key] = mergeV2DiagnosticText(value)
		case bool, int, int64, float64:
			out[key] = value
		}
	}
	return out
}
