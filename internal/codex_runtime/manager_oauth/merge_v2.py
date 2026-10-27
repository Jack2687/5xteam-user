"""Pro-only Team login and export/deactivate; original login mechanics stay shared."""
import time
from urllib.parse import urlsplit, urljoin

from . import bridge, upstream
from .adapter import ProjectProtocolLogin

EXPORT_URL = "https://chatgpt.com/backend-api/accounts/export_and_deactivate"
SAFARI_UA = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.6 Safari/605.1.15"


class TeamProtocolLogin(ProjectProtocolLogin):
    def __init__(self, job_id, payload):
        super().__init__(job_id, payload)
        self.target_team = payload["merge_v2"]["team_id"]
        self.team_session_at = None
        self.team_session = None
        self.workspace_selected = False

    def trace(self, event, message, **details):
        # Only structural evidence, never an AT, cookie or callback query.
        bridge.emit("merge_v2_team", event, message, details=details)

    def submit_workspace_and_org(self, referer_url):
        # Never pick the first/default workspace. The server validates membership.
        self.trace("workspace_select", "选择邀请对应的目标团队")
        url = "https://auth.openai.com/api/accounts/workspace/select"
        resp = self.request(url, method="POST", json_data={"workspace_id": self.target_team},
                            headers=self.headers(url, {"Accept": "application/json", "Origin": "https://auth.openai.com", "Referer": referer_url}), timeout=45)
        if resp.status in {301, 302, 303, 307, 308} and resp.location():
            self.workspace_selected = True
            return urljoin(url, resp.location())
        if resp.status != 200:
            raise RuntimeError(f"选择目标团队失败：HTTP {resp.status}")
        next_url = self.extract_continue_url(resp.json())
        if not next_url:
            raise RuntimeError("选择目标团队未返回后续地址，停止以免使用错误空间")
        self.workspace_selected = True
        return self.normalize_auth_url(next_url)

    def follow_callback(self, callback_url):
        current = self.normalize_auth_url(callback_url)
        self.trace("callback_start", "检查团队登录回调路径",
                   workspace_page=self.is_workspace_or_consent_url(current))
        for _ in range(18):
            parsed = urlsplit(current)
            if parsed.scheme != "https" or parsed.hostname not in {"chatgpt.com", "auth.openai.com"}:
                raise RuntimeError("团队登录返回非预期跳转地址")
            resp = self.request(current, headers=self.headers(current, {"Accept": "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8"}))
            if 300 <= resp.status < 400 and resp.location():
                current = urljoin(current, resp.location())
                parsed = urlsplit(current)
                if parsed.hostname == "chatgpt.com" and not parsed.path.startswith("/api/auth/"):
                    self.trace("callback_complete", "登录回调结束，等待校验实际团队会话",
                               workspace_selected=self.workspace_selected)
                    return
                continue
            if resp.status == 200 and parsed.hostname == "auth.openai.com" and self.is_workspace_or_consent_url(current):
                current = self.submit_workspace_and_org(current)
                continue
            self.trace("callback_complete", "登录回调结束，等待校验实际团队会话",
                       workspace_selected=self.workspace_selected)
            return
        raise RuntimeError("团队登录跳转超过上限")

    def _chatgpt_web_session(self):
        session = super()._chatgpt_web_session()
        self.validate_team(session)
        self.team_session_at = time.monotonic()
        self.team_session = session
        return session

    def validate_team(self, session):
        token = session.get("access_token") or session.get("accessToken") or ""
        claims = upstream.jwt_payload(token)
        auth = claims.get("https://api.openai.com/auth") or {}
        token_team = auth.get("chatgpt_account_id")
        session_team = (session.get("account") or {}).get("id")
        user = session.get("user") or {}
        email = user.get("email") or (claims.get("https://api.openai.com/profile") or {}).get("email") or claims.get("email")
        matches = dict(at_team_present=bool(token_team), at_team_matches=token_team == self.target_team,
                       session_team_present=bool(session_team), session_team_matches=session_team == self.target_team,
                       email_matches=bool(email) and email.casefold() == str(self.payload.get("email") or "").casefold(),
                       workspace_selected=self.workspace_selected)
        self.trace("team_validation", "核对登录凭据的目标团队与子号身份", **matches)
        if not email or email.casefold() != str(self.payload.get("email") or "").casefold():
            raise RuntimeError("团队会话邮箱与子号不一致或无法确认")
        user_id = auth.get("chatgpt_user_id") or user.get("id")
        if not user_id:
            raise RuntimeError("团队会话缺少子号用户 ID")
        # The Go merge workflow owns a separate, bounded recovery budget. Do not
        # let generic network retries multiply or immediately repeat this error.
        if token_team != self.target_team or (session_team and session_team != self.target_team):
            raise upstream.LoginFlowError("登录凭据未切换到受邀团队，禁止提交导出停用",
                                          code="team_session_mismatch", retryable=False)
        return token, user_id

    def submit_export(self, session):
        if not self.proxy_url:
            raise RuntimeError("合并空间2.0必须使用登录代理")
        if self.team_session_at is None or not 0 <= time.monotonic() - self.team_session_at < 120:
            return {"success": False, "retryable": True, "error_code": "team_session_expired", "error": "团队登录凭据超过120秒或时间不明，需要重新登录"}
        token, user_id = self.validate_team(session)
        bridge.protect_code(token)
        bridge.call("merge_v2_ready", team_id=self.target_team, user_id=user_id)
        # Go acknowledges this only after a durable write and stop check.
        bridge.call("merge_v2_submitting", team_id=self.target_team)
        if not 0 <= time.monotonic() - self.team_session_at < 120:
            bridge.call("merge_v2_rejected")  # Nothing has been sent.
            return {"success": False, "retryable": True, "error_code": "team_session_expired", "error": "提交前凭据已超过120秒，重新登录"}
        headers = {"Authorization": "Bearer " + token, "User-Agent": SAFARI_UA,
                   "Origin": "https://chatgpt.com", "Referer": "https://chatgpt.com", "oai-language": "zh-CN", "Accept": "application/json"}
        started = time.monotonic()
        bridge.emit("merge_v2_submit", "export_start", "提交导出停用请求，已保存提交检查点",
                    request={"method": "POST", "url": EXPORT_URL, "timeout_seconds": 60,
                             "impersonate": "chrome", "allow_redirects": False})
        try:
            # Deliberately bypass login request retries; exactly one destructive POST.
            resp = self.web_session.request("POST", EXPORT_URL, headers=headers,
                                            timeout=60, allow_redirects=False,
                                            impersonate="chrome", default_headers=False)
        except Exception as exc:
            bridge.emit("merge_v2_submit", "export_exception", "导出停用请求中断，保留未知提交检查点",
                        level="error", duration_ms=int((time.monotonic()-started)*1000),
                        details={"error_type": type(exc).__name__, "error": bridge.redact(exc)[:500],
                                 "curl_code": int(getattr(exc, "code", 0) or 0), "retryable": False})
            return {"success": False, "retryable": False, "error_code": "export_uncertain", "error": "导出停用请求中断，结果待确认，保留团队成员"}
        try:
            data = resp.json()
        except (ValueError, TypeError):
            data = {}
        status = int(resp.status_code)
        error = data.get("error") if isinstance(data, dict) else None
        code = str(error.get("code") or "") if isinstance(error, dict) else ""
        bridge.emit("merge_v2_submit", "export_response", "导出停用请求返回",
                    http_status=status, duration_ms=int((time.monotonic()-started)*1000),
                    response={"http_status": status, "error_code": code,
                              "ok": isinstance(data, dict) and data.get("success") is True},
                    details={"retryable": False})
        if 200 <= status < 300 and isinstance(data, dict) and data.get("success") is True:
            bridge.call("merge_v2_accepted")
            return {"success": True, "accepted": True, "http_status": status}
        # Only a definite rejection clears the submission checkpoint.
        if status in {400, 401, 403, 404, 422, 429}:
            error = data.get("error") if isinstance(data, dict) else None
            code = str(error.get("code") or "") if isinstance(error, dict) else ""
            bridge.call("merge_v2_rejected")
            retry = code == "invalid_workspace_selected" or status in {401, 429}
            return {"success": False, "retryable": retry, "error_code": "export_rejected", "http_status": status,
                    "error": f"导出停用被拒绝：HTTP {status}" + ("，需重新登录团队" if retry else "")}
        return {"success": False, "retryable": False, "error_code": "export_uncertain", "http_status": status,
                "error": f"导出停用返回 HTTP {status}，未明确受理；保留团队成员，需人工核实"}
