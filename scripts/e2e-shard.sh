#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-only
# Copyright 2026 Artur Abalov
#
# The e2e classes split over CI runners. Each class starts an installation of
# its own (SardEnvironment), so any split is valid; the heaviest class goes to
# the lightest shard (scripts/lib/lpt.awk) by the seconds per class in
# scripts/e2e-weights.tsv, a class without a weight weighs the mean.
#
#   scripts/e2e-shard.sh list                  every e2e class
#   scripts/e2e-shard.sh <i>/<n>               the classes of shard i of n
#   scripts/e2e-shard.sh missing <dir>         the classes without a JUnit report in <dir>; fails if any
#   scripts/e2e-shard.sh times [--tsv] <dir>   seconds per class from the reports, slowest first;
#                                              --tsv prints them as weights
#
# Exit codes: 0 done, 1 an empty shard or a class without a report, 2 usage.
# E2E_SRC and E2E_WEIGHTS replace the test sources and the weights (tests).
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SRC="${E2E_SRC:-$ROOT/test/e2e/src/test/kotlin}"
WEIGHTS="${E2E_WEIGHTS:-$ROOT/scripts/e2e-weights.tsv}"

usage() {
  sed -n '10,14s/^# \{0,1\}//p' "${BASH_SOURCE[0]}" >&2
  exit 2
}

# classes: <package>.<file name> of every *Test.kt, sorted.
classes() {
  local f
  find "$SRC" -name '*Test.kt' -type f | sort | while read -r f; do
    echo "$(sed -n 's/^package[[:space:]]\{1,\}\([A-Za-z0-9_.]*\).*/\1/p' "$f" | head -1).$(basename "$f" .kt)"
  done | sort
}

shard() {
  local i="${1%/*}" n="${1#*/}" out
  [[ "$1" == */* && "$i" =~ ^[0-9]+$ && "$n" =~ ^[0-9]+$ ]] && [ "$n" -ge 1 ] && [ "$i" -ge 1 ] && [ "$i" -le "$n" ] || usage
  [ -f "$WEIGHTS" ] || WEIGHTS=/dev/null
  out="$(classes | awk -f "$ROOT/scripts/lib/lpt.awk" -v i="$i" -v n="$n" -v wf="$WEIGHTS")"
  if [ -z "$out" ]; then
    echo "e2e-shard: shard $1 is empty" >&2
    exit 1
  fi
  echo "$out"
}

missing() {
  local c status=0
  [ -d "$1" ] || usage
  while read -r c; do
    if [ ! -f "$1/TEST-$c.xml" ]; then
      echo "$c"
      status=1
    fi
  done < <(classes)
  [ "$status" -eq 0 ] || echo "e2e-shard: the classes above have no report in $1" >&2
  return "$status"
}

times() {
  local tsv=false
  if [ "${1:-}" = --tsv ]; then tsv=true; shift; fi
  [ "$#" -eq 1 ] && [ -d "$1" ] || usage
  python3 -I - "$tsv" "$1" <<'EOF'
import glob, os, sys
import xml.etree.ElementTree as ET
tsv, folder = sys.argv[1] == "true", sys.argv[2]
rows = []
for path in glob.glob(os.path.join(folder, "TEST-*.xml")):
    suite = ET.parse(path).getroot()
    rows.append((float(suite.get("time", 0)), int(suite.get("tests", 0)), suite.get("name")))
rows.sort(key=lambda r: (-r[0], r[2]))
if tsv:
    print("# seconds<TAB>e2e class, from the JUnit reports of a CI run; refresh with scripts/e2e-shard.sh times --tsv")
    for seconds, _, name in rows:
        print(f"{round(seconds)}\t{name}")
else:
    for seconds, tests, name in rows:
        print(f"{seconds:8.1f} s  {tests:3d} tests  {name}")
    print(f"{sum(r[0] for r in rows):8.1f} s  total in {len(rows)} classes")
EOF
}

case "${1:-}" in
  list) classes ;;
  missing) [ "$#" -eq 2 ] || usage; missing "$2" ;;
  times) shift; times "$@" ;;
  *) [ "$#" -eq 1 ] || usage; shard "$1" ;;
esac
