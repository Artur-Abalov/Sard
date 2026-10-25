#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-only
# Copyright 2026 Artur Abalov
#
# Fails when a workflow could hand a secret to code it did not review (SC1,
# ADR 00XX-draft-supply-chain):
#   - no pull_request_target and no workflow_run trigger anywhere: both run
#     with the repository's secrets on behalf of a pull request;
#   - a workflow that pull requests run references no secret but the
#     workflow token (secrets.GITHUB_TOKEN, github.token);
#   - the signing keys (secrets.MINISIGN_*, secrets.COSIGN_*) only in a job
#     of the environment "release" (owner's approval, v* tags only:
#     docs/release.md) of a workflow that nothing but a tag push starts;
#   - never every secret at once (toJSON(secrets), secrets[...]).
# Every occurrence counts, comments included: an expression in a run block is
# evaluated even inside a shell comment.
# Optional argument: the directory of the workflows (default .github/workflows).
set -euo pipefail

DIR="${1:-$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)/.github/workflows}"

failed=0
checked=0
for file in "$DIR"/*.yml "$DIR"/*.yaml; do
  [ -f "$file" ] || continue
  checked=$((checked + 1))
  offenders="$(awk -v file="$(basename "$file")" '
    function report(line, why) { printf "check-workflow-secrets: %s:%d: %s\n", file, line, why }
    function trigger(name) {
      triggers[name] = 1
      if (name == "pull_request_target" || name == "workflow_run")
        report(FNR, name " runs with the secrets for code nobody reviewed")
    }
    function close_job(   i) {
      for (i = 1; i <= nrefs; i++) {
        if (refs[i] ~ /GITHUB_TOKEN/) continue
        if (refs[i] ~ /toJSON|\[/)
          report(reflines[i], refs[i] " hands on every secret at once")
        else if ("pull_request" in triggers)
          report(reflines[i], refs[i] " in a workflow that pull requests run")
        else if (refs[i] ~ /MINISIGN|COSIGN/ && (env != "release" || !tag_only()))
          report(reflines[i], refs[i] " outside a job of the environment release on tags only")
      }
      nrefs = 0; env = ""
    }
    function tag_only(   t) {
      for (t in triggers) if (t != "push") return 0
      return ("push" in triggers) && push_tags && !push_branches
    }
    # on: a scalar, a list or a map of triggers (before jobs:).
    /^on:[ \t]*[A-Za-z_]/ { sub(/^on:[ \t]*/, ""); trigger($1) }
    /^on:[ \t]*\[/ { s = $0; sub(/^on:[ \t]*\[/, "", s); sub(/\].*/, "", s); n = split(s, l, /[ \t]*,[ \t]*/); for (i = 1; i <= n; i++) trigger(l[i]) }
    /^on:[ \t]*$/ { in_on = 1; next }
    in_on && /^[^ \t#]/ { in_on = 0 }
    in_on && /^  [A-Za-z_]+:/ { name = $1; sub(/:.*/, "", name); trigger(name); current = name }
    in_on && current == "push" && /^    tags(-ignore)?:/ { push_tags = 1 }
    in_on && current == "push" && /^    branches(-ignore)?:/ { push_branches = 1 }
    /^jobs:/ { in_jobs = 1; next }
    in_jobs && /^  [A-Za-z0-9_-]+:[ \t]*$/ { close_job() }
    in_jobs && /^    environment:[ \t]*[^ \t]/ { env = $2 }
    in_jobs && /^      name:[ \t]*/ && prev ~ /^    environment:[ \t]*$/ { env = $2 }
    {
      line = $0
      while (match(line, /secrets\.[A-Za-z_][A-Za-z0-9_]*|secrets\[|toJSON\(secrets\)/)) {
        refs[++nrefs] = substr(line, RSTART, RLENGTH); reflines[nrefs] = FNR
        line = substr(line, RSTART + RLENGTH)
      }
      prev = $0
    }
    END { close_job() }
  ' "$file")"
  if [ -n "$offenders" ]; then
    echo "$offenders"
    failed=1
  fi
done

if [ "$failed" -ne 0 ]; then
  echo "check-workflow-secrets: FAILED"
  exit 1
fi
echo "check-workflow-secrets: $checked workflows OK"
