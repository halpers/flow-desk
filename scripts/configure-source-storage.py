"""Generate local sandbox credentials without reading or printing existing secrets."""

import argparse
import secrets
from pathlib import Path

parser = argparse.ArgumentParser()
parser.add_argument("--env-file", type=Path, default=Path(".env"))
parser.add_argument(
    "--enable-cleanup",
    action="store_true",
    help="Enable copy cleanup only in an explicit mock sandbox",
)
args = parser.parse_args()
lines = args.env_file.read_text(encoding="utf-8").splitlines()
settings = dict(
    line.split("=", 1) for line in lines if "=" in line and not line.startswith("#")
)
if args.enable_cleanup:
    if settings.get("MODEL_PROVIDER") != "mock":
        parser.error(
            "--enable-cleanup requires an isolated MODEL_PROVIDER=mock sandbox"
        )
    lines = [
        line for line in lines if not line.startswith("FILE_STORE_CLEANUP_ENABLED=")
    ]
    lines.append("FILE_STORE_CLEANUP_ENABLED=true")
for key, value in {
    "FILE_STORE_ACCESS_KEY": secrets.token_hex(16),
    "FILE_STORE_SECRET_KEY": secrets.token_hex(32),
    "FILE_STORE_BUCKET": "flowdesk-sources",
}.items():
    if settings.get(key):
        continue
    lines = [line for line in lines if not line.startswith(key + "=")]
    lines.append(key + "=" + value)
args.env_file.write_text("\n".join(lines) + "\n", encoding="utf-8")
print("Local S3 sandbox credentials configured; values are not printed.")
