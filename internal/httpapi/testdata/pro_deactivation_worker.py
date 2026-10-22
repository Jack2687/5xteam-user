"""Run the production worker against loopback fixtures, never real services."""
import json
import runpy
import sys
import types
import urllib.error
import urllib.request
from pathlib import Path

endpoint, worker = sys.argv[1:]
assert endpoint.startswith('http://127.0.0.1:')


class Session:
    def __init__(self, **kwargs):
        assert kwargs.get('impersonate') == 'chrome142'
        self.headers = {}
        self.cookies = types.SimpleNamespace(set=lambda *a, **k: None)

    def request(self, method, url, **kwargs):
        assert url.startswith('https://chatgpt.com/')
        data = kwargs.get('data')
        if isinstance(data, str):
            data = data.encode()
        request = urllib.request.Request(endpoint + url.removeprefix('https://chatgpt.com'),
                                         data=data, method=method,
                                         headers={**self.headers, **kwargs.get('headers', {})})
        try:
            response = urllib.request.urlopen(request, timeout=10)
        except urllib.error.HTTPError as error:
            response = error
        with response:
            body, status = response.read(), response.code
        return types.SimpleNamespace(status_code=status, json=lambda: json.loads(body))

    def get(self, url, **kwargs):
        return self.request('GET', url, **kwargs)

    def post(self, url, **kwargs):
        return self.request('POST', url, **kwargs)

    def close(self):
        pass


class Generator:
    def __init__(self, *args):
        pass

    def generate_requirements_token(self):
        return 'fixture-requirements'


sys.path.insert(0, str(Path(worker).parent))
sys.path.insert(0, str(Path(worker).parent / 'codex_runtime'))
import protocol_codex_oauth
from manager_oauth import upstream
upstream._SentinelTokenGenerator = Generator
sys.modules['curl_cffi'] = types.SimpleNamespace(requests=types.SimpleNamespace(Session=Session))
runpy.run_path(worker, run_name='__main__')
