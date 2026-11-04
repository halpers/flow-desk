"""Deploy fixed application images locally; record a successful target for explicit rollback."""

import argparse
import json
import re
from datetime import UTC, datetime
from urllib.parse import urlsplit
from uuid import uuid4

from compose_runtime import ROOT, run

STATE = ROOT / "runtime/releases/state.json"


def validate_server(target):
    """Catch mismatched origins/listeners before any release mutation."""
    if not target.get("server", False):
        return
    settings = {}
    for line in (ROOT / ".env").read_text(encoding="utf-8-sig").splitlines():
        if "=" in line and not line.lstrip().startswith("#"):
            name, value = line.split("=", 1)
            settings[name.strip()] = value.strip().strip('"').strip("'")
    origin = settings.get("ALLOWED_ORIGINS", "")
    parsed = urlsplit(origin)
    scheme = "https" if target["tls"] else "http"
    mode, secure = ("pro", "true") if target["tls"] else ("dev", "false")
    if (
        parsed.scheme != scheme
        or not parsed.hostname
        or parsed.username is not None
        or parsed.password is not None
        or parsed.path
        or parsed.query
        or parsed.fragment
        or "," in origin
        or any(character.isspace() for character in origin)
        or settings.get("APP_MODE") != mode
        or settings.get("COOKIE_SECURE") != secure
    ):
        raise ValueError("Server origin, APP_MODE and COOKIE_SECURE must match --server [--tls]")
    port = parsed.port if parsed.port is not None else (443 if target["tls"] else 80)
    if port < 1 or str(port) != settings.get("WEB_HOST_PORT"):
        raise ValueError("WEB_HOST_PORT must match the browser origin port")
    if target["tls"]:
        for name in ("server.crt", "server.key"):
            path = ROOT / "runtime/server-tls" / name
            if not path.is_file() or path.stat().st_size == 0:
                raise ValueError(
                    "Server HTTPS requires runtime/server-tls/server.crt and server.key"
                )


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--revision")
    parser.add_argument("--registry", default="ghcr.io/halpers")
    parser.add_argument("--monitoring", action="store_true")
    parser.add_argument("--tls", action="store_true")
    parser.add_argument(
        "--server", action="store_true", help="publish the configured IP/domain port"
    )
    parser.add_argument(
        "--synthetic",
        action="store_true",
        help="use the explicit mock acceptance override",
    )
    parser.add_argument("--rollback", action="store_true")
    parser.add_argument("--plan", action="store_true")
    parser.add_argument("--pull-policy", choices=["always", "missing", "never"], default="always")
    args = parser.parse_args()
    state = json.loads(STATE.read_text()) if STATE.exists() else {}
    pending = STATE.parent / "pending.json"
    if args.rollback:
        fallback = "current" if pending.exists() else "previous"
        if args.revision or fallback not in state:
            parser.error("rollback requires a previous successful release and no --revision")
        target = state[fallback]
    else:
        if not args.revision or not re.fullmatch(r"[0-9a-f]{40}", args.revision):
            parser.error("--revision must be a full 40-character commit SHA")
        if not re.fullmatch(r"[a-z0-9.-]+(?::[0-9]+)?/[a-z0-9._/-]+", args.registry):
            parser.error("registry must be a registry/namespace prefix")
        target = {
            "revision": args.revision,
            "registry": args.registry,
            "monitoring": args.monitoring,
            "tls": args.tls,
            "server": args.server,
            "synthetic": args.synthetic,
        }
    images = {
        service: target["registry"] + "/flow-desk-" + service + ":sha-" + target["revision"]
        for service in ("api", "agent", "web")
    }
    files = ["docker-compose.yaml"]
    if target["monitoring"]:
        files.append("deploy/compose.monitoring.yaml")
    if target.get("server", False):
        files.append(
            "deploy/compose.server-tls.yaml" if target["tls"] else "deploy/compose.server.yaml"
        )
    elif target["tls"]:
        files.append("deploy/compose.tls.yaml")
    if target.get("synthetic", False):
        files.append("deploy/compose.mcp-acceptance.yaml")
    if args.plan:
        print(json.dumps({"target": target, "images": images, "compose_files": files}, indent=2))
        return
    validate_server(target)
    directory = ROOT / "runtime/releases"
    directory.mkdir(parents=True, exist_ok=True)
    lock = directory / "release.lock"
    try:
        handle = lock.open("x")
    except FileExistsError:
        raise SystemExit("A local release lock exists; inspect before retrying") from None
    try:
        handle.write(datetime.now(UTC).isoformat())
        handle.close()
        plan = directory / (uuid4().hex + ".env")
        plan.write_text(
            "".join(
                "FLOWDESK_" + name.upper() + "_IMAGE=" + image + "\n"
                for name, image in images.items()
            ),
            encoding="utf-8",
        )
        environment = [".env", plan.relative_to(ROOT).as_posix()]
        pending.write_text(json.dumps(target, indent=2) + "\n", encoding="utf-8")

        def compose(*arguments):
            return run(arguments, compose_files=files, env_files=environment)

        compose("config", "--quiet")
        print("Preparing fixed images: " + target["revision"], flush=True)
        if args.pull_policy != "never":
            compose("pull", "--quiet", "--policy", args.pull_policy, "api", "agent", "web")
        # Infrastructure first; application migration and readiness are explicit gates.
        infrastructure = ["postgres"]
        if target["monitoring"]:
            infrastructure += ["jaeger", "otel-collector", "prometheus", "grafana"]
        compose(
            "up",
            "--detach",
            "--no-build",
            "--wait",
            "--wait-timeout",
            "180",
            *infrastructure,
        )
        compose("run", "--rm", "--no-deps", "migrate")
        for service in ("agent", "api", "web"):
            compose(
                "up",
                "--detach",
                "--no-build",
                "--no-deps",
                "--wait",
                "--wait-timeout",
                "180",
                service,
            )
        record = {
            **target,
            "completed_at": datetime.now(UTC).isoformat(),
            "images": images,
        }
        updated = {"current": record}
        if state.get("current"):
            updated["previous"] = state["current"]
        temporary = directory / "state.next.json"
        temporary.write_text(json.dumps(updated, indent=2) + "\n", encoding="utf-8")
        temporary.replace(STATE)
        pending.unlink(missing_ok=True)
        print("Release ready; prior successful target retained. No database Down was run.")
    finally:
        handle.close()
        lock.unlink(missing_ok=True)


if __name__ == "__main__":
    main()
