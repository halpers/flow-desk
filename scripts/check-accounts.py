"""Synthetic public-API acceptance; creates only disposable accounts and roles."""

import argparse
import json
import secrets
import time
from datetime import UTC, datetime
from pathlib import Path
from uuid import uuid4

import httpx

ROOT = Path(__file__).resolve().parents[1]


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--env-file", type=Path, default=ROOT / ".env")
    parser.add_argument("--output", type=Path)
    args = parser.parse_args()
    settings = dict(
        line.split("=", 1)
        for line in args.env_file.read_text().splitlines()
        if "=" in line and not line.startswith("#")
    )
    assert settings.get("MODEL_PROVIDER", "mock") == "mock", (
        "Use a disposable mock stack"
    )
    origin = "http://localhost:" + settings["WEB_HOST_PORT"]
    checks = []
    with httpx.Client(
        base_url=origin + "/api/v1",
        trust_env=False,
        timeout=15,
        headers={"Origin": origin, "X-FlowDesk-Client": "web"},
    ) as http:

        def call(method, path, body=None, expected=200, key=None, code=None):
            response = http.request(
                method,
                path,
                json=body,
                headers={"Idempotency-Key": key or str(uuid4())},
            )
            assert response.status_code == expected, (path, response.status_code)
            result = response.json()
            if code:
                assert result["code"] == code, (path, result["code"])
            return result.get("data")

        def login(account, password, expected=200):
            time.sleep(0.55)
            data = call(
                "POST",
                "/auth/login",
                {"account": account, "password": password},
                expected,
            )
            if data:
                http.headers["Authorization"] = "Bearer " + data["access_token"]
            return http.headers.get("Authorization")

        admin = login("demo.admin", settings["DEMO_PASSWORD"])
        roles = call("GET", "/admin/roles")
        builtin = {
            row["builtin_role"]: row["id"]
            for row in roles["items"]
            if row["builtin_role"]
        }
        role = call(
            "POST",
            "/admin/roles",
            {
                "name": "Synthetic accounts " + uuid4().hex[:8],
                "permissions": ["knowledge:manage", "run:observe"],
            },
            201,
        )
        password = "Test1-" + secrets.token_hex(16)
        body = {
            "account": "acceptance." + uuid4().hex[:12],
            "display_name": "Synthetic acceptance user",
            "password": password,
            "role_ids": [builtin["operator"], role["id"]],
        }
        key = str(uuid4())
        user = call("POST", "/admin/users", body, 201, key)
        assert call("POST", "/admin/users", body, 201, key) == user
        call(
            "POST",
            "/admin/users",
            {**body, "password": password + "X"},
            409,
            key,
            "IDEMPOTENCY_CONFLICT",
        )
        assert password not in json.dumps(user) and "password_hash" not in json.dumps(
            user
        )
        call(
            "PUT",
            "/admin/roles/" + builtin["operator"],
            {
                "name": "Forbidden",
                "permissions": [],
                "enabled": False,
                "expected_revision": 1,
            },
            409,
            code="BUILTIN_ROLE_IMMUTABLE",
        )
        checks.append(
            "create replay, changed-password conflict and immutable builtin roles"
        )
        old = login(user["account"], password)
        me = call("GET", "/auth/me")
        assert me["role"] == "operator" and "knowledge:manage" in me["permissions"]
        assert "account:manage" not in me["permissions"]
        call("GET", "/admin/users", expected=403)
        http.headers["Authorization"] = admin
        new_password = "Test2-" + secrets.token_hex(16)
        reset = {"password": new_password, "expected_revision": user["revision"]}
        reset_key = str(uuid4())
        user = call(
            "POST", f"/admin/users/{user['id']}/reset-password", reset, key=reset_key
        )
        http.headers["Authorization"] = old
        call("GET", "/auth/me", expected=401)
        call("POST", "/auth/refresh", expected=401)
        login(user["account"], password, 401)
        fresh = login(user["account"], new_password)
        http.headers["Authorization"] = admin
        assert (
            call(
                "POST",
                f"/admin/users/{user['id']}/reset-password",
                reset,
                key=reset_key,
            )
            == user
        )
        http.headers["Authorization"] = fresh
        call("GET", "/auth/me")
        checks.append(
            "reset revokes access and refresh; reset replay preserves fresh login"
        )
        http.headers["Authorization"] = admin
        role = call(
            "PUT",
            "/admin/roles/" + role["id"],
            {
                "name": role["name"],
                "permissions": ["audit:read"],
                "enabled": True,
                "expected_revision": role["revision"],
            },
        )
        http.headers["Authorization"] = fresh
        call("GET", "/auth/me", expected=401)
        login(user["account"], new_password)
        me = call("GET", "/auth/me")
        assert (
            "audit:read" in me["permissions"]
            and "knowledge:manage" not in me["permissions"]
        )
        http.headers["Authorization"] = admin
        users = call("GET", "/admin/users?limit=100")["items"]
        user = next(row for row in users if row["id"] == user["id"])
        update = {
            "display_name": user["display_name"],
            "enabled": False,
            "role_ids": user["role_ids"],
            "expected_revision": user["revision"],
        }
        user = call("PUT", "/admin/users/" + user["id"], update)
        login(user["account"], new_password, 401)
        http.headers["Authorization"] = admin
        call("PUT", "/admin/users/" + user["id"], update, 409, code="VERSION_CONFLICT")
        checks.append(
            "role update refreshes union and sessions; disable and optimistic versions enforced"
        )
        page = call("GET", "/admin/users?limit=1")
        assert len(page["items"]) == 1 and page["has_more"]
        next_page = call("GET", "/admin/users?limit=1&cursor=" + page["next_cursor"])
        assert page["items"][0]["id"] != next_page["items"][0]["id"]
        login("demo.operator", settings["DEMO_PASSWORD"])
        call("GET", "/admin/roles", expected=403)
        checks.append("bounded distinct pages and operator directory denial")
    report = {
        "phase": "M7.6",
        "profile": "mock",
        "completed_at": datetime.now(UTC).isoformat(),
        "checks": checks,
    }
    if args.output:
        args.output.parent.mkdir(parents=True, exist_ok=True)
        args.output.write_text(
            json.dumps(report, ensure_ascii=False, indent=2) + "\n", encoding="utf-8"
        )
    print(json.dumps(report, ensure_ascii=False))


if __name__ == "__main__":
    main()
