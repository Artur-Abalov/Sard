<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<!-- Copyright 2026 Artur Abalov -->

# 12. Server recovery

The server burned down with its VM. You have access to the self-backup storage,
the repository password and the recovery card. This page brings Sard up on a new
machine: agents reconnect without re-enrolling, the history is there, the
self-backup carries on. Every command on this page is run by the drill in CI
(`ServerRecoveryDrillTest` in [`test/e2e`](../../../test/e2e/README.md)) on a clean
machine with no internet access: the blocks the source of the page marks with a
`<!-- drill: … -->` line are executed exactly as written. This page is the
English version of [the Russian one](../../operator/12-server-recovery.md); the
drill blocks of both are identical.

How to read it:

- the steps go in order; each of steps 1-6 has a block of commands, a "Check"
  line (what you should get) and an "If it failed" line (the Russian page calls
  them «Проверка» and «Если не вышло»);
- a block is meant to run in a `bash` process of its own (paste it into `bash`
  or save it to a file and run `bash file`); the state between blocks lives in
  `~/sard-recovery` (step 0);
- values in angle brackets come from the recovery card or are yours (the table
  below); there are no passwords or keys in the blocks: secrets are typed without
  echo (`read -rs`) and then travel only in files with mode `600`;
- on a VM without internet access the images come from the offline archive
  ([section 11](../../operator/11-offline.md)) and every `docker compose up` in the
  blocks runs with `--pull never`.

| Placeholder | Where it comes from |
|---|---|
| `<repositoryName>` | card: repository name |
| `<location>` | card: storage address as the wizard built it |
| `<repositoryId>` | card: repository id (64 hex) |
| `<caFingerprint>` | card: CA fingerprint (64 hex) |
| `<sardVersion>` | card: the server version the card was made on |
| `<адрес-restic>` | `<location>` in the form restic understands (table in step 0) |
| `<образ-агента>` | image `sard-agent:<sardVersion>-pg18` (step 0) |
| `<имя-сервера>` | the DNS name agents use to find the server |

The last three names keep their Russian spelling in the blocks: the blocks of the
two versions of the page are identical, and the drill substitutes the values by
name. `<адрес-restic>` is the restic address, `<образ-агента>` the agent image,
`<имя-сервера>` the server name.

## What you need

Three things. Without any of them recovery is impossible, and the Sard server does not
keep them for you (ADR 0008):

1. **The repository password.** Only the
   neighbour `sard-self` knows it; you saved it by running
   `repo password <name> --reveal` ([section 6](../../operator/06-data-and-backup.md)).
   Without it the snapshots cannot be read.
2. **Access to the storage:** for S3, the key id, the secret key and the region;
   for SFTP, your SSH key and the fingerprint of the server's key. Local storage
   on the old VM died with it: nothing can be recovered from it.
3. **The recovery card.** Console, "Self-backup",
   "Recovery card" (print or `.txt`), or `GET /api/v1/self-backup/recovery-card`
   before the disaster. It holds the repository name and type, the storage
   address, `repositoryId`, `caFingerprint`, `sardVersion`. There is no password
   or key in the card.

**The administrator password** is not a fourth thing
and not a new mechanism. Its hash is in the database, and the database comes
back from the snapshot with everything else: after recovery you sign in with the
same password the administrator had when the database snapshot
was taken. Keep the administrator password together with the card; you need it in step 5. If it is forgotten there is the fallback
`admin-reset` ([section 10](../../operator/10-security.md)); it does not reset a completed
onboarding and touches no data.

The new VM: Docker with the compose plugin ([section 1](../../operator/01-requirements.md)),
a user in the `docker` group, network access to the storage, free ports 8080
(`127.0.0.1` only) and 9090. The server name for agents still points to the old
VM or nowhere: it is too early to switch it.

## How long it takes

The drill measures
the time from the start of the first block on the new VM to the server's
health, to all agents being online after the name is switched, and to the
successful self-backup runs. The drill database is small, so the numbers show an
order of magnitude, not a guarantee: on your database the minutes grow with the
size of the dump and the speed of the storage. CI does not assert the duration (a
shared runner); a person carries the row into the table from
`test/e2e/build/recovery-drill/result.md`.

