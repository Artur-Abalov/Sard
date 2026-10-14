#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-only
# Copyright 2026 Artur Abalov
#
# Fails when a tracked text file tells to run something with "sudo -u"
# (also -iu, -Hu, --user; rule A8, ADR 0050, Q5 Р4: mutating commands run as
# "sudo sard-agent ..."). Such a line is allowed only when it, or the line
# directly above, carries "allow-sudo-u: <reason>" with a non-empty reason.
# Only history (docs/sessions, docs/adr) is skipped, as are this script and
# its test; specs and QA procedures must mark every legitimate occurrence.
# Optional argument: the root directory to check (default: the repository).
set -euo pipefail

ROOT="${1:-$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)}"
cd "$ROOT"

is_checked() {
  case "$1" in
    docs/sessions/* | docs/adr/*) return 1 ;;
    scripts/check-sudo-u.sh | scripts/test-check-sudo-u.sh) return 1 ;;
  esac
  return 0
}

# "sudo", optional other short options, then -u / -iu / -Hu / --user.
sudo_u_pattern='(^|[^A-Za-z0-9_-])sudo[ \t]+(-[A-Za-z]+[ \t]+)*(-[A-Za-z]*u([ \t=]|$)|--user([ \t=]|$))'

failed=0
checked=0
while IFS= read -r -d '' file; do
  is_checked "$file" || continue
  [ -f "$file" ] || continue
  checked=$((checked + 1))
  offenders="$(awk -v file="$file" -v SUDO_U="$sudo_u_pattern" '
    function allowed(s) { return s ~ /allow-sudo-u:[ \t]*[^ \t]/ }
    $0 ~ SUDO_U && !allowed($0) && !allowed(prev) {
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
