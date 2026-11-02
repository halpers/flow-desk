"""Export schema without opening database connections."""

import json
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "src"))
from flowdesk_agent.api.main import app  # noqa: E402

target = Path(__file__).resolve().parents[2] / "shared/contracts/agent.openapi.json"
target.write_text(
    json.dumps(app.openapi(), ensure_ascii=False, indent=2, sort_keys=True) + "\n", encoding="utf-8"
)
