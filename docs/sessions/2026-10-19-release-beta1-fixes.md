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
