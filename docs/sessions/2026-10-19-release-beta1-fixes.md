# 2026-10-19 — fixes of the failed release v0.1.0-beta.1

Not a product feature: no `/ship-feature` pipeline.

## Release run 39114602295

- `install`, Rocky: `docker build -t sard-pkgtest:rockylinux/rockylinux-9` is an invalid tag.
  `scripts/test-console-install.sh` replaces `/` as `test-agent-install.sh` does.
- `install`, Debian and Ubuntu: `dpkg -i` left the deb unconfigured (`openssh-client`, ADR 0047,
  was not on the host), and the upgrade's `apt-get` had no mirror on the internal network.
  `test/packages/host.Dockerfile` installs `openssh-client`, as the rpm host installs `openssh-clients`.
  OQ-157 (the console's `dpkg -i`) is unchanged.
- `e2e`: `ServerRecoveryDrillTest` "Расписание источника A1 даёт один догоняющий запуск" found no catch-up
  run in 4 minutes. The logs of the run were not readable from this session, so the cause is derived from
  the code, not observed: Р40 needs no active A1 run in the database dump, and `RecoveryOldVm.snapshotRun`
  checked it only before the self-backup started; the dump is taken inside the run, and A1 (every minute)
  can start a run before that. Then the catch-up after the restore is written as `skipped_active`
  (docs/specs/server/server-recovery.feature, OQ-227) and no `catch_up` run exists.
  `snapshotRun` now repeats the snapshot (up to 5 times) until no A1 run overlaps the self-backup runs.
  Not run: the drill needs Docker VMs; `:e2e:compileTestKotlin` is green.

## Follow-up: the drill still fails

The fix above did not remove the failure (same assertion, "no the catch-up run of A1 within PT4M"), so the
overlap of A1's run with the dump was at most one cause, or not the cause. The logs were still unreadable from
this session (`blob.core.windows.net` is not allowed by the network policy). The test now appends the journal of
A1's schedule (`GET /api/v1/sources/{id}/schedule/fires`) and A1's runs to its failure message: the outcome of the
catch-up fire (`skipped_active`, `refused` with a reason, or no fire at all) tells which of the three it is.

## Root cause of the drill failure (from `e2e-logs` of release run 39218920673)

Not a product defect: a race in the drill. In the dump A1's next fire (`next_run_at`) was 08:27:00. The old machine
ran that fire and was destroyed at 08:27:05; the new server started at 08:27:50 and its first scheduler pass was at
08:27:59.4, 59 s after `next_run_at`. One fire had passed and the next (08:28:00) was still ahead, which D16
(ADR 0053) treats as a late ordinary run (`trigger=schedule`), not a catch-up (two or more passed fires). The run
`…503a`, created at 08:27:59.418, was exactly that. A slower new machine (CI) crosses the next minute before
its first pass, so the test passed there; the release runner boots it fast and failed every time.

`RecoveryOldVm.awaitTwoMissedFires` keeps the old machine until the dump's `next_run_at` plus one minute
and a margin has passed, so two fires are owed whatever the speed of the new machine. The overlap check
of `snapshotRun` (above) stays: it covers the other precondition of Р40, no active A1 run in the dump.
Not run: the drill needs Docker VMs; `:e2e:compileTestKotlin` is green.
