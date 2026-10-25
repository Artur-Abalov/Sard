#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-only
# Copyright 2026 Artur Abalov
#
# Test of scripts/check-workflow-secrets.sh on throwaway workflow directories.
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CHECK="$HERE/check-workflow-secrets.sh"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

die() { echo "test-check-workflow-secrets: FAIL: $*" >&2; exit 1; }

# expect <want exit> <description> <workflow content>: the workflow alone in a directory.
expect() {
  local rc=0 out dir
  dir="$(mktemp -d -p "$WORK")"
  printf '%s\n' "$3" >"$dir/w.yml"
  out="$("$CHECK" "$dir" 2>&1)" || rc=$?
  [ "$rc" -eq "$1" ] || die "$2: exit $rc, want $1: $out"
  [ "$1" -eq 0 ] || grep -q 'check-workflow-secrets: w.yml:' <<<"$out" || die "$2: offender not named: $out"
}

readonly RELEASE_ON=$'on:\n  push:\n    tags: ["v*"]\n'
readonly SIGN_JOB=$'jobs:\n  sign:\n    runs-on: ubuntu-latest\n    environment: release\n    steps:\n      - run: sign\n        env:\n          KEY: ${{ secrets.MINISIGN_SECRET_KEY }}\n'

expect 0 "signing in the release environment on tags" "$RELEASE_ON$SIGN_JOB"
expect 0 "cosign key in the release environment" "${RELEASE_ON}${SIGN_JOB//MINISIGN_SECRET_KEY/COSIGN_PRIVATE_KEY}"
expect 0 "environment as a map" "$RELEASE_ON${SIGN_JOB//environment: release/environment:
      name: release}"
expect 1 "signing key without the environment" "$RELEASE_ON${SIGN_JOB//    environment: release
/}"
expect 1 "signing key in another environment" "$RELEASE_ON${SIGN_JOB//environment: release/environment: staging}"
expect 1 "signing key in a workflow pull requests run" $'on:\n  pull_request:\n  push:\n    tags: ["v*"]\n'"$SIGN_JOB"
expect 1 "signing key in a workflow pushes to branches run" $'on:\n  push:\n    branches: [main]\n    tags: ["v*"]\n'"$SIGN_JOB"
expect 1 "signing key on workflow_dispatch" $'on:\n  workflow_dispatch:\n'"$SIGN_JOB"
expect 1 "the environment of another job" "$RELEASE_ON"$'jobs:\n  a:\n    environment: release\n    steps: []\n  b:\n    steps:\n      - run: x\n        env:\n          K: ${{ secrets.COSIGN_PASSWORD }}\n'
expect 1 "any secret in a pull request workflow" $'on: [pull_request]\njobs:\n  a:\n    steps:\n      - run: x\n        env:\n          T: ${{ secrets.QODANA_TOKEN }}\n'
expect 1 "any secret in a pull request workflow, map trigger" $'on:\n  pull_request:\n    paths: ["a"]\njobs:\n  a:\n    steps:\n      - env:\n          T: ${{ secrets.X }}\n'
expect 1 "a token merely named like the workflow token" $'on: pull_request\njobs:\n  a:\n    steps:\n      - env:\n          T: ${{ secrets.DEPLOY_GITHUB_TOKEN }}\n'
expect 1 "every secret handed to a reusable workflow" $'on:\n  workflow_dispatch:\njobs:\n  a:\n    uses: ./.github/workflows/b.yml\n    secrets: inherit\n'
expect 1 "on: with a comment, then a pull request trigger" $'on: # triggers\n  pull_request:\njobs:\n  a:\n    steps:\n      - env:\n          T: ${{ secrets.X }}\n'
expect 1 "triggers the checker cannot read" $'"on": [pull_request]\njobs: {}\n'
expect 1 "triggers as a flow map" $'on: { pull_request_target: {} }\njobs: {}\n'
expect 0 "the workflow token in a pull request workflow" $'on: pull_request\njobs:\n  a:\n    steps:\n      - env:\n          T: ${{ secrets.GITHUB_TOKEN }}\n          U: ${{ github.token }}\n'
expect 0 "another secret off pull requests" $'on:\n  workflow_dispatch:\njobs:\n  a:\n    steps:\n      - env:\n          T: ${{ secrets.QODANA_TOKEN }}\n'
expect 1 "pull_request_target" $'on:\n  pull_request_target:\njobs: {}\n'
expect 1 "workflow_run" $'on:\n  workflow_run:\n    workflows: [CI]\njobs: {}\n'
expect 1 "pull_request_target in a list" $'on: [push, pull_request_target]\njobs: {}\n'
expect 1 "every secret at once" $'on:\n  push:\n    tags: ["v*"]\njobs:\n  a:\n    environment: release\n    steps:\n      - uses: x/y@0000000000000000000000000000000000000000 # v1\n        with:\n          s: ${{ toJSON(secrets) }}\n'
# An expression in a run block is evaluated even in a shell comment: every occurrence counts.
expect 1 "a comment naming a secret" $'on: pull_request\njobs:\n  a:\n    steps:\n      - run: |\n          # ${{ secrets.MINISIGN_SECRET_KEY }}\n'
expect 0 "the word secrets in prose" $'on: pull_request\n# logs, secrets masked, are uploaded\njobs: {}\n'

out="$("$CHECK" "$HERE/../.github/workflows")" || die "the repository's workflows: $out"
grep -q 'OK' <<<"$out" || die "the repository's workflows: no OK in: $out"

echo "test-check-workflow-secrets: OK"
