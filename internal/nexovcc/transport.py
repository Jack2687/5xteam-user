import json
import sys

# Only this allowlisted host is reachable. Secrets enter on stdin, never argv.
def main():
    p = json.load(sys.stdin)
    try:
        from curl_cffi import requests
        from curl_cffi.requests.impersonate import BrowserType
        BrowserType("chrome150")
    except Exception:
        return {"error": "环球请求需要支持 chrome150 的 curl_cffi >= 0.16，请更新本地 Python 依赖"}
    try:
        with requests.Session(impersonate="chrome150", trust_env=False) as session:
            r = session.post("https://nexovcc.com" + p["path"], json=p["body"],
                headers={"Cookie": "VCC-Web=" + p["cookie"]} if p.get("cookie") else {},
                timeout=30, allow_redirects=False)
            return {"status": r.status_code, "body": r.text, "cookie": r.cookies.get("VCC-Web", "")}
    except Exception:
        # Library errors can include request bodies. Never forward them.
        return {"error": "环球请求未收到可靠响应，请检查网络；资金操作请先核实原记录", "uncertain": True}

try:
    print(json.dumps(main(), ensure_ascii=True))
except Exception:
    print(json.dumps({"error": "环球请求处理失败", "uncertain": True}))
