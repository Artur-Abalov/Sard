// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.retention

/*
 * The deletes of the cleaning, one batch each. Every batch picks its rows with FOR UPDATE SKIP LOCKED and at most
 * :limit of them, then deletes them in the same statement: foreign keys without ON DELETE are checked at the end of a
 * statement, so a parent and the children that refer to it go together, and a row another server or transaction holds
 * is left to its holder. Every statement names :tenant; the rules "never deleted" are counted inside the tenant.
 */

/** A delivery in a final status older than the term of deliveries; a pending one is never deleted. */
internal const val DELIVERIES =
    """
    delete from notification_deliveries where tenant_id = :tenant and id in (
        select d.id from notification_deliveries d
        where d.tenant_id = :tenant and d.status <> 'pending' and d.created_at < :cutoff
        order by d.created_at, d.id limit :limit for update of d skip locked)
    """

private const val NO_DELIVERY_OF_FIRE =
    """not exists (select 1 from notification_deliveries d where d.tenant_id = f.tenant_id and d.fire_id = f.id)"""

/** The downtime rows that stand for a catch-up: they go before the catch-up fire they refer to. */
internal const val DOWNTIME_FIRES =
    """
    delete from schedule_fires where tenant_id = :tenant and id in (
        select f.id from schedule_fires f
        where f.tenant_id = :tenant and f.recorded_at < :cutoff and f.catch_up_fire_id is not null
          and $NO_DELIVERY_OF_FIRE
        order by f.recorded_at, f.id limit :limit for update of f skip locked)
    """

/** The other fires: those no delivery and no downtime row refers to any more. */
internal const val OTHER_FIRES =
    """
    delete from schedule_fires where tenant_id = :tenant and id in (
        select f.id from schedule_fires f
        where f.tenant_id = :tenant and f.recorded_at < :cutoff and f.catch_up_fire_id is null
          and $NO_DELIVERY_OF_FIRE
          and not exists (select 1 from schedule_fires c where c.tenant_id = f.tenant_id and c.catch_up_fire_id = f.id)
        order by f.recorded_at, f.id limit :limit for update of f skip locked)
    """

/**
 * The records of verifications are never deleted (DB2, Р4), and sard_app may not change one: the database clears what
 * the verifier found (stages and mismatches) in the old ones, except the last passed and the last not passed one of
 * each
 * source, and never in a record younger than its own floor (docs/adr/0069-database-roles.md).
 */
internal const val CLEAR_VERIFICATION_EVIDENCE = "select clear_verification_evidence(:tenant, :cutoff, :limit)"

private const val SAME_KIND =
    """(case when n.trigger = 'verification' then 'verify' else n.kind end) =
       (case when r.trigger = 'verification' then 'verify' else r.kind end)"""

private const val STEP_OF_RUN = "s.tenant_id = r.tenant_id and s.run_id = r.id"

/**
 * Finished runs older than the term, with their steps, unless something keeps them: a snapshot of a step, a log line,
 * a verification record of a step, a job, a schedule fire, a delivery or an overdue episode that refers to the run,
 * or the run is the last success of its kind (backup, restore, verify) for its source.
 */
internal const val RUNS =
    """
    with picked as (
        select r.id from runs r
        where r.tenant_id = :tenant and r.finished_at < :cutoff
          and not exists (select 1 from run_steps s
                          join snapshots sn on sn.tenant_id = s.tenant_id and sn.step_id = s.id
                          where $STEP_OF_RUN)
          and not exists (select 1 from run_steps s join step_logs l on l.tenant_id = s.tenant_id and l.step_id = s.id
                          where $STEP_OF_RUN)
          and not exists (select 1 from run_steps s
                          join restore_verifications v on v.tenant_id = s.tenant_id and v.step_id = s.id
                          where $STEP_OF_RUN)
          and not exists (select 1 from verification_jobs j where j.tenant_id = r.tenant_id and j.run_id = r.id)
          and not exists (select 1 from schedule_fires f where f.tenant_id = r.tenant_id and f.run_id = r.id)
          and not exists (select 1 from notification_deliveries d where d.tenant_id = r.tenant_id and d.run_id = r.id)
          and not exists (select 1 from overdue_episodes e
                          where e.tenant_id = r.tenant_id and e.closed_by_run_id = r.id)
          and (r.status <> 'succeeded'
               or exists (select 1 from runs n
                          where n.tenant_id = r.tenant_id and n.source_id = r.source_id and n.status = 'succeeded'
                            and $SAME_KIND and (n.finished_at, n.id) > (r.finished_at, r.id)))
        order by r.finished_at, r.id limit :limit for update of r skip locked),
    gone_steps as (
        delete from run_steps s using picked p where s.tenant_id = :tenant and s.run_id = p.id returning s.id),
    gone_runs as (
        delete from runs r using picked p where r.tenant_id = :tenant and r.id = p.id returning r.id)
    select (select count(*) from gone_runs), (select count(*) from gone_steps)
    """