The row was taken by the drill on 2026-10-14 on a developer machine (not on CI)
with the local build `0.0.0-e2e`; "to the server" is from the start of loading the
images to the health, "to the agents" until the two old agents are online after the
name is switched, "to the self-backup" until both self-backup runs of step 5
succeeded.

| Date | Version | Dump size | Database size | Minutes to the server | Minutes to the agents | Minutes to the self-backup |
|---|---|---|---|---|---|---|
| 2026-10-14 | 0.0.0-e2e | 0.1 MiB | 9.5 MiB | 0.9 | 2.2 | 1.5 |

## Step 0. Preparing the new VM

Where the restic address comes from:

| Storage (from the card) | `<location>` | `<адрес-restic>` |
|---|---|---|
| own S3 (`custom`) | `s3:https://s3.example.com/<bucket>[/<path>]` | the same |
| AWS S3 | `s3:<bucket>[/<path>]` | `s3:https://s3.<region>.amazonaws.com/<bucket>[/<path>]` |
| Backblaze B2 | `s3:<bucket>[/<path>]` | `s3:https://s3.<region>.backblazeb2.com/<bucket>[/<path>]` |
| SFTP | `sftp:<user>@<host>:<path>` | the same |

The card does not keep the region: it is yours. The agent image must be of the
version in the card: it holds restic (`/usr/libexec/sard/restic`) and `repo add`.
With internet access:

```bash
docker pull ghcr.io/artur-abalov/sard-agent:<sardVersion>-pg18
docker pull ghcr.io/artur-abalov/sard-server:<sardVersion>
docker pull postgres:18.6-alpine@sha256:898c60cb390d1085bc97bdd6a1ae968757421df394b071848b52b1c09c89d6a5
# the tag an offline installation's .env names (section 11)
docker tag postgres:18.6-alpine@sha256:898c60cb390d1085bc97bdd6a1ae968757421df394b071848b52b1c09c89d6a5 postgres:18.6-alpine
```

Without internet access the archive with the images
(`sard-<version>-images-linux-<arch>.tar.gz`, [section 11](../../operator/11-offline.md))
is in `~/sard-offline`:

<!-- drill: images -->
```bash
set -euo pipefail
gunzip -c ~/sard-offline/sard-*-images-linux-*.tar.gz | docker load
docker images --format '{{.Repository}}:{{.Tag}}' | grep -c 'sard-'
```

The directory `~/sard-recovery` with mode `700` holds the password, the storage
keys and the working files; secrets are typed without echo and land in files
with mode `600`. The agent image (`<образ-агента>`) is `sard-agent:<sardVersion>-pg18`
with the registry you took it from. For SFTP the loop that reads the S3 keys is
not needed: remove it, and the `s3.*` files.

<!-- drill: prepare -->
```bash
set -euo pipefail
umask 077
mkdir -p ~/sard-recovery
chmod 700 ~/sard-recovery
cd ~/sard-recovery
cat > state.env <<'EOF'
REPO='<адрес-restic>'
AGENT_IMAGE='<образ-агента>'
EOF
cat > restic <<'EOF'
#!/bin/bash
cd "$(dirname "$0")" && . ./state.env
env_file() {
  printf '%s=%s\n' AWS_ACCESS_KEY_ID "$(cat s3.id)" AWS_SECRET_ACCESS_KEY "$(cat s3.secret)" AWS_DEFAULT_REGION "$(cat s3.region)"
}
exec docker run --rm --user "$(id -u):$(id -g)" --env-file <(env_file) -v "$PWD:$PWD" -w "$PWD" \
  --entrypoint /usr/libexec/sard/restic "$AGENT_IMAGE" --no-cache -r "$REPO" --password-file restic.pass "$@"
EOF
read -rs -p 'restic repository password: ' P; echo
printf '%s' "$P" > restic.pass
for F in s3.id s3.secret s3.region; do
  read -rs -p "$F: " V; echo
  printf '%s' "$V" > "$F"
done
unset P V
chmod 700 restic
chmod 600 state.env restic.pass s3.id s3.secret s3.region
test "$(stat -c '%a' . restic restic.pass s3.id s3.secret s3.region | tr '\n' ' ')" = '700 700 600 600 600 600 '
ls -l
```

