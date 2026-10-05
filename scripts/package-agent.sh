#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-only
# Copyright 2026 Artur Abalov
#
# Packages sard-agent with the pinned restic for linux/amd64 and linux/arm64
# (docs/adr/0018-agent-packaging.md) into dist/:
#
#   sard-agent_<version>_linux_<arch>.tar.gz   binaries, licenses, config, unit
#   sard-agent_<version>_<arch>.deb / .rpm     /usr/lib/sard, systemd unit
#   manifest.json                              version, restic, protocol, artifacts
#   SHA256SUMS                                 over all of the above
#
#   scripts/package-agent.sh              amd64 and arm64
#   scripts/package-agent.sh arm64        one architecture
#   VERSION=v1.2.3 scripts/package-agent.sh
#   DIST=/tmp/out scripts/package-agent.sh amd64   another output directory
#   GO_TAGS=e2e DIST=... scripts/package-agent.sh  e2e stand build (make e2e-images
#                                                   only): links the stand's plugins
#
# Every package carries Sard's license (AGPL-3.0), restic's (BSD-2) and the
# license texts of all Go modules compiled into sard-agent.
#
# The output is reproducible (docs/adr/0040-agent-release.md):
# every timestamp is SOURCE_DATE_EPOCH, the commit time unless set, so two
# builds of one commit produce the same SHA256SUMS.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BIN="$ROOT/.bin"
DIST="${DIST:-$ROOT/dist}"
VERSION="${VERSION:-$(git -C "$ROOT" describe --tags --always --dirty 2>/dev/null || echo dev)}"
RESTIC_VERSION="$(sed -n 's/^version=//p' "$ROOT/agent/internal/restic/restic-version")"
COMMIT="$(git -C "$ROOT" rev-parse HEAD 2>/dev/null || echo unknown)"
SOURCE_DATE_EPOCH="${SOURCE_DATE_EPOCH:-$(git -C "$ROOT" log -1 --format=%ct 2>/dev/null || echo 0)}"
export SOURCE_DATE_EPOCH
PROTOCOL_VERSION="$(sed -n 's/^const ProtocolVersion = \([0-9][0-9]*\)$/\1/p' "$ROOT/agent/internal/app/app.go")"
# Go build tags of sard-agent: empty for a release; "e2e" adds the e2e
# stand's plugins (agent/plugins/stand_e2e.go) and is never shipped.
GO_TAGS="${GO_TAGS:-}"

die() { echo "package-agent: $*" >&2; exit 1; }

# pkg_version turns a git description into a deb/rpm version: v1.2.3 → 1.2.3,
# anything else → 0.0.0~dev.<description>, which sorts below any release.
pkg_version() {
  if [[ "$VERSION" =~ ^v?([0-9]+\.[0-9]+\.[0-9]+)$ ]]; then
    echo "${BASH_REMATCH[1]}"
  else
    echo "0.0.0~dev.${VERSION//[^A-Za-z0-9.]/.}"
  fi
}

# third_party_licenses prints the license files of every non-Sard module
# linked into sard-agent for GOOS=linux GOARCH=$1; a module without one fails.
third_party_licenses() {
  local arch="$1" module dir files
  (cd "$ROOT/agent" && GOOS=linux GOARCH="$arch" go list -deps -tags "$GO_TAGS" \
    -f '{{if not .Standard}}{{with .Module}}{{.Path}}@{{.Version}} {{.Dir}}{{end}}{{end}}' ./cmd/sard-agent) |
    sort -u | grep -v '^github.com/Artur-Abalov/sard' |
    while read -r module dir; do
      files="$(find "$dir" -maxdepth 1 -type f -iregex '.*/\(licen[cs]e\|copying\|notice\)\(\.[a-z]*\)?' | sort)"
      [ -n "$files" ] || die "$module: no license file in $dir"
      for f in $files; do
        printf '================================================================================\n'
        printf '%s — %s\n' "$module" "$(basename "$f")"
        printf '================================================================================\n\n'
        cat "$f"
        printf '\n'
      done
    done
}

notice() {
  cat <<EOF
sard-agent $VERSION (commit $COMMIT)
Copyright 2026 Artur Abalov
License: AGPL-3.0-only (LICENSE). Source: https://github.com/Artur-Abalov/sard

This package includes:
- restic $RESTIC_VERSION, the official release binary, BSD-2-Clause
  (LICENSE.restic). Source: https://github.com/restic/restic/tree/v$RESTIC_VERSION
- Go modules compiled into sard-agent, under their own licenses
  (THIRD_PARTY_LICENSES).
EOF
}

