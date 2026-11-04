"""Deploy fixed application images locally; record a successful target for explicit rollback."""

import argparse
import json
import re
from datetime import UTC, datetime
from uuid import uuid4

from compose_runtime import ROOT, run

STATE = ROOT / "runtime/releases/state.json"


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--revision")
    parser.add_argument("--registry", default="ghcr.io/halpers")
    parser.add_argument("--monitoring", action="store_true")
    parser.add_argument("--tls", action="store_true")
    parser.add_argument(
        "--synthetic",
        action="store_true",
        help="use the explicit mock acceptance override",
    )
    parser.add_argument("--rollback", action="store_true")
    parser.add_argument("--plan", action="store_true")
    parser.add_argument(
        "--pull-policy", choices=["always", "missing", "never"], default="always"
    )
    args = parser.parse_args()
    state = json.loads(STATE.read_text()) if STATE.exists() else {}
    pending = STATE.parent / "pending.json"
    if args.rollback:
        fallback = "current" if pending.exists() else "previous"
        if args.revision or fallback not in state:
            parser.error(
                "rollback requires a previous successful release and no --revision"
            )
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
            "synthetic": args.synthetic,
        }
    images = {
        service: target["registry"]
        + "/flow-desk-"
        + service
        + ":sha-"
        + target["revision"]
        for service in ("api", "agent", "web")
    }
    files = ["docker-compose.yaml"]
    if target["monitoring"]:
        files.append("deploy/compose.monitoring.yaml")
    if target["tls"]:
        files.append("deploy/compose.tls.yaml")
    if target.get("synthetic", False):
        files.append("deploy/compose.mcp-acceptance.yaml")
    if args.plan:
        print(
            json.dumps(
                {"target": target, "images": images, "compose_files": files}, indent=2
            )
        )
        return
    directory = ROOT / "runtime/releases"
    directory.mkdir(parents=True, exist_ok=True)
    lock = directory / "release.lock"
    try:
        handle = lock.open("x")
    except FileExistsError:
        raise SystemExit(
            "A local release lock exists; inspect before retrying"
        ) from None
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

        print("Preparing fixed images: " + target["revision"], flush=True)
        if args.pull_policy != "never":
            compose(
                "pull", "--quiet", "--policy", args.pull_policy, "api", "agent", "web"
            )
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
        print(
            "Release ready; prior successful target retained. No database Down was run."
        )
    finally:
        handle.close()
        lock.unlink(missing_ok=True)


if __name__ == "__main__":
    main()
