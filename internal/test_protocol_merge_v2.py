"""Offline protocol contracts; never sends an account mutation to OpenAI."""
import base64
import json
import sys
import time
import unittest
from pathlib import Path
from unittest.mock import Mock, patch

sys.path.insert(0, str(Path(__file__).parent / "codex_runtime"))
from manager_oauth import adapter, merge_v2, upstream


def session(team="target-team", email="child@example.com"):
    payload={"email": email, "https://api.openai.com/auth": {"chatgpt_account_id": team, "chatgpt_user_id": "child-user"}}
    token="x."+base64.urlsafe_b64encode(json.dumps(payload).encode()).decode().rstrip("=")+".x"
    return {"accessToken": token, "access_token": token, "account": {"id": team}, "user": {"email": email}}


class MergeV2ProtocolTests(unittest.TestCase):
    def flow(self):
        f=merge_v2.TeamProtocolLogin("fixture",{"email":"child@example.com","credential_mode":"chatgpt_at","proxy":"http://user:secret@proxy.invalid:8080","merge_v2":{"team_id":"target-team"}})
        self.addCleanup(f.close)
        f.team_session_at=time.monotonic()
        return f

    def response(self,status=200,body=None):
        r=Mock(status_code=status)
        r.json.return_value={"success":True} if body is None else body
        return r

    def test_exact_single_request_and_same_session(self):
        f=self.flow();rpc=[]
        with patch.object(f.web_session,"request",return_value=self.response()) as request,patch.object(merge_v2.bridge,"call",side_effect=lambda method,**kw:rpc.append(method) or True):
            result=f.submit_export(session())
        self.assertTrue(result["success"])
        request.assert_called_once()
        args,kw=request.call_args
        self.assertEqual(args,("POST",merge_v2.EXPORT_URL))
        self.assertNotIn("json",kw);self.assertNotIn("data",kw)
        self.assertNotIn("chatgpt-account-id",{k.lower() for k in kw["headers"]})
        self.assertEqual(kw["timeout"],60);self.assertEqual(kw["impersonate"],"chrome")
        self.assertFalse(kw["allow_redirects"]);self.assertFalse(kw["default_headers"])
        self.assertEqual(kw["headers"]["User-Agent"],merge_v2.SAFARI_UA)
        self.assertEqual(kw["headers"]["Origin"],"https://chatgpt.com")
        self.assertEqual(kw["headers"]["Referer"],"https://chatgpt.com")
        self.assertEqual(kw["headers"]["oai-language"],"zh-CN")
        self.assertEqual(rpc,["merge_v2_ready","merge_v2_submitting","merge_v2_accepted"])
        self.assertNotIn("access_token",result)

    def test_strict_success_and_unknown_never_retry(self):
        for status,body in [(200,{}),(200,{"success":"true"}),(200,{"success":1}),(204,{}),(500,{"success":True}),(502,{}),(302,{})]:
            with self.subTest(status=status,body=body):
                f=self.flow();rpc=[]
                with patch.object(f.web_session,"request",return_value=self.response(status,body)) as request,patch.object(merge_v2.bridge,"call",side_effect=lambda method,**kw:rpc.append(method) or True):
                    result=f.submit_export(session())
                self.assertFalse(result["success"]);self.assertFalse(result["retryable"])
                self.assertNotIn("merge_v2_accepted",rpc);self.assertNotIn("merge_v2_rejected",rpc)
                request.assert_called_once()

    def test_transport_loss_preserves_checkpoint(self):
        f=self.flow()
        with patch.object(f.web_session,"request",side_effect=TimeoutError("timeout")) as request,patch.object(merge_v2.bridge,"call") as rpc:
            result=f.submit_export(session())
        self.assertFalse(result["retryable"]);request.assert_called_once()
        self.assertEqual([c.args[0] for c in rpc.call_args_list],["merge_v2_ready","merge_v2_submitting"])

    def test_wrong_team_or_identity_never_submits(self):
        for value in [session("personal"),session(email="someone@example.com"),{**session(),"account":{"id":"other"}}]:
            f=self.flow()
            with patch.object(f.web_session,"request") as req,patch.object(merge_v2.bridge,"call") as rpc,self.assertRaises(RuntimeError):
                f.submit_export(value)
            req.assert_not_called();rpc.assert_not_called()

    def test_missing_stale_or_future_timestamp_requires_fresh_login(self):
        for stamp in [None,time.monotonic()-120,time.monotonic()+100]:
            f=self.flow();f.team_session_at=stamp
            with patch.object(f.web_session,"request") as req,patch.object(merge_v2.bridge,"call") as rpc:
                result=f.submit_export(session())
            self.assertTrue(result["retryable"]);req.assert_not_called();rpc.assert_not_called()

    def test_stop_or_failed_checkpoint_prevents_post(self):
        for checkpoint in ["merge_v2_ready","merge_v2_submitting"]:
            f=self.flow()
            def call(method,**kwargs):
                if method==checkpoint:raise RuntimeError("stopped")
            with patch.object(f.web_session,"request") as req,patch.object(merge_v2.bridge,"call",side_effect=call),self.assertRaises(RuntimeError):
                f.submit_export(session())
            req.assert_not_called()

    def test_invalid_workspace_rejection_requests_new_login(self):
        f=self.flow()
        with patch.object(f.web_session,"request",return_value=self.response(400,{"error":{"code":"invalid_workspace_selected"}})) as request,patch.object(merge_v2.bridge,"call") as rpc:
            result=f.submit_export(session())
        self.assertTrue(result["retryable"]);self.assertFalse(result["success"]);request.assert_called_once()
        self.assertEqual(rpc.call_args.args[0],"merge_v2_rejected")

    def test_workspace_selection_and_callback_use_target_not_first(self):
        f=self.flow();calls=[]
        def request(url,**kw):
            calls.append((url,kw))
            if url.endswith("/workspace"):
                return upstream.ProtocolResponse(200,url,{},"{}")
            if url.endswith("workspace/select"):
                return upstream.ProtocolResponse(200,url,{},json.dumps({"continue_url":"https://chatgpt.com/api/auth/callback/openai?code=fixture"}))
            return upstream.ProtocolResponse(302,url,{"Location":"https://chatgpt.com/"},"{}")
        with patch.object(f,"request",side_effect=request),patch.object(f,"decode_oauth_session_cookie",return_value={"workspaces":[{"id":"wrong-first"},{"id":"target-team"}]}):
            f.follow_callback("https://auth.openai.com/workspace")
        self.assertEqual(calls[1][1]["json_data"],{"workspace_id":"target-team"})
        self.assertIn("/api/auth/callback/",calls[2][0])

    def test_adapter_reuses_login_and_keeps_credentials_private(self):
        original=merge_v2.TeamProtocolLogin;flows=[]
        class Flow(original):
            def login(self):
                flows.append(self)
                self.team_session_at=time.monotonic()
                return session()
        with patch.object(merge_v2,"TeamProtocolLogin",Flow),patch("curl_cffi.requests.Session.request",return_value=self.response()),patch.object(merge_v2.bridge,"call",return_value=True):
            result=adapter.run({"email":"child@example.com","credential_mode":"chatgpt_at","proxy":"http://proxy.invalid:8080","merge_v2":{"team_id":"target-team"},"login_mode":"password_totp","gpt_password":"fixture","totp_secret":"JBSWY3DPEHPK3PXP"})
        self.assertTrue(result["success"]);self.assertNotIn("access_token",result)
        self.assertEqual(flows[0].payload["password"],"fixture")
        self.assertEqual(flows[0].payload["_totp_secret"],"JBSWY3DPEHPK3PXP")
        self.assertIs(original.login,adapter.ProjectProtocolLogin.login)


if __name__=="__main__":unittest.main()
