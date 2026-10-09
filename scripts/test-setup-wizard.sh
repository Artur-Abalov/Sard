#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-only
# Copyright 2026 Artur Abalov
#
# Tests scripts/lib/setup-wizard.sh without a server: the setup code is found in
# a server log by the line of the F4a format (docs/specs/server/onboarding-setup.feature, Р1),
# the code of the last start wins, and the wizard calls go to the API in order
# (code, CA, administrator) against a fake curl.
#
#   scripts/test-setup-wizard.sh
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# shellcheck source=lib/setup-wizard.sh
source "$ROOT/scripts/lib/setup-wizard.sh"
failures=0
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

fail() {
  echo "FAIL: $*" >&2
  failures=$((failures + 1))
}

expect() { # expect NAME WANT GOT
  [ "$2" = "$3" ] || fail "$1: got '$3', want '$2'"
}

old='ABCD-EFGH-JKMN-PQRS-TVWX-YZ01-2345'
new='0123-4567-89AB-CDEF-GHJK-MNPQ-RSTV'

# 1. The line of one start.
log_one() {
  echo "2026-10-09T12:00:00Z  INFO 1 --- [main] d.s.s.o.SetupCodeAnnouncer : SARD SETUP CODE: $old valid until 2026-10-10T12:00:00Z"
  echo "server-1  | something else"
}
expect "one start" "$old" "$(sard_setup_code_from_log < <(log_one))"

# 2. The prefix of docker compose logs does not matter; the code of the last start wins.
log_two() {
  echo "server-1  | SARD SETUP CODE: $old valid until 2026-10-10T12:00:00Z"
  echo "server-1  | SARD SETUP CODE: $new valid until 2026-10-11T12:00:00Z"
}
expect "two starts" "$new" "$(sard_setup_code_from_log < <(log_two))"

# 3. No line, or a line that is not of the format (I, L, O, U are not in the alphabet): no code.
expect "no line" "" "$(sard_setup_code_from_log < <(echo "nothing here"))"
expect "bad alphabet" "" "$(sard_setup_code_from_log < <(echo "SARD SETUP CODE: ILOU-ILOU-ILOU-ILOU-ILOU-ILOU-ILOU valid until x"))"

# 4. The code is waited for: the logs command is run until it prints one, then gives up.
calls="$work/calls"
: >"$calls"
logs_late() {
  echo x >>"$calls"
  if [ "$(wc -l <"$calls")" -ge 3 ]; then log_one; fi
}
expect "late code" "$old" "$(SARD_WIZARD_POLL=0 SARD_WIZARD_TRIES=5 sard_setup_code logs_late)"
expect "polls until the code" "3" "$(wc -l <"$calls" | tr -d ' ')"
if SARD_WIZARD_POLL=0 SARD_WIZARD_TRIES=2 sard_setup_code echo >/dev/null 2>&1; then
  fail "a log without a code must fail"
fi

# 5. The wizard: code, CA, administrator, in this order, with the code as printed.
fakebin="$work/bin"
mkdir -p "$fakebin"
cat >"$fakebin/curl" <<'F'
#!/usr/bin/env bash
# Records the URL and the body; answers 204 on %{http_code}.
url="" body=""
args=("$@")
for ((i = 0; i < ${#args[@]}; i++)); do
  case "${args[i]}" in
    --data-binary | -d) body="${args[i + 1]}" ;;
    http*) url="${args[i]}" ;;
  esac
done
echo "$url $body" >>"$SARD_FAKE_CURL_LOG"
printf '%s' "${SARD_FAKE_CURL_CODE:-204}"
F
chmod +x "$fakebin/curl"
export SARD_FAKE_CURL_LOG="$work/curl.log"
: >"$SARD_FAKE_CURL_LOG"
PATH="$fakebin:$PATH" sard_complete_wizard http://h:8080 'pa"ss\word-1234' log_one >/dev/null
expect "wizard calls" \
  "http://h:8080/api/v1/onboarding/setup-session {\"code\":\"$old\"}
http://h:8080/api/v1/onboarding/ca 
http://h:8080/api/v1/onboarding/admin {\"password\":\"pa\\\"ss\\\\word-1234\"}" \
  "$(cat "$SARD_FAKE_CURL_LOG")"

# 5b. sard_curl can be replaced: the requests go through it, with the cookie jar asked for.
: >"$SARD_FAKE_CURL_LOG"
sard_curl() { echo "via-override $*" >>"$SARD_FAKE_CURL_LOG"; printf 204; }
SARD_WIZARD_COOKIES=/root/jar sard_complete_wizard http://h:8080 pw-123456789012 log_one >/dev/null
expect "override used for all three steps" "3" "$(grep -c '^via-override .* -b /root/jar -c /root/jar ' "$SARD_FAKE_CURL_LOG" | tr -d ' ')"
unset -f sard_curl
sard_curl() { curl "$@"; }

# 6. A refused step stops the wizard and says which.
: >"$SARD_FAKE_CURL_LOG"
if out="$(SARD_FAKE_CURL_CODE=401 PATH="$fakebin:$PATH" sard_complete_wizard http://h:8080 pw-123456789012 log_one 2>&1)"; then
  fail "a 401 on the code must fail"
fi
grep -q 'setup-session' <<<"$out" || fail "the failure does not name the step: $out"
expect "stops at the first refusal" "1" "$(wc -l <"$SARD_FAKE_CURL_LOG" | tr -d ' ')"

if [ "$failures" -gt 0 ]; then
  echo "test-setup-wizard: $failures failure(s)" >&2
  exit 1
fi
echo "test-setup-wizard: ok"
