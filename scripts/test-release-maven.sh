#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-only
# Copyright 2026 Artur Abalov
#
# Test of scripts/release-maven.sh without a network: curl on PATH is a stub
# that records what it would upload. A repository that lacks a library, holds
# another version or anything else uploads nothing; a complete one uploads
# every file of the three libraries and no maven-metadata.xml, with the token
# in no argument.
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

die() { echo "test-release-maven: FAIL: $*" >&2; exit 1; }

mkdir "$WORK/bin"
cat >"$WORK/bin/curl" <<'EOF'
#!/bin/sh
echo "$*" >>"$CURL_LOG"
EOF
chmod +x "$WORK/bin/curl"
TOKEN=s3cret-token

repo() { # dir, version
  local artifact
  for artifact in sard-server-core sard-proto-jvm sard-server-console; do
    mkdir -p "$1/dev/sard/$artifact/$2"
    for ext in jar pom module; do
      touch "$1/dev/sard/$artifact/$2/$artifact-$2.$ext" "$1/dev/sard/$artifact/$2/$artifact-$2.$ext.sha256"
    done
    touch "$1/dev/sard/$artifact/maven-metadata.xml"
  done
}

run() { # dir, version... → output, exit code in RC
  : >"$WORK/curl.log"
  RC=0
  OUT="$(PATH="$WORK/bin:$PATH" CURL_LOG="$WORK/curl.log" MAVEN_TOKEN="$TOKEN" \
    "$HERE/release-maven.sh" "$@" https://maven.pkg.github.com/o/r 2>&1)" || RC=$?
}

expect_refused() { # description, reason, arguments...
  local what="$1" reason="$2"
  shift 2
  run "$@"
  [ "$RC" -eq 1 ] || die "$what: exit $RC, want 1: $OUT"
  [ ! -s "$WORK/curl.log" ] || die "$what: uploaded something: $(cat "$WORK/curl.log")"
  grep -q -- "$reason" <<<"$OUT" || die "$what: reason not given: $OUT"
  echo "ok: $what"
}

repo "$WORK/missing" 1.2.0-beta.3
rm -r "$WORK/missing/dev/sard/sard-server-console"
expect_refused "a library is missing" "sard-server-console 1.2.0-beta.3 is missing" "$WORK/missing" v1.2.0-beta.3

repo "$WORK/nomodule" 1.2.0
rm "$WORK/nomodule/dev/sard/sard-proto-jvm/1.2.0/sard-proto-jvm-1.2.0.module"
expect_refused "a module file is missing" "sard-proto-jvm-1.2.0.module is missing" "$WORK/nomodule" v1.2.0

repo "$WORK/two" 1.2.0
repo "$WORK/two" 1.1.0
expect_refused "another version is there" "has other versions" "$WORK/two" v1.2.0

repo "$WORK/extra" 1.2.0
mkdir -p "$WORK/extra/org/other/1.0" && touch "$WORK/extra/org/other/1.0/other.jar"
expect_refused "a foreign artifact is there" "not a library of the release" "$WORK/extra" v1.2.0

repo "$WORK/novee" 1.2.0
expect_refused "the tag has no v" "starts with v" "$WORK/novee" 1.2.0

repo "$WORK/ok" 1.2.0-rc.1
run "$WORK/ok" v1.2.0-rc.1
[ "$RC" -eq 0 ] || die "a complete repository: exit $RC: $OUT"
[ "$(wc -l <"$WORK/curl.log")" -eq 18 ] || die "want 18 uploads, got: $(cat "$WORK/curl.log")"
grep -q "https://maven.pkg.github.com/o/r/dev/sard/sard-server-core/1.2.0-rc.1/sard-server-core-1.2.0-rc.1.jar" \
  "$WORK/curl.log" || die "the core jar was not uploaded: $(cat "$WORK/curl.log")"
grep -q "maven-metadata" "$WORK/curl.log" && die "maven-metadata.xml was uploaded"
grep -q "$TOKEN" "$WORK/curl.log" && die "the token is on the command line"
echo "ok: a complete repository uploads its 18 files"
