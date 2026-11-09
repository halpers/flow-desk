import importlib.util
import tempfile
import unittest
from pathlib import Path

SOURCE = Path(__file__).resolve().parents[1] / "configure-agent-migration.py"
SPEC = importlib.util.spec_from_file_location("agent_migration_config", SOURCE)
MODULE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(MODULE)


class CredentialPreservationTests(unittest.TestCase):
    def test_existing_credentials_and_bytes_are_preserved_and_replay_is_unchanged(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / ".env"
            original = (
                b"# synthetic\r\nAGENT_PASSWORD=synthetic-old\r\nMODEL_PROVIDER=mock"
            )
            path.write_bytes(original)
            MODULE.configure(path)
            updated = path.read_bytes()
            self.assertTrue(updated.startswith(original + b"\n"))
            self.assertEqual(updated.count(b"AGENT_MIGRATION_PASSWORD="), 1)
            self.assertNotIn(b"synthetic-old", updated[len(original) :])
            MODULE.configure(path)
            self.assertEqual(path.read_bytes(), updated)

    def test_invalid_existing_value_is_rejected_without_overwrite(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / ".env"
            original = b"AGENT_MIGRATION_PASSWORD=\nAGENT_PASSWORD=synthetic-old\n"
            path.write_bytes(original)
            with self.assertRaises(ValueError):
                MODULE.configure(path)
            self.assertEqual(path.read_bytes(), original)
            self.assertFalse(path.with_name(".env.migration.lock").exists())


if __name__ == "__main__":
    unittest.main()
