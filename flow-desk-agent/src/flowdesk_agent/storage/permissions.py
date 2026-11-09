"""Trusted deployment-role grants and read-only runtime boundary verification."""

from langgraph.checkpoint.postgres.aio import AsyncPostgresSaver
from psycopg import sql

from flowdesk_agent.storage.migrations import MIGRATIONS

RUNTIME_ROLE = "fd_agent"
MIGRATOR_ROLE = "fd_agent_migrator"
READ_ONLY_TABLES = ("schema_versions", "checkpoint_migrations")


async def owned_code_or_types(connection, role):
    """Find independent owned DDL objects, including objects outside service schemas."""
    return await (
        await connection.execute(
            "SELECT EXISTS(SELECT 1 FROM pg_proc p WHERE p.proowner=r.oid) AS routines, "
            "EXISTS(SELECT 1 FROM pg_type t WHERE t.typowner=r.oid AND NOT EXISTS("
            "SELECT 1 FROM pg_depend d WHERE d.classid='pg_type'::regclass "
            "AND d.objid=t.oid AND d.deptype='i' AND ("
            "(d.refclassid='pg_class'::regclass AND EXISTS(SELECT 1 FROM pg_class parent "
            "WHERE parent.oid=d.refobjid AND parent.reltype=t.oid "
            "AND parent.relowner<>r.oid)) OR "
            "(d.refclassid='pg_type'::regclass AND EXISTS(SELECT 1 FROM pg_type parent "
            "WHERE parent.oid=d.refobjid AND parent.typarray=t.oid "
            "AND parent.typowner<>r.oid))))) AS types "
            "FROM pg_roles r WHERE r.rolname=%s",
            (role,),
        )
    ).fetchone()


async def grant_runtime(connection, schema="agent", role=RUNTIME_ROLE):
    """Only an explicit schema owner/migration connection may call this."""
    target, actor = sql.Identifier(schema), sql.Identifier(role)
    await connection.execute(sql.SQL("GRANT USAGE ON SCHEMA {} TO {}").format(target, actor))
    await connection.execute(sql.SQL("GRANT USAGE ON SCHEMA knowledge TO {}").format(actor))
    await connection.execute(
        sql.SQL("GRANT SELECT,INSERT,UPDATE,DELETE ON ALL TABLES IN SCHEMA {} TO {}").format(
            target, actor
        )
    )
    await connection.execute(
        sql.SQL("GRANT USAGE,SELECT ON ALL SEQUENCES IN SCHEMA {} TO {}").format(target, actor)
    )
    await connection.execute(
        sql.SQL(
            "ALTER DEFAULT PRIVILEGES IN SCHEMA {} "
            "GRANT SELECT,INSERT,UPDATE,DELETE ON TABLES TO {}"
        ).format(target, actor)
    )
    await connection.execute(
        sql.SQL(
            "ALTER DEFAULT PRIVILEGES IN SCHEMA {} GRANT USAGE,SELECT ON SEQUENCES TO {}"
        ).format(target, actor)
    )
    for table in READ_ONLY_TABLES:
        await connection.execute(
            sql.SQL("REVOKE ALL ON TABLE {} FROM {}").format(sql.Identifier(schema, table), actor)
        )
        await connection.execute(
            sql.SQL("GRANT SELECT ON TABLE {} TO {}").format(sql.Identifier(schema, table), actor)
        )


async def verify_runtime(pool, *, role=RUNTIME_ROLE, schema="agent"):
    """Read-only readiness gate, including exact application/checkpointer migrations."""
    async with pool.connection() as connection, connection.transaction():
        await connection.execute("SET TRANSACTION READ ONLY")
        await connection.execute("SET LOCAL statement_timeout='3s'")
        current = await (
            await connection.execute("SELECT current_user AS name,current_schema() AS schema")
        ).fetchone()
        if current["name"] != role or current["schema"] != schema:
            raise RuntimeError("AGENT_RUNTIME_IDENTITY_INVALID")
        versions = await (
            await connection.execute("SELECT version FROM schema_versions ORDER BY version")
        ).fetchall()
        checkpoints = await (
            await connection.execute("SELECT v FROM checkpoint_migrations ORDER BY v")
        ).fetchall()
        if [row["version"] for row in versions] != list(range(1, len(MIGRATIONS) + 1)) or [
            row["v"] for row in checkpoints
        ] != list(range(len(AsyncPostgresSaver.MIGRATIONS))):
            raise RuntimeError("AGENT_SCHEMA_VERSION_MISMATCH")
        boundary = await (
            await connection.execute(
                "SELECT r.rolsuper OR r.rolcreatedb OR r.rolcreaterole OR r.rolreplication "
                "OR r.rolbypassrls AS elevated, "
                "has_database_privilege(current_user,current_database(),'CREATE') "
                "OR has_database_privilege(current_user,current_database(),'TEMP') "
                "AS database_ddl, "
                "EXISTS(SELECT 1 FROM pg_roles p WHERE p.oid<>r.oid "
                "AND pg_has_role(r.oid,p.oid,'MEMBER')) AS membership, "
                "EXISTS(SELECT 1 FROM pg_namespace n WHERE n.nspname !~ '^pg_' "
                "AND n.nspname<>'information_schema' AND "
                "(n.nspowner=r.oid OR has_schema_privilege(current_user,n.oid,'CREATE'))) "
                "AS schema_ddl, "
                "EXISTS(SELECT 1 FROM pg_class c WHERE c.relowner=r.oid) AS owns_relation, "
                "COALESCE(has_schema_privilege(current_user,"
                "to_regnamespace('business'),'USAGE'),false) "
                "AS business_access FROM pg_roles r WHERE r.rolname=current_user"
            )
        ).fetchone()
        if not boundary or any(boundary.values()):
            raise RuntimeError("AGENT_RUNTIME_PRIVILEGES_UNSAFE")
        catalog = await owned_code_or_types(connection, role)
        if not catalog or any(catalog.values()):
            raise RuntimeError("AGENT_RUNTIME_PRIVILEGES_UNSAFE")
        tables = await (
            await connection.execute(
                "SELECT c.relname, "
                "has_table_privilege(current_user,c.oid,'SELECT') AS readable, "
                "has_table_privilege(current_user,c.oid,'INSERT') AS insertable, "
                "has_table_privilege(current_user,c.oid,'UPDATE') AS updatable, "
                "has_table_privilege(current_user,c.oid,'DELETE') AS deletable, "
                "has_table_privilege(current_user,c.oid,'TRUNCATE') "
                "OR has_table_privilege(current_user,c.oid,'TRIGGER') "
                "OR has_table_privilege(current_user,c.oid,'REFERENCES') AS extra "
                "FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace "
                "WHERE n.nspname=%s AND c.relkind IN ('r','p')",
                (schema,),
            )
        ).fetchall()
        for table in tables:
            mutable = table["relname"] not in READ_ONLY_TABLES
            if (
                table["extra"]
                or not table["readable"]
                or any(table[name] != mutable for name in ("insertable", "updatable", "deletable"))
            ):
                raise RuntimeError("AGENT_RUNTIME_TABLE_GRANTS_INVALID")
