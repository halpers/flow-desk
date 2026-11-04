"""Portable implementation of goctl-model.sh; never migrates a live business schema."""
import os
import re
from pathlib import Path
import subprocess
from urllib.parse import parse_qsl, urlencode, urlsplit, urlunsplit
from uuid import uuid4

directory = Path(__file__).resolve().parent
database_url = os.environ["MODEL_DB_URL"]
schema = "fd_model_" + uuid4().hex


def run(*args):
    result = subprocess.run(args, cwd=directory)
    if result.returncode:
        # CalledProcessError includes the DSN (and password) in its string form.
        raise SystemExit(f"{args[0]} failed with exit code {result.returncode}")


try:
    run("psql", database_url, "-v", "ON_ERROR_STOP=1", "-c", f'CREATE SCHEMA "{schema}"')
    parts = urlsplit(database_url)
    query = dict(parse_qsl(parts.query))
    query["search_path"] = schema
    isolated_url = urlunsplit(parts._replace(query=urlencode(query)))
    run("goose", "-dir", "migrations", "postgres", isolated_url, "up")
    tables = ",".join(line.strip() for line in (directory / "model-tables.conf").read_text().splitlines() if line.strip())
    run("goctl", "model", "pg", "datasource", "--url", database_url, "--schema", schema, "--table", tables, "--dir", "model")
    for path in (directory / "model").glob("*_gen.go"):
        content = path.read_text(encoding="utf-8")
        # goctl 1.10.2 maps BYTEA to string; lib/pq needs []byte for arbitrary PDF bytes.
        if path.name == "documentversionsmodel_gen.go":
            content, count = re.subn(r'(SourceBytes\s+)string(\s+`db:"source_bytes"`)',
                                    r'\g<1>[]byte\2', content)
            if count != 1:
                raise SystemExit("Review goctl BYTEA mapping before regenerating models")
        path.write_text(content.replace(f'"{schema}".', '"business".'), encoding="utf-8", newline="\n")
finally:
    run("psql", database_url, "-v", "ON_ERROR_STOP=1", "-c", f'DROP SCHEMA IF EXISTS "{schema}" CASCADE')
