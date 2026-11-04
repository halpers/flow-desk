"""Invoke local Docker Compose on Windows/WSL or Linux, with redacted command output."""

import os
import shutil
import subprocess
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]


def docker_command():
    if shutil.which("docker"):
        return ["docker"]
    if os.name == "nt" and shutil.which("wsl.exe"):
        drive, tail = str(ROOT).split(":", 1)
        return [
            "wsl.exe",
            "--cd",
            "/mnt/" + drive.lower() + tail.replace("\\", "/"),
            "--exec",
            "docker",
        ]
    raise RuntimeError("Docker or WSL Docker is required")


def run(arguments, *, compose_files=None, env_files=None):
    command = docker_command()
    if compose_files is not None:
        command += ["compose"]
        for path in env_files or [".env"]:
            command += ["--env-file", str(path)]
        for path in compose_files:
            command += ["--file", str(path)]
    result = subprocess.run(command + list(arguments), cwd=ROOT, capture_output=True)
    output = result.stdout.decode("utf-8", errors="replace")
    if result.returncode:
        output += result.stderr.decode(
            "utf-16-le" if b"\0" in result.stderr else "utf-8", errors="replace"
        )
    if (ROOT / ".env").exists():
        for line in (ROOT / ".env").read_text(encoding="utf-8-sig").splitlines():
            if "=" not in line or line.startswith("#"):
                continue
            name, value = line.split("=", 1)
            if len(value) >= 12 and any(
                word in name for word in ("PASSWORD", "KEY", "TOKEN", "SECRET")
            ):
                output = output.replace(value, "[redacted]")
    if result.returncode:
        raise RuntimeError("Docker operation failed: " + output[-2400:])
    return output
