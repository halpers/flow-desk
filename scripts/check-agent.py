"""Run Agent checks with a disposable-schema administrator supplied only to tests."""
import os
from pathlib import Path

import pytest

root = Path(__file__).resolve().parents[1]
settings = dict(line.split("=", 1) for line in (root / ".env").read_text(encoding="utf-8-sig").splitlines() if line and not line.startswith("#"))
os.environ["AGENT_TEST_DATABASE_URL"] = (
    "postgresql://flowdesk_owner:" + settings["POSTGRES_PASSWORD"]
    + "@localhost:" + settings["PG_HOST_PORT"] + "/flowdesk"
)
os.chdir(root / "flow-desk-agent")
raise SystemExit(pytest.main(["-q", "--tb=short"]))
