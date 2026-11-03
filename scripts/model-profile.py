"""Select a local model profile without displaying or changing API credentials."""

import argparse
import re
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]

parser = argparse.ArgumentParser()
parser.add_argument("profile", choices=["mock", "openai-compatible"])
args = parser.parse_args()
path = ROOT / ".env"
text = path.read_text(encoding="utf-8-sig")
if re.search(r"^MODEL_PROVIDER=", text, re.MULTILINE):
    text = re.sub(
        r"^MODEL_PROVIDER=.*$", "MODEL_PROVIDER=" + args.profile, text, flags=re.MULTILINE
    )
else:
    text = text.rstrip() + "\nMODEL_PROVIDER=" + args.profile + "\n"
path.write_text(text, encoding="utf-8")
print("Local profile selected: " + args.profile + "; recreate Agent to apply")
