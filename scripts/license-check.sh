#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-only
# Copyright 2026 Artur Abalov
#
# Fails when a source file lacks the SPDX header expected for its
# directory or the copyright line. proto/ and agent/plugins/sdk/ are
# Apache-2.0; everything else is AGPL-3.0-only. CLAUDE.md, .claude/ and
# docs/ are not source and are skipped.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

HEAD_LINES=12
COPYRIGHT='Copyright 2026 Artur Abalov'

expected_license() {
  case "$1" in
    proto/* | agent/plugins/sdk/*) echo Apache-2.0 ;;
    *) echo AGPL-3.0-only ;;
  esac
}

is_source() {
  case "$1" in
    CLAUDE.md | .claude/* | docs/* | */node_modules/* | */dist/* | */build/*) return 1 ;;
    */LICENSE | LICENSE) return 1 ;;
    *.go | *.kt | *.kts | *.java | *.ts | *.tsx | *.js | *.mjs | *.cjs | *.proto | *.sh | *.sql) return 0 ;;
    Makefile | */Makefile | Dockerfile | */Dockerfile | *.Dockerfile) return 0 ;;
  esac
  return 1
}

failed=0
checked=0
while IFS= read -r -d '' file; do
  is_source "$file" || continue
  [ -f "$file" ] || continue
  checked=$((checked + 1))
  want="$(expected_license "$file")"
  header="$(head -n "$HEAD_LINES" "$file")"
  if ! grep -q "SPDX-License-Identifier: $want\$" <<<"$header"; then
    got="$(grep -o 'SPDX-License-Identifier: [^ ]*' <<<"$header" | head -1 || true)"
    echo "license-check: $file: want 'SPDX-License-Identifier: $want', found '${got:-nothing}'"
    failed=1
  fi
  if ! grep -q "$COPYRIGHT" <<<"$header"; then
    echo "license-check: $file: missing '$COPYRIGHT'"
    failed=1
  fi
done < <(git ls-files -z --cached --others --exclude-standard)

if [ "$failed" -ne 0 ]; then
  echo "license-check: FAILED"
  exit 1
fi
echo "license-check: $checked files OK"
