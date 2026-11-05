"""Check the Git index for runtime files and locally generated secret values."""
import subprocess
from pathlib import Path

root = Path(__file__).resolve().parents[1]


def git(*args):
    return subprocess.check_output(["git", "-c", "safe.directory=" + root.as_posix(), *args], cwd=root)


files = git("diff", "--cached", "--name-only", "-z", "--diff-filter=ACMR").decode().split("\0")
files = [name for name in files if name]
forbidden = {".env", ".venv", "node_modules", ".cache", ".gocache", "work", "runtime", "__pycache__"}
secret_names = {"POSTGRES_PASSWORD", "BUSINESS_PASSWORD", "AGENT_PASSWORD", "JWT_SECRET", "INTERNAL_TOKEN", "DEMO_PASSWORD", "MODEL_API_KEY", "EMBEDDING_API_KEY", "GRAFANA_ADMIN_PASSWORD", "FILE_STORE_ACCESS_KEY", "FILE_STORE_SECRET_KEY"}
settings = {}
env_file = root / ".env"
if env_file.exists():
    settings = dict(line.split("=", 1) for line in env_file.read_text(encoding="utf-8-sig").splitlines() if line and not line.startswith("#") and "=" in line)
secrets = [value.encode() for key, value in settings.items() if key in secret_names and len(value) >= 12]
failures = []
for name in files:
    if forbidden.intersection(Path(name).parts):
        failures.append(name + ": runtime path")
        continue
    content = git("show", ":" + name)
    if any(value in content for value in secrets):
        failures.append(name + ": contains a local secret")
if failures:
    raise SystemExit("\n".join(failures))
print(f"Staged audit passed: {len(files)} files; no runtime paths or local secret values")
