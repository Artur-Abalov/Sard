#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-only
# Copyright 2026 Artur Abalov
#
# Per-file cost of Go mutation testing, in seconds, from shard logs:
#
#   ./scripts/mutation-weights.sh build/mutation/agent/*.log > scripts/mutation-weights/agent.tsv
#
# A shard log has a "mutate-exec: START <epoch ms> <mutant>" line per mutant
# (scripts/mutate-exec.sh). A mutant costs the time from its START to the next
# START in the same log; the last one of a log is not counted. Lines of the
# same file from several logs add up. scripts/gate.sh shards files by these
# weights (docs/adr/0057-go-mutation-exec.md).
set -euo pipefail

[ "$#" -gt 0 ] || { echo "usage: $0 <shard log>..." >&2; exit 2; }
echo "# seconds<TAB>file of the module: Go mutation cost per file, from $0"
for log in "$@"; do
  sed -n 's#^mutate-exec: START \([0-9]*\) .*/go-mutesting-[0-9]*/\(.*\)\.[0-9]*$#\1 \2#p' "$log" |
    awk 'prev != "" { cost[prev] += ($1 - start) / 1000 } { start = $1; prev = $2 } END { for (f in cost) printf "%s\t%s\n", cost[f], f }'
done | awk -F'\t' '{ s[$2] += $1 } END { for (f in s) printf "%d\t%s\n", s[f] + 0.5, f }' | sort -t$'\t' -k2
