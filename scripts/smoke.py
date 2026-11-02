"""Synthetic local HTTP checks. Credentials and token values are never printed."""
import json
import os
from http.cookiejar import CookieJar
from pathlib import Path
from urllib.error import HTTPError
from urllib.request import HTTPCookieProcessor, Request, build_opener

root = Path(__file__).resolve().parents[1]
settings = dict(line.split("=", 1) for line in (root / ".env").read_text(encoding="utf-8-sig").splitlines() if line and not line.startswith("#"))
base = os.getenv("FLOWDESK_SMOKE_URL", "http://localhost:" + settings["WEB_HOST_PORT"])
browser = build_opener(HTTPCookieProcessor(CookieJar()))


def request(path, body=None, token="", origin=base, expected=200):
    headers = {"Content-Type": "application/json", "X-FlowDesk-Client": "web", "Origin": origin}
    if token:
        headers["Authorization"] = "Bearer " + token
    req = Request(base + path, data=json.dumps(body).encode() if body is not None else None, headers=headers)
    try:
        response = browser.open(req, timeout=15)
    except HTTPError as exc:
        response = exc
    assert response.status == expected, f"{path}: expected {expected}, got {response.status}"
    data = json.load(response)
    return data, response.headers


request("/api/v1/auth/me", expected=401)
request("/api/v1/auth/login", {"account": "demo.operator", "password": settings["DEMO_PASSWORD"]}, origin="https://untrusted.invalid", expected=403)
data, headers = request("/api/v1/auth/login", {"account": "demo.operator", "password": settings["DEMO_PASSWORD"]})
cookie = headers.get("Set-Cookie", "")
assert "HttpOnly" in cookie and "SameSite=Strict" in cookie and "Path=/api/v1/auth" in cookie
old_access = data["data"]["access_token"]
me, _ = request("/api/v1/auth/me", token=old_access)
assert me["data"]["account"] == "demo.operator" and me["data"]["role"] == "operator"
assert "knowledge:manage" not in me["data"]["permissions"]
request("/api/v1/auth/refresh", {}, origin="https://untrusted.invalid", expected=403)
rotated, _ = request("/api/v1/auth/refresh", {})
request("/api/v1/auth/me", token=old_access, expected=401)
access = rotated["data"]["access_token"]
request("/api/v1/auth/me", token=access)
request("/api/v1/auth/logout", {})
request("/api/v1/auth/me", token=access, expected=401)
request("/api/v1/auth/refresh", {}, expected=401)
print("HTTP smoke passed: authentication, CSRF origin, cookie policy, rotation, permission, logout")