The file `restic` is a wrapper: it runs restic from the agent image as your user
(the storage keys go through `--env-file`, not the command line). If the VM
already has restic of its own, replace the wrapper with this variant; the other
steps do not change:

```bash
cat > ~/sard-recovery/restic <<'EOF'
#!/bin/bash
cd "$(dirname "$0")" && . ./state.env
while read -r N F; do export "$N=$(cat "$F")"; done <<'KEYS'
AWS_ACCESS_KEY_ID s3.id
AWS_SECRET_ACCESS_KEY s3.secret
AWS_DEFAULT_REGION s3.region
KEYS
exec restic -r "$REPO" --password-file restic.pass --no-cache "$@"
EOF
chmod 700 ~/sard-recovery/restic
```

SFTP: in the wrapper from the image, instead of `--env-file` mount your SSH
directory (`-v "$HOME/.ssh:/ssh:ro"`) and add
`-o sftp.args='-i /ssh/id_ed25519 -o UserKnownHostsFile=/ssh/known_hosts'`; in the
wrapper with a restic of its own your `~/.ssh` is enough. The drill passes S3;
it does not test SFTP.

**Check.** The block `images` prints the number of Sard images loaded (at least
two: the server and the `-pg18` agent). The last command of the block `prepare`
prints the files; the directory and the wrapper have mode `700`, the other files
`600`. The commands leave no secrets in the shell history: input goes through
`read -rs`.

**If it failed.** `docker load` does not find the file: the archive was not
copied or the version differs ([section 11](../../operator/11-offline.md)). `read`
skipped an input: repeat the whole block, it overwrites the files.

## Step 1. Snapshots: choosing and checking against the card

The storage holds
snapshots of two sources:

- **D**, the database snapshot: the last snapshot with the tag
  `postgresql.part=database` (a database snapshot appears only after a successful
  dump);
- **K**, the keys and configuration snapshot: the last snapshot of the paths
  `--path /var/lib/sard/pki --path /etc/sard/install` that has `ca.key`,
  `ca.crt`, `.env` and `docker-compose.yml`. A failed step could leave an
  incomplete snapshot, so the block checks completeness itself and takes the
  first complete one, newest first.

The block first checks `restic cat config` against the card: the repository `id`
must equal `repositoryId`. Another repository with the same password has another
`id`.

<!-- drill: snapshots -->
```bash
set -euo pipefail
cd ~/sard-recovery
. ./state.env
ID=$(./restic cat config | grep -oE '"id": *"[0-9a-f]+"' | cut -d'"' -f4)
echo "repository id: $ID"
if [ "$ID" != '<repositoryId>' ]; then echo 'WRONG REPOSITORY: stop here' >&2; exit 1; fi
list() { ./restic snapshots --json "$@" | grep -oE '"(time|id)":"[^"]*"' | cut -d'"' -f4 | paste - -; }
complete() {
  local L
  L=$(./restic ls "$1")
  for F in /var/lib/sard/pki/ca/ca.key /var/lib/sard/pki/ca/ca.crt /etc/sard/install/.env /etc/sard/install/docker-compose.yml; do
    if ! grep -qxF "$F" <<<"$L"; then echo "snapshot $1 lacks $F" >&2; return 1; fi
  done
}
read -r DT DID <<<"$(list --latest 1 --tag postgresql.part=database | tail -1)"
if [ -z "$DID" ]; then echo 'no snapshot of the database' >&2; exit 1; fi
K=
while read -r KT KID; do
  if complete "$KID"; then K=$KID; break; fi
done < <(list --path /var/lib/sard/pki --path /etc/sard/install | tac)
if [ -z "$K" ]; then echo 'no complete snapshot of the keys' >&2; exit 1; fi
printf 'D=%s\nK=%s\n' "$DID" "$K" >> state.env
echo "database snapshot: $DID ($DT)"
echo "keys snapshot:     $K ($KT)"
if [[ "$KT" < "$DT" ]]; then
  echo 'WARNING: the keys snapshot is older than the database one; if the server refuses with DATABASE_NEWER_THAN_SERVER, raise SARD_VERSION in ~/sard/.env'
fi
```

