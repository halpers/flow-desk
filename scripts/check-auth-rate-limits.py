"""Two-API authentication budget acceptance; disposable mock data only."""

import argparse
import json
from datetime import UTC, datetime, timedelta
from pathlib import Path
from urllib.parse import quote
from uuid import uuid4

import httpx
import psycopg
from acceptance_http import login_with_backoff

ROOT = Path(__file__).resolve().parents[1]
SYNTHETIC_PASSWORD = "Synthetic-rate-password-123"


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--env-file", type=Path, required=True)
    parser.add_argument("--api-two", required=True)
    parser.add_argument("--fixture", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--phase", choices=("prepare", "verify"), required=True)
    args = parser.parse_args()
    for path in (args.env_file, args.fixture, args.output):
        assert path.resolve().is_relative_to(ROOT / "work")
    settings = dict(
        line.split("=", 1)
        for line in args.env_file.read_text(encoding="utf-8-sig").splitlines()
        if "=" in line and not line.startswith("#")
    )
    assert settings["MODEL_PROVIDER"] == "mock", "Use a disposable mock stack"
    origin = "http://localhost:" + settings["WEB_HOST_PORT"]
    urls = ["http://localhost:" + settings["API_HOST_PORT"] + "/api/v1", args.api_two]
    assert all(
        url.startswith(("http://localhost:", "http://127.0.0.1:")) for url in urls
    )
    database_url = (
        "postgresql://flowdesk_owner:"
        + quote(settings["POSTGRES_PASSWORD"], safe="")
        + "@localhost:"
        + settings["PG_HOST_PORT"]
        + "/flowdesk?options=-csearch_path%3Dbusiness"
    )
    checks = []
    with psycopg.connect(database_url, autocommit=True, connect_timeout=5) as db:
        clients = [
            httpx.Client(
                base_url=url,
                timeout=15,
                trust_env=False,
                headers={"Origin": origin, "X-FlowDesk-Client": "web"},
            )
            for url in urls
        ]
        try:

            def login(client, account, password):
                response = login_with_backoff(client, account, password)
                assert response.status_code == 200, (
                    "synthetic login",
                    response.status_code,
                )
                client.headers["Authorization"] = (
                    "Bearer " + response.json()["data"]["access_token"]
                )

            def blocked(response, code):
                assert response.status_code == 429, ("rate", response.status_code)
                assert response.json()["code"] == code
                assert int(response.headers["Retry-After"]) >= 1
                assert response.headers.get("cache-control") == "no-store"

            if args.phase == "prepare":
                login(clients[0], "demo.admin", settings["DEMO_PASSWORD"])
                roles = clients[0].get("/admin/roles")
                assert roles.status_code == 200
                role_id = next(
                    row["id"]
                    for row in roles.json()["data"]["items"]
                    if row["builtin_role"] == "operator"
                )
                account = "shared.rate." + uuid4().hex[:10]
                created = clients[0].post(
                    "/admin/users",
                    headers={"Idempotency-Key": str(uuid4())},
                    json={
                        "account": account,
                        "display_name": "Synthetic shared rate",
                        "password": SYNTHETIC_PASSWORD,
                        "role_ids": [role_id],
                    },
                )
                assert created.status_code == 201
                user = created.json()["data"]
                fixture = {"account": account, "user_id": user["id"]}
                for client in clients:
                    login(client, account, SYNTHETIC_PASSWORD)
                body = {
                    "current_password": "Synthetic-wrong-password-456",
                    "new_password": "Synthetic-replacement-789",
                    "confirm": True,
                }
                for index in range(5):
                    response = clients[index % 2].post("/auth/password", json=body)
                    assert (
                        response.status_code == 400
                        and response.json()["code"] == "CURRENT_PASSWORD_INVALID"
                    ), ("wrong password", response.status_code)
                for client in clients:
                    blocked(
                        client.post("/auth/password", json=body),
                        "PASSWORD_CHANGE_RATE_LIMITED",
                    )
                checks.append(
                    "two independent APIs share the same trusted-account password budget"
                )
                # Seed synthetic future debt for deterministic public-handler checks;
                # production admission and contention are covered in DB tests.
                db.execute(
                    "INSERT INTO auth_rate_limits(id,bucket_kind,available_at) VALUES ('login_global','login_global',clock_timestamp()+interval '30 seconds') ON CONFLICT(id) DO UPDATE SET available_at=clock_timestamp()+interval '30 seconds'"
                )
                for index, client in enumerate(clients):
                    response = client.post(
                        "/auth/login",
                        json={
                            "account": "Synthetic-unknown-account-" + str(index),
                            "password": "Synthetic-unused-password-123",
                        },
                        headers={"X-Forwarded-For": "192.0.2." + str(index + 1)},
                    )
                    blocked(response, "RATE_LIMITED")
                checks.append(
                    "login budget and Retry-After are shared regardless of account input or forwarded IP"
                )
                keys = db.execute(
                    "SELECT id FROM auth_rate_limits WHERE bucket_kind='login_global'"
                ).fetchall()
                assert keys == [("login_global",)]
                db.execute(
                    "UPDATE auth_rate_limits SET available_at=clock_timestamp() WHERE id='login_global'"
                )
                checks.append(
                    "unauthenticated account strings and IPs create no per-input buckets"
                )
                fixture["available_at"] = (
                    db.execute(
                        "SELECT available_at FROM auth_rate_limits WHERE bucket_kind='password_account' AND user_id=%s",
                        (fixture["user_id"],),
                    )
                    .fetchone()[0]
                    .isoformat()
                )
                fixture["checks"] = checks
                args.fixture.parent.mkdir(parents=True, exist_ok=True)
                args.fixture.write_text(json.dumps(fixture) + "\n", encoding="utf-8")
                print(json.dumps({"phase": "M7.29-prepare", "passed": len(checks)}))
                return

            fixture = json.loads(args.fixture.read_text(encoding="utf-8"))
            checks.extend(fixture["checks"])
            persisted = db.execute(
                "SELECT available_at FROM auth_rate_limits WHERE bucket_kind='password_account' AND user_id=%s",
                (fixture["user_id"],),
            ).fetchone()[0]
            assert persisted.isoformat() == fixture["available_at"], (
                "restart changed persisted budget"
            )
            for client in clients:
                login(client, fixture["account"], SYNTHETIC_PASSWORD)
                schedule = db.execute(
                    "SELECT available_at FROM auth_rate_limits WHERE bucket_kind='password_account' AND user_id=%s",
                    (fixture["user_id"],),
                ).fetchone()[0]
                response = client.post(
                    "/auth/password",
                    json={
                        "current_password": "Synthetic-wrong-password-456",
                        "new_password": "Synthetic-replacement-789",
                        "confirm": True,
                    },
                )
                next_schedule, observed_now = db.execute(
                    "SELECT available_at,clock_timestamp() FROM auth_rate_limits WHERE bucket_kind='password_account' AND user_id=%s",
                    (fixture["user_id"],),
                ).fetchone()
                if response.status_code == 429:
                    blocked(response, "PASSWORD_CHANGE_RATE_LIMITED")
                    assert next_schedule == schedule, (
                        "rejected attempt consumed account budget"
                    )
                else:
                    assert (
                        response.status_code == 400
                        and response.json()["code"] == "CURRENT_PASSWORD_INVALID"
                    )
                    # Legitimate database-time refill may occur while the process
                    # is restarting. It must still debit the persisted bucket.
                    assert observed_now >= schedule - timedelta(minutes=4)
                    assert next_schedule >= schedule + timedelta(minutes=1)
            checks.append(
                "actual API restart preserves persisted debt; both APIs enforce it and debit legitimate database-time refill"
            )
            before = db.execute(
                "SELECT password_hash,revision,auth_version FROM users WHERE id=%s",
                (fixture["user_id"],),
            ).fetchone()
            sessions_before = db.execute(
                "SELECT count(*) FROM sessions WHERE user_id=%s", (fixture["user_id"],)
            ).fetchone()[0]
            try:
                db.execute(
                    "CREATE FUNCTION synthetic_auth_rate_failure() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic auth rate failure'; END $$"
                )
                db.execute(
                    "CREATE TRIGGER synthetic_auth_rate_failure BEFORE INSERT ON auth_rate_limits FOR EACH ROW EXECUTE FUNCTION synthetic_auth_rate_failure()"
                )
                for client in clients:
                    for path, body in (
                        (
                            "/auth/login",
                            {
                                "account": fixture["account"],
                                "password": SYNTHETIC_PASSWORD,
                            },
                        ),
                        (
                            "/auth/password",
                            {
                                "current_password": SYNTHETIC_PASSWORD,
                                "new_password": "Synthetic-replacement-789",
                                "confirm": True,
                            },
                        ),
                    ):
                        response = client.post(path, json=body)
                        assert (
                            response.status_code == 503
                            and response.json()["code"] == "AUTH_RATE_LIMIT_UNAVAILABLE"
                        ), (path, response.status_code)
                        assert response.headers.get("cache-control") == "no-store"
                        assert SYNTHETIC_PASSWORD not in response.text
            finally:
                db.execute(
                    "DROP TRIGGER IF EXISTS synthetic_auth_rate_failure ON auth_rate_limits"
                )
                db.execute("DROP FUNCTION IF EXISTS synthetic_auth_rate_failure()")
            after = db.execute(
                "SELECT password_hash,revision,auth_version FROM users WHERE id=%s",
                (fixture["user_id"],),
            ).fetchone()
            sessions_after = db.execute(
                "SELECT count(*) FROM sessions WHERE user_id=%s", (fixture["user_id"],)
            ).fetchone()[0]
            assert before == after and sessions_before == sessions_after
            for client in clients:
                assert client.get("/auth/me").status_code == 200
            checks.append(
                "limiter database failures fail closed on both APIs without password, version or session effects"
            )
            report = {
                "phase": "M7.29",
                "profile": "disposable-mock-two-api",
                "checks": checks,
                "completed_at": datetime.now(UTC).isoformat(),
            }
            args.output.write_text(
                json.dumps(report, indent=2) + "\n", encoding="utf-8"
            )
            print(json.dumps({"phase": "M7.29", "passed": len(checks)}))
        finally:
            for client in clients:
                client.close()


if __name__ == "__main__":
    main()
