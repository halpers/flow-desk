"""Explicit owner-only existing-volume upgrade; no business or application row writes."""

import argparse
import asyncio
import os

import psycopg
from psycopg import sql
from psycopg.rows import dict_row

from flowdesk_agent.storage.permissions import MIGRATOR_ROLE, RUNTIME_ROLE, owned_code_or_types


async def upgrade(connection, password, *, runtime_role=RUNTIME_ROLE, migration_role=MIGRATOR_ROLE):
    if not password or len(password) < 32:
        raise RuntimeError("AGENT_MIGRATION_PASSWORD_REQUIRED")
    async with connection.transaction():
        await connection.execute("SET LOCAL lock_timeout='5s'")
        await connection.execute("SET LOCAL statement_timeout='60s'")
        row = await (
            await connection.execute("SELECT pg_try_advisory_xact_lock(60112501) AS locked")
        ).fetchone()
        if not row["locked"]:
            raise RuntimeError("AGENT_ROLE_UPGRADE_BUSY")
        row = await (
            await connection.execute("SELECT rolsuper FROM pg_roles WHERE rolname=current_user")
        ).fetchone()
        if not row["rolsuper"]:
            raise RuntimeError("AGENT_ROLE_UPGRADE_REQUIRES_OWNER")
        active = await (
            await connection.execute(
                "SELECT count(*) AS count FROM pg_stat_activity WHERE usename=%s "
                "AND datname=current_database() AND pid<>pg_backend_pid()",
                (runtime_role,),
            )
        ).fetchone()
        if active["count"]:
            raise RuntimeError("AGENT_ROLE_UPGRADE_STOP_RUNTIME_FIRST")
        # Reject surprising ownership rather than reassigning anything outside the service.
        unexpected = await (
            await connection.execute(
                "SELECT EXISTS(SELECT 1 FROM pg_namespace n JOIN pg_roles r ON n.nspowner=r.oid "
                "WHERE r.rolname=%s AND n.nspname NOT IN ('agent','knowledge')) "
                "OR EXISTS(SELECT 1 FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace "
                "JOIN pg_roles r ON r.oid=c.relowner WHERE r.rolname=%s "
                "AND n.nspname NOT IN ('agent','knowledge') "
                "AND NOT EXISTS(SELECT 1 FROM pg_class parent JOIN pg_namespace pn "
                "ON pn.oid=parent.relnamespace WHERE pn.nspname IN ('agent','knowledge') "
                "AND parent.reltoastrelid<>0 AND (parent.reltoastrelid=c.oid "
                "OR EXISTS(SELECT 1 FROM pg_index toast_index "
                "WHERE toast_index.indexrelid=c.oid "
                "AND toast_index.indrelid=parent.reltoastrelid)))) AS unsafe",
                (runtime_role, runtime_role),
            )
        ).fetchone()
        if unexpected["unsafe"]:
            raise RuntimeError("AGENT_ROLE_UPGRADE_UNEXPECTED_OWNER")
        exists = await (
            await connection.execute(
                "SELECT rolcanlogin,rolsuper,rolcreatedb,rolcreaterole,"
                "rolreplication,rolbypassrls FROM pg_roles WHERE rolname=%s",
                (migration_role,),
            )
        ).fetchone()
        if not exists:
            await connection.execute(
                sql.SQL(
                    "CREATE ROLE {} LOGIN PASSWORD {} NOSUPERUSER "
                    "NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS"
                ).format(sql.Identifier(migration_role), sql.Literal(password))
            )
        elif not exists["rolcanlogin"] or any(
            exists[name]
            for name in (
                "rolsuper",
                "rolcreatedb",
                "rolcreaterole",
                "rolreplication",
                "rolbypassrls",
            )
        ):
            raise RuntimeError("AGENT_ROLE_UPGRADE_MIGRATOR_UNSAFE")
        memberships = await (
            await connection.execute(
                "SELECT 1 FROM pg_auth_members m JOIN pg_roles r ON r.oid=m.member "
                "WHERE r.rolname=%s LIMIT 1",
                (migration_role,),
            )
        ).fetchone()
        if memberships:
            raise RuntimeError("AGENT_ROLE_UPGRADE_MIGRATOR_UNSAFE")
        for schema in ("agent", "knowledge"):
            await connection.execute(
                sql.SQL("ALTER SCHEMA {} OWNER TO {}").format(
                    sql.Identifier(schema), sql.Identifier(migration_role)
                )
            )
            relations = await (
                await connection.execute(
                    "SELECT c.relname,c.relkind FROM pg_class c JOIN pg_namespace n "
                    "ON n.oid=c.relnamespace JOIN pg_roles r ON r.oid=c.relowner "
                    "WHERE n.nspname=%s AND r.rolname=%s "
                    "AND c.relkind IN ('r','p','S','v','m','f') "
                    "ORDER BY CASE WHEN c.relkind='S' THEN 1 ELSE 0 END,c.relname",
                    (schema, runtime_role),
                )
            ).fetchall()
            for relation in relations:
                kind = {
                    "r": "TABLE",
                    "p": "TABLE",
                    "S": "SEQUENCE",
                    "v": "VIEW",
                    "m": "MATERIALIZED VIEW",
                    "f": "FOREIGN TABLE",
                }[relation["relkind"]]
                await connection.execute(
                    sql.SQL("ALTER {} {} OWNER TO {}").format(
                        sql.SQL(kind),
                        sql.Identifier(schema, relation["relname"]),
                        sql.Identifier(migration_role),
                    )
                )
            await connection.execute(
                sql.SQL("REVOKE ALL ON SCHEMA {} FROM PUBLIC,{}").format(
                    sql.Identifier(schema), sql.Identifier(runtime_role)
                )
            )
        # ALTER TABLE transfers its automatic row/array types too. Any remaining
        # independent type or routine needs review, even in an unrelated schema.
        catalog = await owned_code_or_types(connection, runtime_role)
        if not catalog:
            raise RuntimeError("AGENT_ROLE_UPGRADE_RUNTIME_MISSING")
        if catalog["routines"]:
            raise RuntimeError("AGENT_ROLE_UPGRADE_REVIEW_OWNED_ROUTINES")
        if catalog["types"]:
            raise RuntimeError("AGENT_ROLE_UPGRADE_REVIEW_OWNED_TYPES")
        await connection.execute(
            sql.SQL("REVOKE CREATE,TEMP ON DATABASE {} FROM PUBLIC,{}").format(
                sql.Identifier(connection.info.dbname), sql.Identifier(runtime_role)
            )
        )
        await connection.execute(
            sql.SQL("REVOKE CREATE ON SCHEMA public FROM PUBLIC,{}").format(
                sql.Identifier(runtime_role)
            )
        )
        await connection.execute(
            sql.SQL("REVOKE ALL ON SCHEMA business FROM {}").format(sql.Identifier(runtime_role))
        )
        await connection.execute(
            sql.SQL(
                "ALTER ROLE {} NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS"
            ).format(sql.Identifier(runtime_role))
        )
        memberships = await (
            await connection.execute(
                "SELECT r.rolname FROM pg_auth_members m JOIN pg_roles r ON r.oid=m.roleid "
                "JOIN pg_roles member ON member.oid=m.member WHERE member.rolname=%s",
                (runtime_role,),
            )
        ).fetchall()
        for membership in memberships:
            await connection.execute(
                sql.SQL("REVOKE {} FROM {}").format(
                    sql.Identifier(membership["rolname"]), sql.Identifier(runtime_role)
                )
            )
    print("Agent ownership upgraded; existing passwords and application rows preserved.")


async def run():
    dsn = os.environ.get("AGENT_OWNER_DATABASE_URL")
    if not dsn:
        raise RuntimeError("AGENT_OWNER_DATABASE_NOT_CONFIGURED")
    async with await psycopg.AsyncConnection.connect(
        dsn, autocommit=True, connect_timeout=5, row_factory=dict_row
    ) as connection:
        await upgrade(connection, os.environ.get("AGENT_MIGRATION_PASSWORD", ""))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--confirm-maintenance", action="store_true", required=True)
    parser.parse_args()
    try:
        asyncio.run(run())
    except (RuntimeError, psycopg.Error, OSError) as error:
        message = str(error) if isinstance(error, RuntimeError) else "AGENT_ROLE_UPGRADE_FAILED"
        parser.exit(1, message + "\n")


if __name__ == "__main__":
    main()
