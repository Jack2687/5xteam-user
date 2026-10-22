"""Account deletion worker. Secrets stay on stdin; mutations are never retried."""
import json
import random
import sys
import time
import uuid
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent / 'codex_runtime'))
from protocol_codex_oauth import rpc

FLOW = 'chatgpt_account_deactivate'
BASE = 'https://chatgpt.com'
ACCOUNTS = '/backend-api/accounts/check/v4-2023-04-27'
USER_AGENT = ('Mozilla/5.0 (Windows NT 10.0; Win64; x64) '
              'AppleWebKit/537.36 (KHTML, like Gecko) '
              'Chrome/142.0.0.0 Safari/537.36')
ACCEPT_LANGUAGE = 'en-US,en;q=0.9'
CLIENT_HINTS = {
    'sec-ch-ua': '"Chromium";v="142", "Not-A.Brand";v="24", "Google Chrome";v="142"',
    'sec-ch-ua-mobile': '?0',
    'sec-ch-ua-platform': '"Windows"',
    'sec-ch-ua-full-version': '"142.0.7444.175"',
    'sec-ch-ua-full-version-list': ('"Chromium";v="142.0.7444.175", "Not-A.Brand";v="24.0.0.0", '
                                    '"Google Chrome";v="142.0.7444.175"'),
    'sec-ch-ua-arch': '"x86"',
    'sec-ch-ua-bitness': '"64"',
    'sec-ch-ua-model': '""',
    'sec-ch-ua-platform-version': '"10.0.0"',
}


def trace_headers():
    # Match the standalone script; each request gets fresh tracing identifiers.
    fragment = f'{random.getrandbits(64):016x}'
    timestamp = f'{int(time.time()):08x}'
    return {
        'traceparent': f'00-0000000000000000{fragment}-{random.getrandbits(64):016x}-01',
        'x-datadog-trace-id': str(random.getrandbits(63)),
        'x-datadog-parent-id': str(random.getrandbits(63)),
        'x-datadog-sampling-priority': '1',
        'x-datadog-origin': 'rum',
        'x-datadog-tags': f'_dd.p.id={fragment},_dd.p.tid={timestamp}00000000,_dd.b.sr=1',
    }


def common_headers(referer=None):
    return {
        'Accept': 'application/json', 'Referer': referer or BASE + '/', 'Origin': BASE,
        'User-Agent': USER_AGENT, 'Accept-Language': ACCEPT_LANGUAGE,
        'Accept-Encoding': 'gzip, deflate, br, zstd',
        'Sec-Fetch-Dest': 'empty', 'Sec-Fetch-Mode': 'cors', 'Sec-Fetch-Site': 'same-origin',
        'priority': 'u=1, i', **CLIENT_HINTS, **trace_headers(),
    }


def data_of(response):
    try:
        value = response.json()
        return value if isinstance(value, dict) else {}
    except (ValueError, TypeError):
        return {}


def request_error(response):
    # Never echo response bodies containing account data, cookies or tokens.
    return '上游请求失败：HTTP ' + str(response.status_code)


def account_rows(data):
    rows = data.get('accounts', {})
    if isinstance(rows, dict):
        rows = rows.values()
    return [r.get('account', {}) for r in rows if isinstance(r, dict)]


def sentinel(session, did, ua):
    from manager_oauth.upstream import _SentinelTokenGenerator
    generator = _SentinelTokenGenerator(did, ua)
    initial = generator.generate_requirements_token()
    response = session.post(BASE + '/backend-api/sentinel/req',
                            data=json.dumps({'p': initial, 'id': did, 'flow': FLOW}),
                            headers={'Content-Type': 'text/plain;charset=UTF-8',
                                     'Accept': '*/*', 'Accept-Encoding': 'gzip, deflate, br, zstd',
                                     'Accept-Language': ACCEPT_LANGUAGE,
                                     'Sec-Fetch-Dest': 'empty', 'Sec-Fetch-Mode': 'cors',
                                     'Sec-Fetch-Site': 'same-origin',
                                     'Origin': BASE, 'Referer': BASE + '/backend-api/sentinel/frame.html'},
                            timeout=45, allow_redirects=False)
    data = data_of(response)
    if response.status_code != 200 or not data.get('token'):
        raise RuntimeError('删除验证失败：HTTP ' + str(response.status_code))
    proof = data.get('proofofwork') or {}
    p = generator.generate_token(str(proof.get('seed') or ''), str(proof.get('difficulty') or '0')) if proof.get('required') else initial
    turn = data.get('turnstile') or {}
    t = ''
    if turn.get('dx'):
        from manager_oauth.sentinel_vm import solve_turnstile_dx
        t = solve_turnstile_dx(turn['dx'], initial, user_agent=ua)
    if turn.get('required') and not t:
        raise RuntimeError('删除验证未完成，未提交删除')
    return json.dumps({'p': p, 't': t, 'c': data['token'], 'id': did, 'flow': FLOW}, separators=(',', ':'))


