#!/usr/bin/env bash
# Auth pentest checklist, end to end: build the server, confirm it refuses
# unsafe configurations, boot it the way an internet-facing deploy runs
# (dev mode off, a real secret, https origins so cookies are Secure, SMTP
# delivery), run the authprobe checklist against it, and confirm the
# server log carries no sign-in link.
#
#   CONVERGE_TEST_DATABASE_URL=postgres://... tools/authprobe/run.sh
#
# The probe's accounts (probe-*@probe.test) stay in that database.
set -euo pipefail
cd "$(dirname "$0")/../.."

DB="${CONVERGE_TEST_DATABASE_URL:?set CONVERGE_TEST_DATABASE_URL}"
API_ADDR="${AUTHPROBE_API_ADDR:-127.0.0.1:3101}"
METRICS_ADDR="${AUTHPROBE_METRICS_ADDR:-127.0.0.1:3102}"
SMTP_ADDR="${AUTHPROBE_SMTP_ADDR:-127.0.0.1:2526}"
WEB_ORIGIN="https://app.probe.test"

WORK="$(mktemp -d)"
SERVER_PID=""
cleanup() {
  if [ -n "$SERVER_PID" ]; then kill "$SERVER_PID" 2>/dev/null || true; wait "$SERVER_PID" 2>/dev/null || true; fi
  rm -rf "$WORK"
}
trap cleanup EXIT

go build -o "$WORK/converge" ./cmd/converge
go build -o "$WORK/authprobe" ./tools/authprobe

fail=0

# refuses NAME ENV...: the server must exit non-zero at startup.
refuses() {
  local name="$1"; shift
  env -i PATH="$PATH" HOME="$HOME" CONVERGE_DATABASE_URL="$DB" CONVERGE_HTTP_ADDR="$API_ADDR" \
    CONVERGE_RUNTIME=false "$@" "$WORK/converge" >"$WORK/refuse.log" 2>&1 &
  local pid=$! i
  for i in $(seq 50); do
    if ! kill -0 "$pid" 2>/dev/null; then
      if wait "$pid"; then
        echo "FAIL  CFG       $name: exited 0"; fail=1
      else
        echo "PASS  CFG       $name"
      fi
      return
    fi
    sleep 0.1
  done
  kill "$pid" 2>/dev/null || true; wait "$pid" 2>/dev/null || true
  echo "FAIL  CFG       $name: still serving after 5 s"; fail=1
}

refuses "refuses to serve without CONVERGE_SESSION_SECRET"
refuses "refuses the published development secret" \
  CONVERGE_SESSION_SECRET=converge-dev-session-secret-do-not-use-in-prod
refuses "refuses a short secret" CONVERGE_SESSION_SECRET=too-short
refuses "refuses dev mode on a public URL" \
  CONVERGE_DEV_MODE=true CONVERGE_PUBLIC_URL=https://api.example.com CONVERGE_WEB_ORIGIN=https://app.example.com
refuses "refuses a short GitHub webhook secret" \
  CONVERGE_SESSION_SECRET="$(openssl rand -hex 32)" CONVERGE_GITHUB_REPOS=probe/app CONVERGE_GITHUB_WEBHOOK_SECRET=too-short

# The poller reads only probe/app, which no link in the database names:
# the run makes no GitHub request.
WEBHOOK_SECRET="$(openssl rand -hex 32)"

env -i PATH="$PATH" HOME="$HOME" \
  CONVERGE_DATABASE_URL="$DB" \
  CONVERGE_HTTP_ADDR="$API_ADDR" \
  CONVERGE_METRICS_ADDR="$METRICS_ADDR" \
  CONVERGE_SESSION_SECRET="$(openssl rand -hex 32)" \
  CONVERGE_PUBLIC_URL="https://api.probe.test" \
  CONVERGE_WEB_ORIGIN="$WEB_ORIGIN" \
  CONVERGE_SMTP_HOST="${SMTP_ADDR%:*}" \
  CONVERGE_SMTP_PORT="${SMTP_ADDR##*:}" \
  CONVERGE_SMTP_TLS=off \
  CONVERGE_GITHUB_REPOS=probe/app \
  CONVERGE_GITHUB_WEBHOOK_SECRET="$WEBHOOK_SECRET" \
  CONVERGE_RUNTIME=false \
  CONVERGE_LOG_LEVEL=debug \
  "$WORK/converge" >"$WORK/server.log" 2>&1 &
SERVER_PID=$!

for _ in $(seq 100); do
  curl -fsS "http://$API_ADDR/healthz" >/dev/null 2>&1 && break
  if ! kill -0 "$SERVER_PID" 2>/dev/null; then cat "$WORK/server.log"; exit 1; fi
  sleep 0.1
done

"$WORK/authprobe" -api "http://$API_ADDR" -web-origin "$WEB_ORIGIN" -smtp "$SMTP_ADDR" -secure-cookies \
  -github-webhook-secret "$WEBHOOK_SECRET" || fail=1

# The server logged every request at debug level: no sign-in link or code,
# no agent API token and no webhook secret may be in it.
SECRET_RE='auth/verify\?preAuthSessionId=|"(linkCode|userInputCode)"|conv_agent_[A-Za-z0-9_-]{20,}'
if grep -Eq "$SECRET_RE" "$WORK/server.log" || grep -qF "$WEBHOOK_SECRET" "$WORK/server.log"; then
  echo "FAIL  LOG       sign-in links, codes, API tokens or the webhook secret appear in the server log:"
  grep -E "$SECRET_RE" "$WORK/server.log" | head -3
  fail=1
else
  echo "PASS  LOG       no sign-in link, code, API token or webhook secret in the server log ($(wc -l <"$WORK/server.log" | tr -d ' ') lines at debug)"
fi

if [ "$fail" -ne 0 ]; then
  echo
  echo "--- server log (last 30 lines)"
  tail -30 "$WORK/server.log"
fi
exit "$fail"
