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

    def submit_workspace_and_org(self, referer_url):
        # Never pick the first/default workspace. The server validates membership.
        url = "https://auth.openai.com/api/accounts/workspace/select"
        resp = self.request(url, method="POST", json_data={"workspace_id": self.target_team},
                            headers=self.headers(url, {"Accept": "application/json", "Origin": "https://auth.openai.com", "Referer": referer_url}), timeout=45)
        if resp.status in {301, 302, 303, 307, 308} and resp.location():
            return urljoin(url, resp.location())
        if resp.status != 200:
            raise RuntimeError(f"选择目标团队失败：HTTP {resp.status}")
        next_url = self.extract_continue_url(resp.json())
        if not next_url:
            raise RuntimeError("选择目标团队未返回后续地址，停止以免使用错误空间")
        return self.normalize_auth_url(next_url)

    def follow_callback(self, callback_url):
        current = self.normalize_auth_url(callback_url)
        for _ in range(18):
            parsed = urlsplit(current)
            if parsed.scheme != "https" or parsed.hostname not in {"chatgpt.com", "auth.openai.com"}:
                raise RuntimeError("团队登录返回非预期跳转地址")
            resp = self.request(current, headers=self.headers(current, {"Accept": "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8"}))
            if 300 <= resp.status < 400 and resp.location():
                current = urljoin(current, resp.location())
                parsed = urlsplit(current)
                if parsed.hostname == "chatgpt.com" and not parsed.path.startswith("/api/auth/"):
                    return
                continue
            if resp.status == 200 and parsed.hostname == "auth.openai.com" and self.is_workspace_or_consent_url(current):
                current = self.submit_workspace_and_org(current)
                continue
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
        # The actual AT must carry the target; a requested workspace is not evidence.
        if token_team != self.target_team or (session_team and session_team != self.target_team):
            raise RuntimeError("登录凭据未切换到受邀团队，禁止提交导出停用")
        user = session.get("user") or {}
        email = user.get("email") or (claims.get("https://api.openai.com/profile") or {}).get("email") or claims.get("email")
        if not email or email.casefold() != str(self.payload.get("email") or "").casefold():
            raise RuntimeError("团队会话邮箱与子号不一致或无法确认")
        user_id = auth.get("chatgpt_user_id") or user.get("id")
        if not user_id:
            raise RuntimeError("团队会话缺少子号用户 ID")
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
        try:
            # Deliberately bypass login request retries; exactly one destructive POST.
            resp = self.web_session.request("POST", EXPORT_URL, headers=headers,
                                            timeout=60, allow_redirects=False,
                                            impersonate="chrome", default_headers=False)
        except Exception:
            return {"success": False, "retryable": False, "error_code": "export_uncertain", "error": "导出停用请求中断，结果待确认，保留团队成员"}
        try:
            data = resp.json()
        except (ValueError, TypeError):
            data = {}
        status = int(resp.status_code)
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
