#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-only
# Copyright 2026 Artur Abalov
#
# The Gradle build refuses what it did not pin (SC1): on a copy of the working
# tree, with the Gradle cache of this machine, :proto-jvm resolves
#   - as committed: passes;
#   - with the sha256 of protobuf-java forged in verification-metadata.xml:
#     fails with "Dependency verification failed";
#   - with protobuf-java locked to another version in its gradle.lockfile:
#     fails, the version the build asks for is not the locked one ("Did not
#     resolve '<locked>' which has been forced / substituted").
# The copy keeps the repository's own files untouched whatever happens.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

die() { echo "test-gradle-verification: FAIL: $*" >&2; exit 1; }
say() { echo "test-gradle-verification: $*"; }

readonly COMPONENT='name="protobuf-java" version="'

fresh_copy() {
  rm -rf "$WORK/tree"
  mkdir "$WORK/tree"
  (cd "$ROOT" && git ls-files -z --cached --others --exclude-standard | xargs -0 cp --parents -t "$WORK/tree")
}

# resolve: resolves and compiles :proto-jvm in the copy; its output in $WORK/out.
resolve() {
  (cd "$WORK/tree" && ./gradlew --no-daemon --quiet :proto-jvm:compileKotlin) >"$WORK/out" 2>&1
}

fresh_copy
resolve || { cat "$WORK/out" >&2; die "the committed tree does not build"; }
say "ok: as committed"

fresh_copy
meta="$WORK/tree/gradle/verification-metadata.xml"
grep -q "$COMPONENT" "$meta" || die "protobuf-java is not in verification-metadata.xml"
# The first sha256 after the component: its jar.
awk -v c="$COMPONENT" 'index($0, c) { inside = 1 }
  inside && /<sha256 value="/ { sub(/value="[0-9a-f]+"/, "value=\"" sprintf("%064d", 0) "\""); inside = 0 }
  { print }' "$meta" >"$meta.new"
mv "$meta.new" "$meta"
if resolve; then die "a forged checksum was accepted"; fi
grep -q "Dependency verification failed" "$WORK/out" || { cat "$WORK/out" >&2; die "forged checksum: another failure"; }
say "ok: a forged checksum fails the build"

fresh_copy
lock="$WORK/tree/proto/jvm/gradle.lockfile"
grep -q '^com.google.protobuf:protobuf-java:' "$lock" || die "protobuf-java is not in proto/jvm/gradle.lockfile"
sed -i 's/^\(com.google.protobuf:protobuf-java:\)[^=]*=/\13.0.0=/' "$lock"
if resolve; then die "a version other than the locked one was accepted"; fi
grep -q "Did not resolve 'com.google.protobuf:protobuf-java:3.0.0'" "$WORK/out" || { cat "$WORK/out" >&2; die "changed lock: another failure"; }
say "ok: a version other than the locked one fails the build"

echo "test-gradle-verification: OK"
