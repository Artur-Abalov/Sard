# SPDX-License-Identifier: AGPL-3.0-only
# Copyright 2026 Artur Abalov
#
# Longest processing time first: the items (stdin, one per line) of shard i of
# n. The heaviest item goes to the lightest shard (ties: lower shard, then
# name), by the weights in the file wf ("<seconds>\t<item>" per line, # starts
# a comment); an item without a weight weighs the mean. Used by
# scripts/gate.sh (Go mutation shards) and scripts/e2e-shard.sh (e2e classes).
#
#   awk -f scripts/lib/lpt.awk -v i=1 -v n=3 -v wf=weights.tsv
BEGIN {
  FS = "\t"
  while ((getline line < wf) > 0) if (line !~ /^#/ && split(line, a, "\t") == 2) { w[a[2]] = a[1]; sum += a[1]; cnt++ }
  mean = cnt ? sum / cnt : 1
}
{ f[NR] = $0; c[NR] = ($0 in w) ? w[$0] : mean }
END {
  for (x = 1; x <= NR; x++) for (y = x + 1; y <= NR; y++)
    if (c[y] > c[x] || (c[y] == c[x] && f[y] < f[x])) { t = f[x]; f[x] = f[y]; f[y] = t; t = c[x]; c[x] = c[y]; c[y] = t }
  for (x = 1; x <= NR; x++) {
    s = 1
    for (k = 2; k <= n; k++) if (load[k] < load[s]) s = k
    load[s] += c[x]
    if (s == i) print f[x]
  }
}