stage() {
  local arch="$1" dir="$2"
  mkdir -p "$dir"
  (cd "$ROOT/agent" && CGO_ENABLED=0 GOOS=linux GOARCH="$arch" go build -trimpath -tags "$GO_TAGS" \
    -ldflags "-s -w -X main.version=$VERSION" -o "$dir/sard-agent" ./cmd/sard-agent)
  install -m 0755 "$BIN/restic/$RESTIC_VERSION/linux_$arch/restic" "$dir/restic"
  install -m 0644 "$ROOT/LICENSE" "$dir/LICENSE"
  install -m 0644 "$ROOT/third_party/restic/LICENSE" "$dir/LICENSE.restic"
  third_party_licenses "$arch" >"$dir/THIRD_PARTY_LICENSES"
  notice >"$dir/NOTICE"
  install -m 0644 "$ROOT/deploy/agent/agent.example.yaml" "$dir/agent.example.yaml"
  install -m 0644 "$ROOT/deploy/agent/sard-agent.service" "$dir/sard-agent.service"
  find "$dir" -exec touch -h -d "@$SOURCE_DATE_EPOCH" {} +
}

# Files every package must carry; paths relative to the tarball directory
# and to / in deb/rpm.
TAR_FILES=(sard-agent restic LICENSE LICENSE.restic THIRD_PARTY_LICENSES NOTICE agent.example.yaml sard-agent.service)
PKG_FILES=(
  /usr/lib/sard/sard-agent /usr/lib/sard/restic /usr/bin/sard-agent
  /usr/lib/systemd/system/sard-agent.service /etc/sard/agent.example.yaml
  /usr/share/doc/sard-agent/LICENSE /usr/share/doc/sard-agent/LICENSE.restic
  /usr/share/doc/sard-agent/THIRD_PARTY_LICENSES /usr/share/doc/sard-agent/NOTICE
)

# require fails unless every wanted path is in the listing on stdin.
require() {
  local what="$1" listing path
  shift
  listing="$(cat)"
  for path in "$@"; do
    grep -qx -- "$path" <<<"$listing" || die "$what: $path missing"
  done
}

# forbid fails if any of the given paths is in the listing on stdin.
forbid() {
  local what="$1" listing path
  shift
  listing="$(cat)"
  for path in "$@"; do
    ! grep -qx -- "$path" <<<"$listing" || die "$what: $path must not be packaged"
  done
}

# The cache directory is made by postinstall (owner and mode reset on every
# install, contents kept, left by removal), so no package or archive owns it.
CACHE_DIR=/var/cache/sard/restic
POSTINSTALL_LINES=("mkdir -p $CACHE_DIR" 'chown sard-agent:"$(id -gn sard-agent)" '"$CACHE_DIR" "chmod 0700 $CACHE_DIR")

# check_cache_dir verifies the scriptlet text on stdin creates the cache dir.
check_cache_dir() {
  local what="$1" script line
  script="$(cat)"
  for line in "${POSTINSTALL_LINES[@]}"; do
    grep -qF -- "$line" <<<"$script" || die "$what: post-install script lacks: $line"
  done
}

# build_tags prints the -tags sard-agent was built with, from its build info.
build_tags() {
  go version -m "$1" | awk '$1 == "build" && $2 ~ /^-tags=/ { sub(/^-tags=/, "", $2); print $2 }'
}

# check_tags fails unless the binary was built with exactly $GO_TAGS, and a
# release (no GO_TAGS) links no e2e stand plugin package.
check_tags() {
  local bin="$1" tags
  tags="$(build_tags "$bin")"
  [ "$tags" = "$GO_TAGS" ] || die "$bin: built with tags '$tags', want '$GO_TAGS'"
  [ -n "$GO_TAGS" ] || ! grep -qa 'sard/agent/plugins/e2eslow' "$bin" || die "$bin: a release links plugins/e2eslow"
}

