#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-only
# Copyright 2026 Artur Abalov
#
# Fails when something the build or CI pulls is named by a tag instead of an
# immutable reference (SC1, ADR 00XX-draft-supply-chain):
#   - workflows and actions in .github/: every "uses:" is owner/repo@<40-hex
#     commit SHA> followed by a "# v<version>" comment, a local "./" action or
#     docker://<image>@sha256:<digest>; "container:" and "image:" by digest;
#   - Dockerfiles: every FROM by digest, or scratch, or an earlier stage, or
#     an ARG whose default is pinned by digest;
#   - compose files: every "image:" by digest (also as a ${VAR:-default});
#   - *.env and .env.example: every *IMAGE* variable by digest.
# A line is allowed unpinned only when it, or the line directly above,
# carries "allow-unpinned: <reason>" with a non-empty reason (our own release
# images, signed with cosign; images a compose file builds itself).
# docs/ is prose and history and is skipped, as are this script and its test.
# Optional argument: the root directory to check (default: the repository).
set -euo pipefail

ROOT="${1:-$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)}"
cd "$ROOT"

# kind <path>: which rules apply to the file, or nothing.
kind() {
  case "$1" in
    docs/* | scripts/check-pins.sh | scripts/test-check-pins.sh) return ;;
    .github/workflows/*.yml | .github/workflows/*.yaml | .github/actions/*.yml | .github/actions/*.yaml) echo workflow ;;
    *Dockerfile | *Dockerfile.*) echo dockerfile ;;
    *compose*.yml | *compose*.yaml) echo compose ;;
    *.env | *.env.example) echo env ;;
  esac
}

failed=0
checked=0
while IFS= read -r -d '' file; do
  [ -f "$file" ] || continue
  k="$(kind "$file")"
  [ -n "$k" ] || continue
  checked=$((checked + 1))
  offenders="$(awk -v file="$file" -v kind="$k" '
    function allowed(s) { return s ~ /allow-unpinned:[ \t]*[^ \t]/ }
    function digest(s) { return s ~ /@sha256:[0-9a-f]{64}/ }
    function strip(s) { sub(/[ \t]+#.*$/, "", s); gsub(/["\047]/, "", s); return s }
    function report(why) {
      if (!allowed($0) && !allowed(prev)) printf "check-pins: %s:%d: %s\n", file, FNR, why
    }
    function workflow(   ref) {
      if (match($0, /^[ \t]*(-[ \t]+)?uses:[ \t]*/)) {
        ref = strip(substr($0, RLENGTH + 1))
        if (ref ~ /^\.\//) return
        if (ref ~ /^docker:\/\//) { if (!digest(ref)) report("docker action not pinned by digest: " ref); return }
        if (ref !~ /^[^@ \t]+@[0-9a-f]{40}$/) { report("action not pinned by a full commit SHA: " ref); return }
        if ($0 !~ /#[ \t]*v?[0-9]/) report("pinned action without a \"# v<version>\" comment: " ref)
        return
      }
      if (match($0, /^[ \t]*(-[ \t]+)?(container|image):[ \t]*[^ \t{]/)) {
        ref = strip(substr($0, RLENGTH))
        if (!digest(ref)) report("image not pinned by digest: " ref)
      }
    }
    function dockerfile(   n, f, i, img) {
      if (toupper($1) == "ARG" && split($2, f, "=") >= 1) { arg[f[1]] = (index($2, "=") ? substr($2, index($2, "=") + 1) : "") ; argline[f[1]] = FNR; return }
      if (toupper($1) != "FROM") return
      for (i = 2; i <= NF && $i ~ /^--/; i++) ;
      img = $i
      if (toupper($(i + 1)) == "AS") stage[tolower($(i + 2))] = 1
      if (tolower(img) == "scratch" || (tolower(img) in stage)) return
      if (img ~ /^\$/) {
        n = img; gsub(/[${}]/, "", n)
        if (!(n in arg)) report("FROM " img ": no ARG " n)
        else if (!digest(arg[n])) report("FROM " img ": ARG " n " default not pinned by digest: " arg[n])
        return
      }
      if (!digest(img)) report("FROM not pinned by digest: " img)
    }
    function compose(   ref) {
      if (match($0, /^[ \t]*image:[ \t]*/)) {
        ref = strip(substr($0, RLENGTH + 1))
        if (!digest(ref)) report("image not pinned by digest: " ref)
      }
    }
    function envfile(   key, val) {
      if ($0 !~ /^[A-Za-z_][A-Za-z0-9_]*=/) return
      key = substr($0, 1, index($0, "=") - 1)
      val = strip(substr($0, index($0, "=") + 1))
      if (key ~ /IMAGE/ && val != "" && !digest(val)) report(key " not pinned by digest: " val)
    }
    kind == "workflow" { workflow() }
    kind == "dockerfile" { dockerfile() }
    kind == "compose" { compose() }
    kind == "env" { envfile() }
    { prev = $0 }
  ' "$file" || true)"
  if [ -n "$offenders" ]; then
    echo "$offenders"
    failed=1
  fi
done < <(git ls-files -z --cached --others --exclude-standard)

if [ "$failed" -ne 0 ]; then
  echo "check-pins: FAILED"
  exit 1
fi
echo "check-pins: $checked files OK"
