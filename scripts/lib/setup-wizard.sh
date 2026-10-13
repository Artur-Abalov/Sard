# SPDX-License-Identifier: AGPL-3.0-only
# Copyright 2026 Artur Abalov
#
# The first start by code (F4a), for scripts: finds the setup code in the log of
# sard-server and goes through the wizard over the REST API. Source it:
#
#   source scripts/lib/setup-wizard.sh
#   sard_complete_wizard http://localhost:8080 'a-password-of-12-chars' docker compose logs server
#
# The line the server prints (docs/specs/server/onboarding-setup.feature, Р1):
#   SARD SETUP CODE: XXXX-XXXX-XXXX-XXXX-XXXX-XXXX-XXXX valid until <instant>
# The code of the last start wins; the logs command may be any command that prints
# the log (docker compose logs server, docker logs <container>). Needs curl.
#
# SARD_WIZARD_TRIES (default 60) and SARD_WIZARD_POLL (seconds, default 2) bound the
# wait for the line; SARD_WIZARD_COOKIES names a file to keep the cookie jar in, so the
# caller is signed in afterwards (the admin step signs the owner in).
#
# The requests go through sard_curl, which is plain curl; a script whose server is
# reachable only from inside a container redefines it (sard_curl() { docker exec -i ... curl "$@"; };
# -i is required: the bodies arrive on stdin, not on the command line) and then names
# a cookie jar path inside that container in SARD_WIZARD_COOKIES.

sard_curl() { curl "$@"; }

# Reads a log from stdin; prints the code of its last SARD SETUP CODE line, or nothing.
sard_setup_code_from_log() {
  { grep -oE 'SARD SETUP CODE: [0-9A-HJKMNP-TV-Z]{4}(-[0-9A-HJKMNP-TV-Z]{4}){6} valid until' || true; } |
    tail -n 1 | awk 'NF { print $4 }'
}

# sard_setup_code LOGS_CMD...: waits until the log has a code line, prints the code.
sard_setup_code() {
  local tries="${SARD_WIZARD_TRIES:-60}" poll="${SARD_WIZARD_POLL:-2}" code i
  for ((i = 0; i < tries; i++)); do
    code="$("$@" 2>&1 | sard_setup_code_from_log || true)"
    if [ -n "$code" ]; then
      printf '%s\n' "$code"
      return 0
    fi
    sleep "$poll"
  done
  echo "setup-wizard: no SARD SETUP CODE line in the server log after $tries tries ($*)" >&2
  return 1
}

# A string as a JSON string body: backslash and double quote escaped.
sard_json_string() {
  local s="$1"
  s="${s//\\/\\\\}"
  s="${s//\"/\\\"}"
  printf '"%s"' "$s"
}

# sard_wizard_step NAME URL [BODY]: POST with the cookie jar; 204 is the only good answer.
sard_wizard_step() {
  local name="$1" url="$2" body="${3:-}" status
  local args=(-sS -o /dev/null -w '%{http_code}' -X POST -H 'Content-Type: application/json'
    -b "$SARD_WIZARD_JAR" -c "$SARD_WIZARD_JAR")
  # The body (the code, the password) goes by stdin: argv is visible in ps and /proc.
  if [ -n "$body" ]; then
    args+=(--data-binary @-)
    status="$(sard_curl "${args[@]}" "$url" <<<"$body")"
  else
    status="$(sard_curl "${args[@]}" "$url" </dev/null)"
  fi
  if [ "$status" != 204 ]; then
    echo "setup-wizard: step $name answered $status, want 204 ($url)" >&2
    return 1
  fi
}

# sard_complete_wizard HTTP_BASE PASSWORD LOGS_CMD...: code, CA, administrator.
sard_complete_wizard() {
  local base="$1" password="$2" code jar_is_temp=0
  shift 2
  code="$(sard_setup_code "$@")" || return 1
  if [ -z "${SARD_WIZARD_COOKIES:-}" ]; then
    SARD_WIZARD_JAR="$(mktemp)"
    jar_is_temp=1
  else
    SARD_WIZARD_JAR="$SARD_WIZARD_COOKIES"
  fi
  local rc=0
  {
    sard_wizard_step setup-session "$base/api/v1/onboarding/setup-session" "{\"code\":\"$code\"}" &&
      sard_wizard_step ca "$base/api/v1/onboarding/ca" &&
      sard_wizard_step admin "$base/api/v1/onboarding/admin" "{\"password\":$(sard_json_string "$password")}"
  } || rc=$?
  [ "$jar_is_temp" = 0 ] || rm -f "$SARD_WIZARD_JAR"
  return "$rc"
}
