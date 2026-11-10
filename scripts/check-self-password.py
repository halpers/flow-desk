"""Own-password acceptance using only newly created synthetic mock accounts."""

import argparse
import json
import secrets
from datetime import UTC, datetime
from pathlib import Path
from uuid import uuid4

import httpx
from acceptance_http import login_with_backoff

ROOT = Path(__file__).resolve().parents[1]


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--env-file", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    assert args.env_file.resolve().is_relative_to(ROOT / "work")
    config = dict(
        line.split("=", 1)
        for line in args.env_file.read_text(encoding="utf-8-sig").splitlines()
        if "=" in line and not line.startswith("#")
    )
    assert config["MODEL_PROVIDER"] == "mock", "Use a disposable mock stack"
    origin = "http://localhost:" + config["WEB_HOST_PORT"]
    headers = {"Origin": origin, "X-FlowDesk-Client": "web"}
    checks = []
    with (
        httpx.Client(
            base_url=origin + "/api/v1", headers=headers, trust_env=False, timeout=20
        ) as admin,
        httpx.Client(
            base_url=origin + "/api/v1", headers=headers, trust_env=False, timeout=20
        ) as owner,
        httpx.Client(
            base_url=origin + "/api/v1", headers=headers, trust_env=False, timeout=20
        ) as other,
    ):

        def call(http, method, path, body=None, expected=200, code=None):
            response = http.request(method, path, json=body)
            assert response.status_code == expected, (path, response.status_code)
            result = response.json()
            if code:
                assert result["code"] == code, (path, result.get("code"))
            if path == "/auth/password":
                assert response.headers.get("cache-control") == "no-store"
            return response

        def login(http, account, password, expected=200):
            response = login_with_backoff(http, account, password)
            assert response.status_code == expected, ("login", response.status_code)
            if response.is_success:
                http.headers["Authorization"] = (
                    "Bearer " + response.json()["data"]["access_token"]
                )
            return response

        login(admin, "demo.admin", config["DEMO_PASSWORD"])
        roles = call(admin, "GET", "/admin/roles").json()["data"]["items"]
        operator = next(row["id"] for row in roles if row["builtin_role"] == "operator")
        account = "password." + uuid4().hex[:12]
        old_password = "Synthetic-old1-" + secrets.token_hex(12)
        new_password = "Synthetic-new2-" + secrets.token_hex(12)
        created = admin.post(
            "/admin/users",
            headers={"Idempotency-Key": str(uuid4())},
            json={
                "account": account,
                "display_name": "Synthetic password acceptance",
                "password": old_password,
                "role_ids": [operator],
            },
        )
        assert created.status_code == 201, (
            "create synthetic account",
            created.status_code,
        )
        user = created.json()["data"]
        login(owner, account, old_password)
        login(other, account, old_password)
        original_access = owner.headers["Authorization"]
        body = {
            "current_password": old_password,
            "new_password": new_password,
            "confirm": True,
        }
        rejected = owner.post(
            "/auth/password", json=body, headers={"Origin": "http://untrusted.invalid"}
        )
        assert (
            rejected.status_code == 403 and rejected.json()["code"] == "ORIGIN_REJECTED"
        )
        call(
            owner,
            "POST",
            "/auth/password",
            {**body, "tenant_id": "untrusted"},
            400,
            "INVALID_ARGUMENT",
        )
        checks.append("trusted-origin and strict caller-free body enforced")
        call(
            owner,
            "POST",
            "/auth/password",
            {**body, "current_password": "Wrong-password-123"},
            400,
            "CURRENT_PASSWORD_INVALID",
        )
        call(
            owner,
            "POST",
            "/auth/password",
            {**body, "new_password": old_password},
            400,
            "PASSWORD_UNCHANGED",
        )
        call(
            owner,
            "POST",
            "/auth/password",
            {**body, "new_password": "short1"},
            400,
            "INVALID_ARGUMENT",
        )
        call(owner, "GET", "/auth/me")
        checks.append(
            "wrong current, unchanged and weak new passwords leave the session usable"
        )
        success = call(owner, "POST", "/auth/password", body)
        assert "Max-Age=0" in success.headers.get("set-cookie", "")
        assert old_password not in success.text and new_password not in success.text
        checks.append(
            "owner change clears refresh cookie and returns no credential material"
        )
        for http in (owner, other):
            call(http, "GET", "/auth/me", expected=401)
            call(http, "POST", "/auth/refresh", expected=401)
        checks.append("every access and refresh family is revoked")
        login(owner, account, old_password, 401)
        login(owner, account, new_password)
        fresh_access = owner.headers["Authorization"]
        owner.headers["Authorization"] = original_access
        call(owner, "POST", "/auth/password", body, 401)
        owner.headers["Authorization"] = fresh_access
        call(owner, "GET", "/auth/me")
        checks.append(
            "new-password login resolves a lost reply and old write cannot revoke the fresh login"
        )
        reset_password = "Synthetic-admin3-" + secrets.token_hex(12)
        reset = admin.post(
            f"/admin/users/{user['id']}/reset-password",
            headers={"Idempotency-Key": str(uuid4())},
            json={
                "password": reset_password,
                "expected_revision": user["revision"] + 1,
            },
        )
        assert reset.status_code == 200, ("admin recovery", reset.status_code)
        call(owner, "GET", "/auth/me", expected=401)
        login(owner, account, new_password, 401)
        login(owner, account, reset_password)
        call(owner, "GET", "/auth/me")
        checks.append(
            "administrator recovery remains compatible with owner changes and revision increments"
        )
    report = {
        "phase": "M7.28",
        "profile": "disposable-mock",
        "checks": checks,
        "completed_at": datetime.now(UTC).isoformat(),
    }
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(json.dumps(report, indent=2) + "\n", encoding="utf-8")
    print(json.dumps({"phase": "M7.28", "passed": len(checks)}))


if __name__ == "__main__":
    main()