verify() {
  local arch="$1" name="$2" deb rpm
  check_tags "$DIST/stage/$name/sard-agent"
  tar -tzf "$DIST/$name.tar.gz" | sed "s|^$name/||" | require "$name.tar.gz" "${TAR_FILES[@]}"
  ! tar -tzf "$DIST/$name.tar.gz" | grep -q 'var/cache' || die "$name.tar.gz: var/cache must not be in the archive"
  grep -qx 'CacheDirectoryMode=0700' "$ROOT/deploy/agent/sard-agent.service" || die "sard-agent.service: CacheDirectoryMode=0700 missing"
  deb="$(ls "$DIST"/sard-agent_*_"$arch".deb)"
  dpkg-deb -c "$deb" | awk '{print $6}' | sed 's|^\.||' | require "$(basename "$deb")" "${PKG_FILES[@]}"
  dpkg-deb -c "$deb" | awk '{print $6}' | sed 's|^\.||; s|/$||' | forbid "$(basename "$deb")" /var/cache /var/cache/sard "$CACHE_DIR"
  dpkg-deb --ctrl-tarfile "$deb" | tar -xO ./postinst | check_cache_dir "$(basename "$deb")"
  rpm="$(ls "$DIST"/sard-agent-*."$( [ "$arch" = amd64 ] && echo x86_64 || echo aarch64)".rpm)"
  if command -v rpm >/dev/null; then
    rpm -qlp "$rpm" 2>/dev/null | require "$(basename "$rpm")" "${PKG_FILES[@]}"
    rpm -qlp "$rpm" 2>/dev/null | forbid "$(basename "$rpm")" /var/cache /var/cache/sard "$CACHE_DIR"
    rpm -qp --scripts "$rpm" 2>/dev/null | check_cache_dir "$(basename "$rpm")"
  else
    [ -z "${REQUIRE_RPM:-}" ] || die "rpm tool required (REQUIRE_RPM set)"
    echo "package-agent: $(basename "$rpm") contents not checked (no rpm tool)"
  fi
}

package_arch() {
  local arch="$1" name dir
  name="sard-agent_${VERSION}_linux_${arch}"
  dir="$DIST/stage/$name"
  "$ROOT/scripts/fetch-restic.sh" "$arch"
  stage "$arch" "$dir"
  tar -C "$DIST/stage" --owner=0 --group=0 --numeric-owner --sort=name \
    --mtime="@$SOURCE_DATE_EPOCH" --format=gnu -cf - "$name" | gzip -n -9 >"$DIST/$name.tar.gz"
  # nfpm does not expand variables in file paths: render the config.
  sed -e "s|\${STAGE}|$dir|g" -e "s|\${ARCH}|$arch|g" -e "s|\${ROOT}|$ROOT|g" \
    -e "s|\${PKG_VERSION}|$(pkg_version)|g" "$ROOT/deploy/agent/nfpm.yaml" >"$dir.nfpm.yaml"
  for format in deb rpm; do
    "$BIN/nfpm" package --config "$dir.nfpm.yaml" --packager "$format" --target "$DIST/" >/dev/null
  done
  verify "$arch" "$name"
  echo "package-agent: $arch done, contents checked"
}

[ -x "$BIN/nfpm" ] || make -C "$ROOT" tools
rm -rf "$DIST"
mkdir -p "$DIST/stage"
[ "$#" -gt 0 ] || set -- amd64 arm64
for arch in "$@"; do
  case "$arch" in
    amd64 | arm64) package_arch "$arch" ;;
    *) die "unsupported architecture $arch (amd64, arm64)" ;;
  esac
done
# manifest prints manifest.json, the machine-readable release description the
# server hands out (docs/adr/0040-agent-release.md).
manifest() {
  local f sep="" arch format
  printf '{\n  "schema": 1,\n  "version": "%s",\n  "package_version": "%s",\n' "$VERSION" "$(pkg_version)"
  printf '  "commit": "%s",\n  "restic_version": "%s",\n  "protocol_version": %s,\n  "artifacts": [' \
    "$COMMIT" "$RESTIC_VERSION" "$PROTOCOL_VERSION"
  for f in *.tar.gz *.deb *.rpm; do
    case "$f" in
      *.tar.gz) format=tar.gz ;;
      *.deb) format=deb ;;
      *.rpm) format=rpm ;;
    esac
    case "$f" in
      *amd64* | *x86_64*) arch=amd64 ;;
      *arm64* | *aarch64*) arch=arm64 ;;
    esac
    printf '%s\n    {"file": "%s", "os": "linux", "arch": "%s", "format": "%s", "size": %s, "sha256": "%s"}' \
      "$sep" "$f" "$arch" "$format" "$(stat -c %s "$f")" "$(sha256sum "$f" | cut -d' ' -f1)"
    sep=","
  done
  printf '\n  ]\n}\n'
}

[ -n "$PROTOCOL_VERSION" ] || die "ProtocolVersion not found in agent/internal/app/app.go"
(cd "$DIST" && manifest >manifest.json && sha256sum -- *.tar.gz *.deb *.rpm manifest.json >SHA256SUMS)
ls -1 "$DIST"