**Check.** The first line is `repository id:` with a number equal to the card's
`repositoryId`; then two snapshots with their times. If the keys are older than
the database, the block warns: then on step 4 the server may refuse with
`DATABASE_NEWER_THAN_SERVER`, because `SARD_VERSION` in the `.env` of snapshot K
is lower than the version whose migrations are already in the database.

**If it failed.** `wrong password` or `no key found`: the password in `restic.pass`
is wrong. A connection error: check the storage address, the region and the keys;
nothing is created on the VM, repeat step 0. `WRONG REPOSITORY`: the address
points to another repository; stop and find the right address. `no complete
snapshot of the keys`: there is no complete keys snapshot; the card and the old
`.env` are needed by hand, see [section 8](../../operator/08-migrate-and-remove.md)
and the CA backup you made separately.

## Step 2. Keys and configuration

Snapshot K is restored into `~/sard-recovery/keys`;
from there `.env` and the compose files go to `~/sard`, and the CA key and
certificate go straight into the volume `sard_sard-pki`, owner 10001, mode `700`
(directories) and `600` (files). `.env` is read by group 10001 (the neighbour
runs under the server's uid): group 10001, mode `640`.

<!-- drill: keys -->
```bash
set -euo pipefail
cd ~/sard-recovery
. ./state.env
./restic restore "$K" --target keys
mkdir -p ~/sard
cp keys/etc/sard/install/.env keys/etc/sard/install/docker-compose.yml ~/sard/
if [ -f keys/etc/sard/install/docker-compose.override.yml ]; then
  cp keys/etc/sard/install/docker-compose.override.yml ~/sard/
fi
chmod 640 ~/sard/.env
docker run --rm --user 0 -v "$HOME/sard:/sard" --entrypoint chgrp "$AGENT_IMAGE" 10001 /sard/.env
cd ~/sard
docker compose create --pull never
docker run --rm --user 0 -v sard_sard-pki:/pki -v "$HOME/sard-recovery/keys/var/lib/sard/pki:/from:ro" \
  --entrypoint sh "$AGENT_IMAGE" -c 'mkdir -p /pki/ca && cp /from/ca/ca.key /from/ca/ca.crt /pki/ca/ && chown -R 10001:10001 /pki && chmod 700 /pki /pki/ca && chmod 600 /pki/ca/ca.key /pki/ca/ca.crt'
test "$(stat -c '%a %g' ~/sard/.env)" = '640 10001'
docker run --rm --user 0 -v sard_sard-pki:/pki:ro --entrypoint stat "$AGENT_IMAGE" -c '%u:%g %a %n' /pki/ca /pki/ca/ca.key /pki/ca/ca.crt
grep '^SARD_VERSION=' ~/sard/.env
```

**Check.** Three lines `10001:10001 700 /pki/ca`, `10001:10001 600 …/ca.key`,
`10001:10001 600 …/ca.crt`, then `SARD_VERSION=` of the installation. `.env` in
`~/sard` is the one the old VM had.

**If it failed.** A CA directory with modes wider than `0700`/`0600` keeps the
server from starting: repeat the block. `docker compose create` complains about a
variable: `.env` was not restored completely (step 1 chose an incomplete
snapshot K). No image locally: do step 0.

## Step 3. The database

Only PostgreSQL goes up first: `docker compose up -d --wait postgres`
(offline, with `--pull never`, as in the block). The empty database `sard` is
created by the image initialisation from `.env`. The dump from snapshot D is
restored into it with `pg_restore --no-owner`: the user `sard` owns everything.
The dump does not contain the role `sard_self`: the server brings it back at start.

<!-- drill: database -->
```bash
set -euo pipefail
cd ~/sard-recovery
. ./state.env
./restic restore "$D" --target dump
cd ~/sard
docker compose up -d --wait --pull never postgres
docker compose exec -T postgres pg_restore -U sard -d sard --no-owner < ~/sard-recovery/dump/sard.dump
docker compose exec -T postgres psql -U sard -d sard -Atc 'select count(*) from agents'
docker compose exec -T postgres psql -U sard -d sard -Atc 'select version from flyway_schema_history order by installed_rank desc limit 1'
```

**Check.** `pg_restore` prints nothing and exits with code 0; then two values:
the number of agents in the database and the version of the last migration (it
also tells which server version to run).

**If it failed.** `already exists`: the database `sard` already has tables (the
block ran twice, or the server managed to apply migrations). Recreate the
database and repeat the step: `docker compose exec -T postgres psql -U sard -d
postgres -c 'DROP DATABASE sard WITH (FORCE)' -c 'CREATE DATABASE sard OWNER sard'`.
No `dump/sard.dump`: snapshot D is not a database; check the tag
`postgresql.part=database`.

## Step 4. The server

`docker compose up -d --wait --pull never server` starts the
server on the restored database. It does three things that matter for recovery:
it checks that the database is not newer than itself (otherwise it refuses with
`DATABASE_NEWER_THAN_SERVER`, changing nothing), it checks the CA directory
against the database, and it brings back the role `sard_self` that the dump lacks.

<!-- drill: server -->
```bash
set -euo pipefail
cd ~/sard
docker compose up -d --wait --pull never server
docker compose logs server | grep -F 'CA fingerprint=<caFingerprint> origin=existing'
curl -fsS http://localhost:8080/api/v1/status | grep -F '"version":"<sardVersion>"'
curl -fsS http://localhost:8080/api/v1/onboarding | grep -o '"restriction":"none"'
docker compose exec -T postgres psql -U sard -d sard -Atc "select count(*) from pg_roles where rolname = 'sard_self'"
```

**Check.** The log has the line `CA fingerprint=<card fingerprint>
origin=existing`; the fingerprint must equal the card's `caFingerprint`; the
version in `/api/v1/status` equals `sardVersion`; `restriction` is `none` (the
onboarding is done, no setup code is printed); the last command prints `1`: the
role `sard_self` is in place. If the fingerprint differs it is another CA: stop
the server (`docker compose stop server`), do not switch the name, go back to
step 1 and take an older snapshot K.

**If it failed.** The cause is the first line of the exception in
`docker compose logs server`:

- `DATABASE_NEWER_THAN_SERVER`: the database has a migration newer than the server
  version in `.env` knows; snapshot K is older than snapshot D. Raise
  `SARD_VERSION` in `~/sard/.env` to the version from the message (or the card's
  `sardVersion`) and repeat the block; nothing was changed, repeating is safe.
- `CA startup refused: … CA_ORIGIN_NOT_RECORDED`: the CA directory holds a CA the
  database does not know: keys and database from different installations. Go back
  to step 1 and take a pair from one self-backup; if you restored the database into
  something other than an empty `sard`, recreate it (step 3).
- `CA startup refused: … CA_MISSING`: the database says the CA is in use and the
  directory is empty; repeat step 2.
- `password authentication failed`: `SARD_DB_PASSWORD` in `.env` is not the one
  the database volume was created with; in a new volume the password comes from
  `.env` on the first start, so the cause is a mismatch of `.env` and the volume.

Other start refusals: [section 9](../../operator/09-troubleshooting.md).

## Step 5. The neighbour and the self-backup

In the restored database the former
built-in agent `sard-self` (call it X) is still alive: the server issues no token
to a second one while X is not revoked. The neighbour of the new VM has no
identity (it was in the old VM's volume) and waits for a token file. The plan:
sign in, start the neighbour, revoke X, let the neighbour become the new built-in
agent (Y), attach the existing repository to it, bind the self-backup, run it and
confirm the key copy.

Signing in uses the same administrator password as before the disaster; it is
typed without echo and goes to `curl` on standard input.

<!-- drill: login -->
```bash
set -euo pipefail
umask 077
cd ~/sard-recovery
read -rs -p 'administrator password: ' A; echo
A=$(printf '%s' "$A" | sed 's/\\/\\\\/g; s/"/\\"/g')
CODE=$(printf '{"password":"%s"}' "$A" | curl -sS -o /dev/null -w '%{http_code}' -c cookie -H 'Content-Type: application/json' --data-binary @- http://localhost:8080/api/v1/session)
unset A
echo "$CODE"
test "$CODE" = 204
```

The neighbour starts before the revocation and waits for a token:

<!-- drill: self-agent-wait -->
```bash
set -euo pipefail
cd ~/sard
docker compose up -d --wait --pull never self-agent
sleep 15
docker compose logs self-agent | grep -c 'waiting for the enrollment token'
docker compose exec -T server ls /var/lib/sard/self
```

One line `waiting for the enrollment token` and only `db-password` in the
channel, no `enroll-token`: that is as it should be. Now revoke X
(`POST /api/v1/agents/<id>/revoke?confirm=sard-self`; in the console, "Revoke"
and typing `sard-self`). The revocation turns X's steps to `lost` with the
message `agent revoked`; the server issues a token, the neighbour registers
itself:

<!-- drill: revoke-x -->
```bash
set -euo pipefail
cd ~/sard-recovery
API=http://localhost:8080/api/v1
live() { curl -fsS -b cookie "$API/agents?limit=100" | tr '{' '\n' | grep '"builtin":true' | grep '"revokedAt":null' | grep -o '"id":"[0-9a-f-]*"' | head -1 | cut -d'"' -f4 || true; }
X=$(live)
test -n "$X"
echo "previous built-in agent: $X"
curl -fsS -b cookie -X POST "$API/agents/$X/revoke?confirm=sard-self" | grep -o '"revokedAt":"[^"]*"'
Y=
for I in $(seq 60); do
  Y=$(live)
  if [ -n "$Y" ] && [ "$Y" != "$X" ]; then break; fi
  sleep 3
done
test -n "$Y" -a "$Y" != "$X"
echo "new built-in agent: $Y"
for I in $(seq 60); do
  if curl -fsS -b cookie "$API/agents?limit=100" | tr '{' '\n' | grep "\"id\":\"$Y\"" | grep -F '"status":"online"' > /dev/null; then break; fi
  sleep 3
done
curl -fsS -b cookie "$API/agents?limit=100" | tr '{' '\n' | grep "\"id\":\"$Y\"" | grep -o '"status":"[a-z]*"'
curl -fsS -b cookie "$API/self-backup" | grep -o '"warnings":\[[^]]*\]'
```

**Check.** It prints the `revokedAt` of the revoked X, then the id of the new Y
(not equal to X), its status `online` and the self-backup warnings:
`agent_replaced` (the sources are still bound to X) and `failing`: the self-backup
runs that were going at the moment of the snapshot became `failed` (step 7), until
a new one succeeds.

Attaching the existing repository is `repo add` with the password and keys from
the step 0 files (`--password-from-file`, `--secret-key-from-file`; they do not
travel as values on the command line). The command is the one the wizard shows,
plus `~/sard-recovery` mounted read-only. For AWS and B2, instead of the address,
`s3:<bucket>[/<path>] --provider aws` (or `b2`) `--region <region>`:

<!-- drill: repo-add -->
```bash
set -euo pipefail
cd ~/sard
docker compose run --rm --no-deps --pull never -u 0 -v "$HOME/sard-recovery:/recovery:ro" \
  --cap-add CHOWN --cap-add DAC_OVERRIDE --cap-add FOWNER --cap-add SETUID --cap-add SETGID \
  self-agent repo add '<repositoryName>' '<адрес-restic>' \
  --access-key-id "$(cat ~/sard-recovery/s3.id)" --region "$(cat ~/sard-recovery/s3.region)" \
  --secret-key-from-file /recovery/s3.secret --password-from-file /recovery/restic.pass \
  --no-restart --config /etc/sard/self/agent.yaml
docker compose restart self-agent
API=http://localhost:8080/api/v1
S=
for I in $(seq 60); do
  S=$(curl -fsS -b ~/sard-recovery/cookie "$API/self-backup" | grep -o '"repositories":\[[^]]*\]' || true)
  case "$S" in *'"repositoryId":"<repositoryId>"'*) break ;; esac
  sleep 3
done
case "$S" in *'"repositoryId":"<repositoryId>"'*) ;; *) echo 'the neighbour does not report the repository' >&2; exit 1 ;; esac
echo "$S"
```

**Check.** The output of `repo add` says the repository is attached (`attached`),
and the `repositories` of the self-backup state holds a repository with the name
and `repositoryId` from the card.

Rebinding (`PUT /api/v1/self-backup/repository` with the card's `location`) moves
the former sources (ids and history stay) to Y and writes `location` into the new
recovery card. Then "run now" (`POST /api/v1/self-backup/runs`) and wait for both
runs:

<!-- drill: rebind -->
```bash
set -euo pipefail
cd ~/sard-recovery
API=http://localhost:8080/api/v1
printf '{"repositoryName":"%s","location":"%s"}' '<repositoryName>' '<location>' | curl -fsS -b cookie -X PUT -H 'Content-Type: application/json' --data-binary @- "$API/self-backup/repository" > /dev/null
curl -fsS -b cookie "$API/self-backup/recovery-card" | grep -o '"location":"[^"]*"'
```

<!-- drill: backup-now -->
```bash
set -euo pipefail
cd ~/sard-recovery
API=http://localhost:8080/api/v1
curl -fsS -b cookie -X POST "$API/self-backup/runs" | grep -o '"role":"[a-z_]*"'
S=
for I in $(seq 100); do
  S=$(curl -fsS -b cookie "$API/self-backup")
  case "$S" in *'"firstBackupDone":true'*) break ;; esac
  sleep 3
done
case "$S" in *'"firstBackupDone":true'*) ;; *) echo 'the first backups did not finish' >&2; exit 1 ;; esac
echo "$S" | grep -o '"warnings":\[[^]]*\]'
```

**Check.** It prints the `location` equal to the card's `location`; then the roles
of the two started sources (`self_database`, `self_keys`) and the warnings:
`keys_unconfirmed` remains (after a rebinding the password and access copy has to
be confirmed again). Confirm it (`keys-confirmation`):

<!-- drill: confirm-keys -->
```bash
set -euo pipefail
cd ~/sard-recovery
API=http://localhost:8080/api/v1
CODE=$(curl -sS -o /dev/null -w '%{http_code}' -b cookie -X POST -H 'Content-Type: application/json' -d '{"copiedOutsideServer":true}' "$API/self-backup/keys-confirmation")
echo "$CODE"
test "$CODE" = 204
S=$(curl -fsS -b cookie "$API/self-backup")
echo "$S" | grep -o '"status":"[a-z]*"' | head -1
echo "$S" | grep -o '"warnings":\[[^]]*\]'
```

**Check.** Code `204`, `"status":"ok"` and `"warnings":[]`.

**If it failed.** `WRONG_PASSWORD` in the `repo add` output: the password in
`restic.pass` is wrong, no traces were left; fix the file and repeat the block.
The revocation answers `409`: no `confirm=sard-self`. Y does not appear: the server
issues a token every 15 seconds (`SARD_SELF_CHECK_INTERVAL`), look at
`docker compose logs self-agent server`. `422 repository_not_initialized` when
rebinding: the neighbour has not sent the repository yet; wait a minute.
`first_backup_pending` on confirmation: both runs have not finished yet. A run
`failed`: the step message in the console, most often the storage keys or the
rights of `sard_self` ([the neighbour agent](../../operations/self-agent.md)).

## Step 6. The server name and the agents

Switch the DNS name of the server to the
new VM (an A record or moving the IP). The name is the same as before: the server
certificate is issued for the names in `SARD_PKI_SERVER_NAMES` from `.env`, port
9090 is open, and there is no TLS-terminating proxy between agent and server
([section 4](../../operator/04-tls-and-names.md)). Agents registered before the
database snapshot reconnect on their own, without re-enrolling or restarting: the
CA is the same and their certificates are in the database. They reconnect with a
growing pause, so it takes up to a few minutes.

If the name changes, add the new one to `SARD_PKI_SERVER_NAMES` (keep the old
one), fix `SARD_AGENT_ENDPOINT`, fix `server.address` in `/etc/sard/agent.yaml` on
each agent and restart the service: the order and details are in
[section 8](../../operator/08-migrate-and-remove.md). For the future, use a DNS name, not an IP.

<!-- drill: agents-online -->
```bash
set -euo pipefail
cd ~/sard-recovery
curl -fsS -b cookie 'http://localhost:8080/api/v1/agents?limit=100' | tr '{' '\n' | grep '"status":"online"' | grep -o '"hostname":"[^"]*"'
```

**Check.** The block prints the host names of the agents that are `online`: all
agents registered before the database snapshot, and `sard-self`.

**If it failed.** An agent does not appear: on its host
`journalctl -u sard-agent --since '-10min'`; a TLS error about the host name means
the name is not in `SARD_PKI_SERVER_NAMES`, a closed port 9090 is a firewall.
`CERT_UNKNOWN`: the agent was registered after the database snapshot, see step 7.

## Step 7. After the recovery

The page does not know what happened between the
database snapshot and the disaster, but the consequences are predictable. Agents
registered after the database snapshot get `CERT_UNKNOWN` and stop with exit
code 78 (systemd does not restart them). On the agent host
`journalctl -u sard-agent` shows `reason CERT_UNKNOWN … not reconnecting`, the
server log shows `agent call refused: reason=CERT_UNKNOWN`:

```bash
journalctl -u sard-agent --no-pager | grep CERT_UNKNOWN
```

Such an agent registers again with a new token (console, "Enrollment tokens").
On its host the token is typed without echo and never lands on the command line:

<!-- drill: reenroll -->
```bash
set -euo pipefail
umask 077
read -rs -p 'enrollment token: ' T; echo
printf '%s' "$T" > /tmp/sard-enroll-token
unset T
sudo sard-agent enroll --force --server <имя-сервера>:9090 --token-file /tmp/sard-enroll-token
rm -f /tmp/sard-enroll-token
sudo systemctl restart sard-agent
```

The old record of such an agent stays in the console, offline: revoke it. The other
consequences:

- **Enrollment tokens issued after the snapshot** are unknown to the server
  (`TOKEN_UNKNOWN`): issue new ones.
- **Agents revoked after the snapshot** are alive again: the revocation came after
  the snapshot and is not in the database. Revoke them once more
  (`POST /api/v1/agents/<id>/revoke` or "Revoke" in the console), otherwise they
  keep connecting.
- **The administrator password** is the one at the time of the database snapshot.
  If you changed it after the snapshot, the old one is in force; change it again in
  the settings.
- **Snapshots made after the database snapshot** are in the storages but not in
  the server's history: the history stops at snapshot D. The data itself is intact.
- **Runs that were going at the moment of the snapshot** become `failed` with the
  step `lost`: the server did not get the result. This includes the database
  self-backup run that made snapshot D and the keys run queued with it.
- **Schedules.** A source with a schedule that missed firings during the downtime
  gets one catch-up run (`trigger` `catch_up`, the downtime period is in `catchUp`)
  if two or more were missed; one missed firing is an ordinary late run. A source
  whose run was going at the moment of the snapshot gets no catch-up: the firing is
  recorded as `skipped_active`.
- **The gap in the history** is visible from what already exists: `failed` with the
  step `lost`, `catchUp` and the line `CA fingerprint=… origin=existing` in the
  log of the new VM's first start. There is no separate "restored from backup" mark.

Cleaning up: `~/sard-recovery` holds the repository password, the storage keys, the
cookie and a copy of the CA key. They are needed only while recovering:

<!-- drill: cleanup -->
```bash
set -euo pipefail
rm -rf ~/sard-recovery
test ! -e ~/sard-recovery
```

Keep the repository password and the card outside the VM, as before
([section 6](../../operator/06-data-and-backup.md)). After recovery the server
already issues a new card (`GET /api/v1/self-backup/recovery-card`): print it again.
