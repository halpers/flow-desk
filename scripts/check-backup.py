"""Consistent local dump/restore drill; never restore over an existing database."""

import json
import shutil
import subprocess
import time
from datetime import UTC, datetime
from uuid import uuid4

import psycopg
from eval_common import ROOT, settings
from psycopg import sql

config = settings()
suffix = uuid4().hex[:12]
database = "fd_restore_" + suffix
dump_file = "/tmp/fd_backup_" + suffix + ".dump"
options = {
    "host": "127.0.0.1",
    "port": config["PG_HOST_PORT"],
    "dbname": "flowdesk",
    "user": "flowdesk_owner",
    "password": config["POSTGRES_PASSWORD"],
    "connect_timeout": 5,
}
shell = shutil.which("pwsh") or shutil.which("powershell")
assert shell
report = {
    "started_at": datetime.now(UTC).isoformat(),
    "method": "pg_dump exported consistent snapshot; pg_restore into fresh disposable database",
    "scope": "local synthetic database; includes business receipts, Agent/checkpoints and knowledge chunks",
    "tables": {},
}
tables = {
    "business": [
        "orders",
        "tickets",
        "ticket_notes",
        "runs",
        "actions",
        "action_receipts",
        "audit_events",
    ],
    "agent": [
        "execution_commands",
        "model_calls",
        "tool_results",
        "checkpoints",
        "knowledge_manifests",
        "knowledge_chunks",
    ],
}


def compose(*arguments):
    with (ROOT / "work/m5-backup.log").open("a", encoding="utf-8") as logfile:
        subprocess.run(
            [
                shell,
                "-NoProfile",
                "-File",
                str(ROOT / "scripts/compose.ps1"),
                *arguments,
            ],
            cwd=ROOT,
            stdout=logfile,
            stderr=logfile,
            check=True,
        )


def manifest(conn):
    result = {}
    for schema, names in tables.items():
        for name in names:
            row = conn.execute(
                sql.SQL(
                    "SELECT count(*), md5(coalesce(string_agg(hash,'' ORDER BY hash),'')) FROM (SELECT md5(to_jsonb(t)::text) hash FROM {}.{} t) rows"
                ).format(sql.Identifier(schema), sql.Identifier(name))
            ).fetchone()
            result[schema + "." + name] = {"rows": row[0], "content_digest": row[1]}
    return result


with psycopg.connect(**options, autocommit=True) as owner:
    owner.execute(sql.SQL("CREATE DATABASE {}").format(sql.Identifier(database)))
    try:
        with psycopg.connect(**options) as source:
            source.execute("SET TRANSACTION ISOLATION LEVEL REPEATABLE READ READ ONLY")
            snapshot = source.execute("SELECT pg_export_snapshot()").fetchone()[0]
            expected = manifest(source)
            started = time.monotonic()
            compose(
                "exec",
                "-T",
                "postgres",
                "pg_dump",
                "-U",
                "flowdesk_owner",
                "-Fc",
                "--snapshot=" + snapshot,
                "-f",
                dump_file,
                "flowdesk",
            )
        report["dump_ms"] = round((time.monotonic() - started) * 1000)
        started = time.monotonic()
        compose(
            "exec",
            "-T",
            "postgres",
            "pg_restore",
            "-U",
            "flowdesk_owner",
            "--exit-on-error",
            "--dbname=" + database,
            dump_file,
        )
        report["restore_ms"] = round((time.monotonic() - started) * 1000)
        with psycopg.connect(**{**options, "dbname": database}) as restored:
            report["tables"] = manifest(restored)
        assert report["tables"] == expected, "RESTORED_CONTENT_MISMATCH"
        report["content_verified"] = True
        backups = ROOT / "storage/backups"
        backups.mkdir(parents=True, exist_ok=True)
        destination = backups / ("synthetic-" + suffix + ".dump")
        compose("cp", "postgres:" + dump_file, destination.relative_to(ROOT).as_posix())
        report["local_backup"] = destination.relative_to(ROOT).as_posix()
        report["backup_bytes"] = destination.stat().st_size
    finally:
        assert database.startswith("fd_restore_") and len(database) == 23
        owner.execute(
            sql.SQL("DROP DATABASE {} WITH (FORCE)").format(sql.Identifier(database))
        )
        report["temporary_database_cleaned"] = True
        report["completed_at"] = datetime.now(UTC).isoformat()
        (ROOT / "reports/m5/backup-restore.json").write_text(
            json.dumps(report, ensure_ascii=False, indent=2) + "\n", encoding="utf-8"
        )
print(
    "Backup/restore verified; disposable restore DB removed; local dump remains ignored"
)
