# 2026-10-09 — A8b-1 S3: amendments П29 and П30 after the e2e run on the F2 stand

Stand: Garage and OpenSSH, restic 0.19.1. 11 of 13 `@stand` scenarios passed; two S3
scenarios of `RepoAddS3Test` failed on wrong assumptions of the specification.

## Measured

| Case | restic command | Exit code | stderr |
|---|---|---|---|
| Empty storage | `restic cat config` | 10 | no repository (the init path of Н19 stays) |
| Bucket with a repository, read-only key | `restic cat config` | 0 | takes **no lock**: the key passes |
| Same, read-only key | command that takes the lock | 1 | `unable to create lock in backend: client.PutObject: Forbidden: Operation is not allowed for this key.` |
| Bucket absent, key may not create buckets, during `restic init` | `restic init` | 1 | `Fatal: create repository at s3:http://garage:3900/<bucket>/<path> failed: client.MakeBucket: Forbidden: Access key <id> is not allowed to create buckets` |

## Done

- П29: `restic.Repository.CheckLock` runs `restic snapshots --latest 1 --json` as the service
  user. `repoconnect` runs it on an existing repository after `cat config` opened it with the
  candidate password and before the env file and the password file are committed; it is bounded
  by `--connect-timeout` (`Bound.CheckLock`). A password from a flag or the fragment's file is
  refused before any prompt; a terminal-only password costs `cat config`, the prompt, then the
  lock check. An empty storage never runs it.
- П30: a `client.MakeBucket:` line with Forbidden or Access Denied is BUCKET_NOT_FOUND ("the
  bucket does not exist ... and the key may not create it"); the env file and the password file
  are kept (П12). A line with `unable to create lock` is STORAGE_ACCESS_DENIED.
- The e2e read-only scenario gives the password by `--password-from-file`.
- Not run here (no Docker in this session): the `@stand` scenarios themselves; only
  `:e2e:compileTestKotlin` was checked.
