"""Validate server bootstrap and effective ingress contracts without starting services."""

import json
import subprocess
import sys
from uuid import uuid4

from compose_runtime import ROOT, run


def bootstrap(path, origin):
    return subprocess.run(
        [
            sys.executable,
            str(ROOT / "scripts/bootstrap-server.py"),
            "--origin",
            origin,
            "--env-file",
            str(path),
        ],
        capture_output=True,
        check=False,
    )


def main():
    checked = []
    for secure in (False, True):
        path = ROOT / (".env.server-check-" + uuid4().hex)
        port = 23443 if secure else 23000
        origin = ("https" if secure else "http") + "://203.0.113.10:" + str(port)
        try:
            assert bootstrap(path, origin).returncode == 0
            original = path.read_bytes()
            assert bootstrap(path, origin).returncode != 0
            assert path.read_bytes() == original, (
                "existing credentials were overwritten"
            )
            settings = dict(
                line.split("=", 1)
                for line in original.decode().splitlines()
                if "=" in line and not line.startswith("#")
            )
            secret_names = (
                "POSTGRES_PASSWORD",
                "BUSINESS_PASSWORD",
                "AGENT_PASSWORD",
                "AGENT_MIGRATION_PASSWORD",
                "JWT_SECRET",
                "INTERNAL_TOKEN",
                "DEMO_PASSWORD",
                "GRAFANA_ADMIN_PASSWORD",
            )
            assert len({settings[name] for name in secret_names}) == len(secret_names)
            assert all(len(settings[name]) >= 32 for name in secret_names)
            if sys.platform != "win32":
                assert path.stat().st_mode & 0o777 == 0o600
            files = [
                "docker-compose.yaml",
                "deploy/compose.monitoring.yaml",
                "deploy/compose.server-tls.yaml"
                if secure
                else "deploy/compose.server.yaml",
            ]
            config = json.loads(
                run(
                    ["config", "--format", "json"],
                    compose_files=files,
                    env_files=[path.name],
                )
            )
            services = config["services"]
            assert services["web"]["ports"] == [
                {
                    "mode": "ingress",
                    "host_ip": "0.0.0.0",
                    "target": 443 if secure else 80,
                    "published": str(port),
                    "protocol": "tcp",
                }
            ], "server entry must replace, rather than append to, the base port mapping"
            for name in (
                "postgres",
                "api",
                "grafana",
                "prometheus",
                "jaeger",
                "otel-collector",
            ):
                assert all(
                    mapping["host_ip"] == "127.0.0.1"
                    for mapping in services[name]["ports"]
                )
            assert not services["agent"].get("ports")
            runtime_environment = services["agent"]["environment"]
            assert "AGENT_MIGRATION_DATABASE_URL" not in runtime_environment
            assert "AGENT_OWNER_DATABASE_URL" not in runtime_environment
            migration_environment = services["agent-migrate"]["environment"]
            assert "AGENT_MIGRATION_DATABASE_URL" in migration_environment
            assert "AGENT_DATABASE_URL" not in migration_environment
            assert (
                services["agent"]["depends_on"]["agent-migrate"]["condition"]
                == "service_completed_successfully"
            )
            assert "agent-role-upgrade" not in services
            environment = services["api"]["environment"]
            assert environment["ALLOWED_ORIGINS"] == origin
            assert environment["COOKIE_SECURE"] == str(secure).lower()
            assert environment["APP_MODE"] == ("pro" if secure else "dev")
            assert settings["MODEL_PROVIDER"] == "mock"
            assert not settings["MODEL_API_KEY"] and not settings["EMBEDDING_API_KEY"]
            checked.append("https" if secure else "http")
        finally:
            path.unlink(missing_ok=True)
    path = ROOT / (".env.server-check-" + uuid4().hex)
    try:
        assert bootstrap(path, "http://203.0.113.10:80").returncode == 0
        assert "ALLOWED_ORIGINS=http://203.0.113.10\n" in path.read_text()
        path.unlink()
        for origin in (
            "http://203.0.113.10:0",
            "http://203.0.113.10:65536",
            "https://user:password@203.0.113.10",
            "https://203.0.113.10/path",
            "https://203.0.113.10?secret=value",
        ):
            assert bootstrap(path, origin).returncode != 0
            assert not path.exists()
    finally:
        path.unlink(missing_ok=True)
    print(
        json.dumps(
            {
                "server_entries": checked,
                "private_service_ports": "loopback only",
                "bootstrap": "random credentials, no overwrite, invalid origins rejected",
            }
        )
    )


if __name__ == "__main__":
    main()
