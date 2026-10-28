# SPDX-License-Identifier: AGPL-3.0-only
# Copyright 2026 Artur Abalov
#
# The tests that cover a Go mutant: the changed lines come from `diff original
# mutant` (stdin, normal format), the coverage from the file idx, one covered
# block per line, "<test> <path>:<line>.<col>,<line>.<col> <statements> <count>"
# (a cover profile line prefixed by the test that ran alone). Prints the tests
# whose blocks with a count above 0 in the file named file (by its base name)
# contain a changed line, sorted, one per line. An insertion counts the lines
# on both sides of it. Used by scripts/mutate-exec.sh
# (docs/adr/0057-go-mutation-exec.md).
#
#   diff a.go mutant.go | awk -f scripts/lib/covering-tests.awk -v idx=index -v file=a.go
/^[0-9]+(,[0-9]+)?[acd][0-9]+(,[0-9]+)?$/ {
  split($0, h, /[acd]/)
  split(h[1], r, ",")
  lo[++n] = r[1] + 0
  hi[n] = (2 in r) ? r[2] + 0 : r[1] + 0
  if ($0 ~ /a/) hi[n] = lo[n] + 1
}
END {
  while ((getline line < idx) > 0) {
    if (split(line, x, " ") != 4 || x[4] + 0 <= 0) continue
    if (!match(x[2], /:[0-9]+\.[0-9]+,[0-9]+\.[0-9]+$/)) continue
    path = substr(x[2], 1, RSTART - 1)
    sub(/.*\//, "", path)
    if (path != file) continue
    split(substr(x[2], RSTART + 1), b, /[.,]/)
    for (i = 1; i <= n; i++)
      if (b[1] + 0 <= hi[i] && b[3] + 0 >= lo[i]) { chosen[x[1]] = 1; break }
  }
  m = 0
  for (t in chosen) names[++m] = t
  for (i = 2; i <= m; i++) {
    t = names[i]
    for (j = i - 1; j >= 1 && names[j] > t; j--) names[j + 1] = names[j]
    names[j + 1] = t
  }
  for (i = 1; i <= m; i++) print names[i]
}
