<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<!-- Copyright 2026 Artur Abalov -->

# Sard roadmap

[Русский](roadmap.md)

> **This is a plan, not a promise.** It lists what Sard does not do yet. There are no
> dates, and the order may change. What works today is in the [README](../README.md) and
> [docs/plugins/](plugins/).

## Works today

Sources: files and directories ([files](plugins/files.md)) and PostgreSQL
([postgresql](plugins/postgresql.md)). restic storage: local disk, S3, SFTP.

## Next: close the loop

Backup → restore from the console → automatic restore verification → retention and
repository checks, for files and PostgreSQL. No new sources until the loop is closed.

## Sources later

The first users choose; these are candidates, not commitments:

- 1C on PostgreSQL;
- MikroTik and Eltex network device configurations;
- MySQL.

A source arrives in Sard together with a working plugin and its page in
`docs/plugins/` (ADR 0056).
