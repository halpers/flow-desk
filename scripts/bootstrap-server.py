"""Create server settings without overwriting credentials or contacting a provider."""

import argparse
import ipaddress
import os
import secrets
from pathlib import Path
from urllib.parse import urlsplit

ROOT = Path(__file__).resolve().parents[1]
SECRET_NAMES = (
    "POSTGRES_PASSWORD",
    "BUSINESS_PASSWORD",
    "AGENT_PASSWORD",
    "JWT_SECRET",
    "INTERNAL_TOKEN",
    "DEMO_PASSWORD",
    "GRAFANA_ADMIN_PASSWORD",
)


def parse_origin(value):
    try:
        parsed = urlsplit(value)
        port = parsed.port if parsed.port is not None else (443 if parsed.scheme == "https" else 80)
        if (
            parsed.scheme not in {"http", "https"}
            or not parsed.hostname
            or parsed.username is not None
            or parsed.password is not None
            or parsed.path not in {"", "/"}
            or parsed.query
            or parsed.fragment
            or any(character.isspace() for character in value)
            or "$" in value
            or port < 1
        ):
            raise ValueError
        hostname = parsed.hostname.encode("idna").decode("ascii")
        # Public binding currently uses IPv4; IPv6 requires a separate listener plan.
        if ":" in hostname:
            raise ValueError
        if not all(character.isalnum() or character in ".-" for character in hostname):
            raise ValueError
        authority = hostname.lower()
        if parsed.port is not None and port != (443 if parsed.scheme == "https" else 80):
            authority += ":" + str(port)
        return parsed.scheme + "://" + authority, parsed.scheme == "https", port
    except (ValueError, UnicodeError):
        raise argparse.ArgumentTypeError(
            "origin must be one http(s)://IPv4-or-domain[:port], without path or credentials"
        ) from None


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--origin", required=True, type=parse_origin)
    parser.add_argument("--env-file", type=Path, default=ROOT / ".env")
    parser.add_argument("--bind-address", default="0.0.0.0")
    args = parser.parse_args()
    try:
        ipaddress.IPv4Address(args.bind_address)
    except ipaddress.AddressValueError:
        parser.error("--bind-address must be an IPv4 address")
    origin, secure, port = args.origin
    values = {name: secrets.token_hex(24) for name in SECRET_NAMES}
    values.update(
        {
            "APP_MODE": "pro" if secure else "dev",
            "COOKIE_SECURE": str(secure).lower(),
            "ALLOWED_ORIGINS": origin,
            "WEB_HOST_PORT": str(port),
            "WEB_BIND_ADDRESS": args.bind_address,
        }
    )
    lines = []
    for line in (ROOT / ".env.example").read_text(encoding="utf-8-sig").splitlines():
        name = line.split("=", 1)[0]
        lines.append(name + "=" + values.pop(name) if name in values else line)
    lines.extend(name + "=" + value for name, value in values.items())
    try:
        descriptor = os.open(args.env_file, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    except FileExistsError:
        parser.error("environment file already exists; preserved, edit it explicitly instead")
    with os.fdopen(descriptor, "w", encoding="utf-8", newline="\n") as output:
        output.write("\n".join(lines) + "\n")
    print("Server settings created; credentials were not printed. Origin: " + origin)
    if not secure:
        print("HTTP uses dev mode and non-Secure cookies; use a controlled network or HTTPS.")


if __name__ == "__main__":
    main()
