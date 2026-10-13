#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-only
# Copyright 2026 Artur Abalov
#
# Fails when a tracked text file tells to run the agent with "sudo -u"
# (rule A8, ADR 0050, Q5 Р4: mutating commands run as "sudo sard-agent ...").
# A line with "sudo -u" is allowed only when it, or the line directly above,
# carries "allow-sudo-u: <reason>" with a non-empty reason. History and
# requirements (docs/sessions, docs/adr, docs/specs) and manual test
# procedures (docs/qa) are skipped, as are this script and its test.
# Optional argument: the root directory to check (default: the repository).
set -euo pipefail

ROOT="${1:-$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)}"
cd "$ROOT"

is_checked() {
  case "$1" in
    docs/sessions/* | docs/adr/* | docs/specs/* | docs/qa/*) return 1 ;;
    scripts/check-sudo-u.sh | scripts/test-check-sudo-u.sh) return 1 ;;
  esac
  return 0
}

failed=0
checked=0
while IFS= read -r -d '' file; do
  is_checked "$file" || continue
  [ -f "$file" ] || continue
  checked=$((checked + 1))
  offenders="$(awk -v file="$file" '
    function allowed(s) { return s ~ /allow-sudo-u:[ \t]*[^ \t]/ }
    index($0, "sudo -u") > 0 && !allowed($0) && !allowed(prev) {
      printf "check-sudo-u: %s:%d: \"sudo -u\" without \"allow-sudo-u: <reason>\"\n", file, FNR
    }
    { prev = $0 }
  ' "$file" || true)"
  if [ -n "$offenders" ]; then
    echo "$offenders"
    failed=1
  fi
done < <(git ls-files -z --cached --others --exclude-standard)

if [ "$failed" -ne 0 ]; then
  echo "check-sudo-u: FAILED"
  exit 1
fi
echo "check-sudo-u: $checked files OK"
