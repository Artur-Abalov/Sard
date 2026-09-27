// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server

import org.springframework.beans.factory.annotation.Autowired
import org.springframework.boot.test.context.SpringBootTest
import org.springframework.context.annotation.Import
import org.springframework.jdbc.core.JdbcTemplate
import org.springframework.transaction.support.TransactionTemplate
import kotlin.test.Test
import kotlin.test.assertEquals

/** Tables of the current schema that carry tenant_id (partitions are checked through their parent). */
private const val TENANT_TABLES = """
    with cols as (
        select attrelid as rel, attnum, attname from pg_attribute where attnum > 0 and not attisdropped
    ),
    tenant_tables as (
        select c.oid as rel, c.relname, cols.attnum as tenant_col
        from pg_class c
        join pg_namespace n on n.oid = c.relnamespace
        join cols on cols.rel = c.oid and cols.attname = 'tenant_id'
        where n.nspname = current_schema() and c.relkind in ('r', 'p') and not c.relispartition
    )
"""

/** Rule: tenant_id references tenants(id). */
private const val WITHOUT_TENANT_FK =
    TENANT_TABLES + """
    select t.relname from tenant_tables t
    where not exists (
        select 1 from pg_constraint k
        where k.conrelid = t.rel and k.contype = 'f'
          and k.confrelid = 'tenants'::regclass and k.conkey = array[t.tenant_col]
    )
    order by 1
"""

/** Rule: a tenant table with an id has UNIQUE (tenant_id, id), the target of composite foreign keys. */
private const val WITHOUT_TENANT_ID_KEY =
    TENANT_TABLES + """
    select t.relname from tenant_tables t
    where exists (select 1 from cols where cols.rel = t.rel and cols.attname = 'id')
      and not exists (
        select 1 from pg_constraint k
        where k.conrelid = t.rel and k.contype in ('p', 'u')
          and (select array_agg(c.attname::text order by c.attname) from cols c
               where c.rel = t.rel and c.attnum = any(k.conkey)) = array['id', 'tenant_id']
    )
    order by 1
"""

/** Rule: a reference between tenant tables pairs the child's tenant_id with the parent's. */
private const val SINGLE_TENANT_REFERENCES =
    TENANT_TABLES + """
    select k.conname from pg_constraint k
    join tenant_tables child on child.rel = k.conrelid
    join tenant_tables parent on parent.rel = k.confrelid
    where k.contype = 'f'
      and k.confkey[array_position(k.conkey, child.tenant_col)] is distinct from parent.tenant_col
    order by 1
"""

/** ADR 0013, rules 1 and 2: the database itself refuses cross-tenant links, whatever the code does. */
@SpringBootTest(
    webEnvironment = SpringBootTest.WebEnvironment.RANDOM_PORT,
    properties = ["spring.grpc.server.port=0"],
)
@Import(TestcontainersConfiguration::class)
class TenantSchemaRulesTest(
    @Autowired private val jdbc: JdbcTemplate,
    @Autowired private val tx: TransactionTemplate,
) {
    private fun violations() =
        listOf(WITHOUT_TENANT_FK, WITHOUT_TENANT_ID_KEY, SINGLE_TENANT_REFERENCES)
            .map { jdbc.queryForList(it, String::class.java) }

    @Test
    fun `the migrated schema follows the tenant rules`() {
        assertEquals(listOf(emptyList<String>(), emptyList(), emptyList()), violations())
    }

    /** Control: the checks do catch each violation (DDL is transactional in PostgreSQL). */
    @Test
    fun `a table breaking the rules is caught`() {
        val found =
            tx.execute { status ->
                status.setRollbackOnly()
                jdbc.execute("create table bad_parent (id uuid primary key, tenant_id uuid not null)")
                jdbc.execute(
                    """
                    create table bad_child (
                        id uuid primary key,
                        tenant_id uuid not null references tenants (id),
                        parent_id uuid references bad_parent (id),
                        unique (tenant_id, id)
                    )
                    """.trimIndent(),
                )
                violations()
            }
        assertEquals(listOf(listOf("bad_parent"), listOf("bad_parent"), listOf("bad_child_parent_id_fkey")), found)
    }
}
