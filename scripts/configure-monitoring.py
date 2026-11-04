"""Create local monitoring credentials and a localhost-only development TLS certificate."""

import ipaddress
import secrets
from datetime import UTC, datetime, timedelta
from pathlib import Path

from cryptography import x509
from cryptography.hazmat.primitives import hashes, serialization
from cryptography.hazmat.primitives.asymmetric import rsa
from cryptography.x509.oid import NameOID

ROOT = Path(__file__).resolve().parents[1]


def main():
    environment = ROOT / ".env"
    if not environment.exists():
        raise SystemExit("Run scripts/start.ps1 to create local configuration first")
    text = environment.read_text(encoding="utf-8-sig")
    if not any(
        line.startswith("GRAFANA_ADMIN_PASSWORD=") and line.split("=", 1)[1]
        for line in text.splitlines()
    ):
        lines = [
            line for line in text.splitlines() if not line.startswith("GRAFANA_ADMIN_PASSWORD=")
        ]
        lines.append("GRAFANA_ADMIN_PASSWORD=" + secrets.token_urlsafe(32))
        environment.write_text("\n".join(lines) + "\n", encoding="utf-8")
    directory = ROOT / "runtime/tls"
    directory.mkdir(parents=True, exist_ok=True)
    certificate, private = directory / "localhost.crt", directory / "localhost.key"
    if certificate.exists() != private.exists():
        raise SystemExit("Incomplete local TLS pair; inspect runtime/tls before replacing")
    if not certificate.exists():
        key = rsa.generate_private_key(public_exponent=65537, key_size=2048)
        name = x509.Name([x509.NameAttribute(NameOID.COMMON_NAME, "localhost")])
        now = datetime.now(UTC)
        cert = (
            x509.CertificateBuilder()
            .subject_name(name)
            .issuer_name(name)
            .public_key(key.public_key())
            .serial_number(x509.random_serial_number())
            .not_valid_before(now - timedelta(minutes=1))
            .not_valid_after(now + timedelta(days=30))
            .add_extension(
                x509.SubjectAlternativeName(
                    [x509.DNSName("localhost"), x509.IPAddress(ipaddress.ip_address("127.0.0.1"))]
                ),
                critical=False,
            )
            .sign(key, hashes.SHA256())
        )
        private.write_bytes(
            key.private_bytes(
                serialization.Encoding.PEM,
                serialization.PrivateFormat.PKCS8,
                serialization.NoEncryption(),
            )
        )
        certificate.write_bytes(cert.public_bytes(serialization.Encoding.PEM))
    print("Local monitoring credential and TLS pair ready; values were not printed.")


if __name__ == "__main__":
    main()
