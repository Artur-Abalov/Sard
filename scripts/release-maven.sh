#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-only
# Copyright 2026 Artur Abalov
#
# Uploads the libraries an enterprise build compiles against (CORE1, ADR 0070)
# to a Maven repository, as the server-jar stage of deploy/server/Dockerfile
# built them next to the jar (build once, ADR 0045; release.yml, job maven):
#
#   release-maven.sh <maven dir> <version> <repository url>
#
# <maven dir> is a Maven file repository; <version> the tag (v1.2.0 or
# v1.2.0-beta.3), whose Maven version is the tag without its "v". Exactly
# sard-server-core, sard-proto-jvm and sard-server-console of that version,
# each with its jar, POM and Gradle module, or nothing is uploaded. The
# repository writes its own maven-metadata.xml, so those are not sent. The
# token comes in MAVEN_TOKEN and reaches curl in a netrc file, never on a
# command line.
set -euo pipefail

die() { echo "release-maven: $*" >&2; exit 1; }

[ "$#" -eq 3 ] || die "usage: release-maven.sh <maven dir> <version> <repository url>"
dir="$1" tag="$2" url="${3%/}"
[ -n "${MAVEN_TOKEN:-}" ] || die "MAVEN_TOKEN is not set"
[ -d "$dir" ] || die "$dir: no such directory"
[[ "$tag" == v* ]] || die "$tag: a release tag starts with v"
version="${tag#v}"

artifacts=(sard-server-core sard-proto-jvm sard-server-console)
files=()
for artifact in "${artifacts[@]}"; do
  base="$dir/dev/sard/$artifact"
  [ -d "$base/$version" ] || die "$artifact $version is missing"
  others="$(find "$base" -mindepth 1 -maxdepth 1 -type d ! -name "$version" -printf '%f ')"
  [ -z "$others" ] || die "$artifact has other versions: $others"
  for ext in jar pom module; do
    [ -f "$base/$version/$artifact-$version.$ext" ] || die "$artifact-$version.$ext is missing"
  done
  while IFS= read -r file; do files+=("$file"); done < <(find "$base/$version" -type f | sort)
done
unexpected="$(find "$dir" -type f ! -path "$dir/dev/sard/sard-server-core/*" ! -path "$dir/dev/sard/sard-proto-jvm/*" \
  ! -path "$dir/dev/sard/sard-server-console/*" -printf '%P ')"
[ -z "$unexpected" ] || die "not a library of the release: $unexpected"

host="$(sed -E 's#^[a-z]+://([^/:]+).*#\1#' <<<"$url")"
netrc="$(mktemp)"
trap 'rm -f "$netrc"' EXIT
chmod 600 "$netrc"
printf 'machine %s login token password %s\n' "$host" "$MAVEN_TOKEN" >"$netrc"

for file in "${files[@]}"; do
  path="${file#"$dir"/}"
  curl --fail --silent --show-error --netrc-file "$netrc" --upload-file "$file" "$url/$path"
  echo "uploaded $path"
done
