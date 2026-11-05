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
parser.add_argument(
    "--enable-inventory",
    action="store_true",
    help="Enable read-only object listing in an explicit mock sandbox",
)
args = parser.parse_args()
lines = args.env_file.read_text(encoding="utf-8").splitlines()
settings = dict(
    line.split("=", 1) for line in lines if "=" in line and not line.startswith("#")
)
if args.enable_cleanup or args.enable_inventory:
    if settings.get("MODEL_PROVIDER") != "mock":
        parser.error(
            "Optional storage checks require an isolated MODEL_PROVIDER=mock sandbox"
        )
    for enabled, name in (
        (args.enable_cleanup, "FILE_STORE_CLEANUP_ENABLED"),
        (args.enable_inventory, "FILE_STORE_INVENTORY_ENABLED"),
    ):
        if enabled:
            lines = [line for line in lines if not line.startswith(name + "=")]
            lines.append(name + "=true")
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