def run(payload, call=rpc, session=None, make_sentinel=sentinel):
    submitted = False
    checking = bool(payload.get('check_only'))
    owned_session = session is None
    try:
        if session is None:
            from curl_cffi import requests
            session = requests.Session(impersonate='chrome142', proxy=payload.get('proxy') or None)
        saved = payload.get('session') or {}
        did = str(uuid.uuid4())
        session.headers.update({'User-Agent': USER_AGENT, 'Accept-Language': ACCEPT_LANGUAGE, **CLIENT_HINTS})
        session.cookies.set('oai-did', did, domain='chatgpt.com', path='/')
        cookie = saved.get('session_token')
        if cookie:
            session.cookies.set('__Secure-next-auth.session-token', cookie, domain='chatgpt.com', path='/')
        at = saved.get('accessToken') or saved.get('access_token') or payload.get('access_token')
        if not at:
            return {'status': 'pending' if checking else 'need_login', 'message': '缺少登录凭据'}
        auth = {'Authorization': 'Bearer ' + at}
        if not checking:
            me = session.get(BASE + '/backend-api/me', headers={**common_headers(), **auth}, timeout=45, allow_redirects=False)
            if me.status_code == 401:
                return {'status': 'need_login', 'message': '登录凭据已失效'}
            if me.status_code != 200:
                raise RuntimeError(request_error(me))
            user = data_of(me)
            if str(user.get('email', '')).lower() != payload['email'].lower():
                raise RuntimeError('登录身份与目标邮箱不一致，未提交删除')
            if payload.get('user_id') and payload['user_id'] != user.get('id'):
                raise RuntimeError('登录用户 ID 与原账号不一致，未提交删除')
        response = session.get(BASE + ACCOUNTS, headers={**common_headers(), **auth}, timeout=45, allow_redirects=False)
        if response.status_code != 200:
            # Operator-defined completion rule, restricted to reconciliation
            # of a submitted deletion. A pre-submit 401 still means login failed.
            if checking and payload.get('account_id') and response.status_code == 401:
                return {'status': 'confirmed', 'confirmation_source': 'http_401',
                        'message': '核验返回 HTTP 401，按规则确认删除成功'}
            if response.status_code == 401 and not checking:
                return {'status': 'need_login', 'message': '登录凭据已失效'}
            return {'status': 'pending' if checking else 'failed', 'message': request_error(response) + ('；不能据此确认删除' if checking else '')}
        rows = account_rows(data_of(response))
        account_id = payload.get('account_id', '')
        if checking:
            account = next((x for x in rows if x.get('account_id') == account_id), None)
            if account and account.get('is_deactivated') is True:
                return {'status': 'confirmed', 'message': '原账户接口明确返回 is_deactivated=true'}
            return {'status': 'pending', 'message': '已提交，等待上游明确确认删除'}
        personal = {x.get('account_id'): x for x in rows if x.get('structure') == 'personal' and x.get('account_user_role') == 'account-owner'}
        if account_id and account_id not in personal:
            raise RuntimeError('原个人账户不在当前登录中，未提交删除')
        if not account_id and len(personal) == 1:
            account_id = next(iter(personal))
        if not account_id or account_id not in personal:
            raise RuntimeError('无法唯一确定个人账户，未提交删除')
        if personal[account_id].get('is_deactivated') is True:
            return {'status': 'already_deactivated', 'message': '上游显示个人账户已停用；没有本次删除提交记录'}
        call('identity', {'account_id': account_id, 'user_id': user['id']})
        call('stage', {'stage': 'verify'})
        token = make_sentinel(session, did, USER_AGENT)
        # Write-ahead checkpoint must commit before the one destructive POST.
        call('submitting', {})
        submitted = True
        response = session.post(BASE + '/backend-api/accounts/deactivate', data=b'',
                                headers={**common_headers(BASE + '/#settings/Account'), **auth,
                                         'Accept': 'application/json, text/plain, */*',
                                         'OAI-Device-ID': did, 'OAI-Language': 'zh-CN',
                                         'ChatGPT-Account-ID': account_id, 'OpenAI-Sentinel-Token': token,
                                         'OAI-Telemetry': '[1,null]'},
                                timeout=45, allow_redirects=False)
        data = data_of(response)
        if response.status_code == 200 and data.get('status') == 'queued':
            call('accepted', {})
            return {'status': 'queued', 'message': '删除请求已受理，等待确认'}
        # Explicit client rejection is safe to retry on a later operator action.
        if response.status_code in (400, 401, 403, 404, 409, 422, 429):
            call('rejected', {})
            submitted = False
            return {'status': 'failed', 'message': request_error(response)}
        return {'status': 'unknown', 'message': '删除提交响应未确认：HTTP ' + str(response.status_code)}
    except Exception as exc:
        # Exception type only: transport exceptions can contain proxy secrets.
        message = str(exc) if type(exc) is RuntimeError else '删除请求异常：' + type(exc).__name__
        return {'status': 'unknown' if submitted else 'pending' if checking else 'failed', 'message': message}
    finally:
        if owned_session and session is not None:
            session.close()


if __name__ == '__main__':
    result = run(json.loads(sys.stdin.readline()))
    result['success'] = result.get('status') in ('queued', 'confirmed', 'pending')
    print(json.dumps(result, ensure_ascii=False), flush=True)
