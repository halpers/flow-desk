"""Exercise two published revisions and explicit rollback on the local synthetic stack."""

import argparse
import json
import ssl
import subprocess
import sys
from datetime import UTC, datetime

from compose_runtime import ROOT, run
from eval_common import PublicAPI, settings


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--baseline", required=True)
    parser.add_argument("--candidate", required=True)
    args = parser.parse_args()
    config = settings()
    report = {
        "started_at": datetime.now(UTC).isoformat(),
        "scope": "local published images",
        "remote_deployment": False,
        "paid_model_calls": False,
        "steps": [],
    }
    trust = ssl.create_default_context(cafile=str(ROOT / "runtime/tls/localhost.crt"))
    steps = [
        ("baseline", ["--revision", args.baseline]),
        ("upgrade", ["--revision", args.candidate]),
        ("rollback", ["--rollback"]),
    ]
    for name, arguments in steps:
        subprocess.run(
            [
                sys.executable,
                str(ROOT / "scripts/release.py"),
                *arguments,
                "--tls",
                "--synthetic",
                "--pull-policy",
                "missing",
            ],
            cwd=ROOT,
            check=True,
        )
        agent_environment = json.loads(
            run(["inspect", "--format", "{{json .Config.Env}}", "flowdesk-agent-1"])
        )
        agent_settings = dict(
            value.split("=", 1) for value in agent_environment if "=" in value
        )
        assert agent_settings.get("MODEL_PROVIDER") == "mock", "MOCK_NOT_APPLIED"
        assert not agent_settings.get("MODEL_API_KEY"), "MODEL_KEY_PRESENT"
        assert not agent_settings.get("EMBEDDING_API_KEY"), "EMBEDDING_KEY_PRESENT"
        api = PublicAPI(
            config,
            "demo.operator",
            base="https://localhost:" + config.get("TLS_HOST_PORT", "13443"),
            verify=trust,
        )
        try:
            task = api.wait(api.start("查询订单 FD202611020001"))
            assert task["status"] == "succeeded", task["error_code"]
        finally:
            api.close()
        state = json.loads((ROOT / "runtime/releases/state.json").read_text())[
            "current"
        ]
        expected = args.candidate if name == "upgrade" else args.baseline
        assert state["revision"] == expected
        running = json.loads(
            run(["inspect", "--format", "{{json .Config.Image}}", "flowdesk-api-1"])
        )
        assert running == state["images"]["api"]
        report["steps"].append(
            {
                "step": name,
                "revision": expected,
                "images": state["images"],
                "verified_api_image": running,
                "run_id": task["id"],
                "task_status": task["status"],
            }
        )
    report["completed_at"] = datetime.now(UTC).isoformat()
    (ROOT / "reports/m6/release-local.json").write_text(
        json.dumps(report, indent=2) + "\n", encoding="utf-8"
    )
    print(
        "Published baseline, upgrade, rollback and three TLS tasks passed; database retained."
    )


if __name__ == "__main__":
    main()
