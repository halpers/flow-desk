"""Append one independent migration credential without replacing an existing environment."""

import argparse
import os
import re
import secrets
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
NAME = "AGENT_MIGRATION_PASSWORD"


def configure(path):
    path = path.resolve(strict=True)
    lock = path.with_name(path.name + ".migration.lock")
    descriptor = os.open(lock, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    try:
        original = path.read_bytes()
        values = [
            line.split("=", 1)[1].strip()
            for line in original.decode("utf-8-sig").splitlines()
            if line.startswith(NAME + "=")
        ]
        if values:
            if len(values) != 1 or not re.fullmatch(r"[0-9a-f]{48,128}", values[0]):
                raise ValueError(
                    "existing migration credential preserved; verify one random hex value"
                )
            print("Independent migration credential already configured; preserved.")
            return
        with path.open("ab") as output:
            if original and not original.endswith(b"\n"):
                output.write(b"\n")
            output.write((NAME + "=" + secrets.token_hex(24) + "\n").encode("ascii"))
        print(
            "Independent migration credential appended; "
            "existing settings preserved, value not printed."
        )
    finally:
        os.close(descriptor)
        lock.unlink()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--env-file", type=Path, default=ROOT / ".env")
    args = parser.parse_args()
    try:
        configure(args.env_file)
    except (OSError, ValueError) as error:
        parser.exit(
            1, "Migration credential configuration failed: " + str(error) + "\n"
        )


if __name__ == "__main__":
    main()
