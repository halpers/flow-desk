import json
from pathlib import Path

import pytest


def test_failed_local_release_preserves_previous_success_and_releases_lock(tmp_path, monkeypatch):
    monkeypatch.syspath_prepend(str(Path(__file__).resolve().parents[2] / "scripts"))
    import release

    monkeypatch.setattr(release, "ROOT", tmp_path)
    directory = tmp_path / "runtime/releases"
    directory.mkdir(parents=True)
    state = directory / "state.json"
    before = {
        "current": {
            "revision": "a" * 40,
            "registry": "ghcr.io/synthetic",
            "monitoring": False,
            "tls": False,
        }
    }
    state.write_text(json.dumps(before))
    monkeypatch.setattr(release, "STATE", state)
    calls = []

    def run(arguments, **kwargs):
        calls.append(arguments)
        if arguments[0] == "run":
            raise RuntimeError("synthetic migration failure")
        return ""

    monkeypatch.setattr(release, "run", run)
    monkeypatch.setattr(
        "sys.argv", ["release.py", "--revision", "b" * 40, "--pull-policy", "never"]
    )
    with pytest.raises(RuntimeError, match="migration failure"):
        release.main()
    assert json.loads(state.read_text()) == before
    assert not (directory / "release.lock").exists()
    assert not any("down" in call or "web" in call for call in calls)
    assert (directory / "pending.json").exists()
    monkeypatch.setattr(release, "run", lambda *args, **kwargs: "")
    monkeypatch.setattr("sys.argv", ["release.py", "--rollback", "--pull-policy", "never"])
    release.main()
    assert json.loads(state.read_text())["current"]["revision"] == "a" * 40
    assert not (directory / "pending.json").exists()
