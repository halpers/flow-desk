"""A failed independent migration must never promote a release or start apps."""

import importlib.util
import json
import sys
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

SCRIPTS = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(SCRIPTS))
spec = importlib.util.spec_from_file_location(
    "migration_release", SCRIPTS / "release.py"
)
release = importlib.util.module_from_spec(spec)
spec.loader.exec_module(release)


class MigrationReleaseGate(unittest.TestCase):
    def test_failed_agent_migration_preserves_successful_release_and_stops_apps(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            state = root / "runtime/releases/state.json"
            state.parent.mkdir(parents=True)
            original = json.dumps({"current": {"revision": "a" * 40}}).encode()
            state.write_bytes(original)
            calls = []

            def compose(arguments, **options):
                calls.append(tuple(arguments))
                if tuple(arguments) == ("run", "--rm", "--no-deps", "agent-migrate"):
                    raise RuntimeError("synthetic migration failure")

            with (
                patch.object(release, "ROOT", root),
                patch.object(release, "STATE", state),
                patch.object(release, "run", side_effect=compose),
                patch.object(
                    sys,
                    "argv",
                    ["release.py", "--revision", "b" * 40, "--pull-policy", "never"],
                ),
                self.assertRaisesRegex(RuntimeError, "synthetic migration failure"),
            ):
                release.main()
            self.assertEqual(state.read_bytes(), original)
            self.assertTrue((state.parent / "pending.json").is_file())
            self.assertFalse((state.parent / "release.lock").exists())
            self.assertIn(("run", "--rm", "--no-deps", "migrate"), calls)
            self.assertEqual(calls[-1], ("run", "--rm", "--no-deps", "agent-migrate"))
            self.assertFalse(
                any(
                    call[0] == "up" and call[-1] in ("agent", "api", "web")
                    for call in calls
                )
            )


if __name__ == "__main__":
    unittest.main()
