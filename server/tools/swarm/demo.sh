#!/usr/bin/env bash
# Demo swarm: create three agents, assign them the open unassigned
# issues, and run the runners so you can watch the swarm work in the
# web UI (localhost:3000).
#
# Usage (with the dev stack running):
#   WS_ID=<workspace-id> \
#   TEAMS=<team-id-1>,<team-id-2> \
#   bash server/tools/swarm/demo.sh
#
#   CONVERGE_API_URL  default http://localhost:3001
#   WEB_ORIGIN        default http://localhost:3000
#   ADMIN_EMAIL       default demo@converge.dev
#   TEAMS             team ids the agents are granted (required for work)
#
# Ctrl-C stops the runners. The agents stay in the workspace; remove
# them from Settings → Members afterwards.
set -euo pipefail

API_URL="${CONVERGE_API_URL:-http://localhost:3001}"
WEB="${WEB_ORIGIN:-http://localhost:3000}"
ADMIN="${ADMIN_EMAIL:-demo@converge.dev}"
WS_ID="${WS_ID:?set WS_ID to the workspace id}"
TEAMS="${TEAMS:?set TEAMS to comma-separated team ids the agents should join}"
cd "$(dirname "$0")"

JAR=$(mktemp)
RUNNERS=()
cleanup() {
  rm -f "$JAR" converge-swarm swarm-*.log
  local pid
  for pid in "${RUNNERS[@]:-}"; do
    [ -n "$pid" ] && kill "$pid" 2>/dev/null || true
  done
}
trap cleanup EXIT

# --- sign in the admin ---------------------------------------------------
R=$(curl -sS -c "$JAR" -H 'Content-Type: application/json' \
  -d "{\"email\":\"$ADMIN\"}" "$WEB/api/auth/signinup/code")
LINK=$(echo "$R" | python3 -c 'import json,sys; print(json.load(sys.stdin)["devMagicLink"])')
CODE=$(echo "$LINK" | sed -n 's/.*#\([^#]*\)$/\1/p')
PRE=$(echo "$LINK" | sed -n 's/.*preAuthSessionId=\([^&#]*\).*/\1/p')
curl -sS -b "$JAR" -c "$JAR" -H 'Content-Type: application/json' \
  -d "{\"linkCode\":\"$CODE\",\"preAuthSessionId\":\"$PRE\"}" \
  "$WEB/api/auth/signinup/code/consume" > /dev/null
echo "signed in as $ADMIN"

TEAM_IDS_JSON=$(echo "$TEAMS" | python3 -c 'import json,sys; t=sys.stdin.read().strip(); print(json.dumps([x.strip() for x in t.split(",") if x.strip()]))')

# --- create the swarm ------------------------------------------------------
declare -a TOKENS=()
for NAME in scout hunter forge; do
  TOKEN_FILE=$(mktemp)
  curl -sS -b "$JAR" -H 'Content-Type: application/json' \
    -d "{\"name\":\"$NAME\",\"teamIds\":$TEAM_IDS_JSON}" \
    "$API_URL/api/v1/workspaces/$WS_ID/agents" > "$TOKEN_FILE"
  TOKENS+=( "$(python3 -c "import json;print(json.load(open('$TOKEN_FILE'))['token'])")" )
  rm -f "$TOKEN_FILE"
  echo "created agent: $NAME"
done

# --- assign work: unassigned open issues, round-robin over the agents ------
python3 - "$JAR" "$API_URL" "$WS_ID" <<'EOF'
import json, subprocess, sys

jar, api_url, ws_id = sys.argv[1], sys.argv[2], sys.argv[3]

def get(url):
    out = subprocess.run(["curl", "-sS", "-b", jar, url],
                         capture_output=True, text=True, check=True).stdout
    return json.loads(out)

feed = get(f"{api_url}/api/v1/sync_actions/bootstrap?workspaceId={ws_id}&modelNames=Issue,UsersOnWorkspaces")
issues = [r["data"] for r in feed["syncActions"] if r["modelName"] == "Issue"]
members = [r["data"]["userId"] for r in feed["syncActions"] if r["modelName"] == "UsersOnWorkspaces"]
users = get(f"{api_url}/api/v1/users?userIds=" + ",".join(members))
agent_ids = [u["id"] for u in users if u.get("kind") == "agent"]
if not agent_ids:
    print("no agents found — aborting assignment")
    sys.exit(1)
open_issues = [i for i in issues if not i.get("assigneeId") and i.get("stateId")]
for n, issue in enumerate(open_issues[: len(agent_ids) * 2]):
    target = agent_ids[n % len(agent_ids)]
    subprocess.run(
        ["curl", "-sS", "-b", jar, "-X", "POST",
         "-H", "Content-Type: application/json",
         "-d", json.dumps({"assigneeId": target}),
         f"{api_url}/api/v1/issues/{issue['id']}"],
        check=False,
    )
    print(f"assigned #{issue.get('number')}: {issue.get('title', '')[:40]}")
if not open_issues:
    print("no unassigned issues — create some, or assign in the UI")
EOF

# --- run the swarm -----------------------------------------------------------
rm -f converge-swarm
go build -o converge-swarm .
for i in "${!TOKENS[@]}"; do
  CONVERGE_API_URL="$API_URL" \
  CONVERGE_WORKSPACE_ID="$WS_ID" \
  CONVERGE_AGENT_TOKEN="${TOKENS[$i]}" \
  CONVERGE_NAME="agent-$((i+1))" \
  CONVERGE_POLL_SECONDS=6 \
  nohup ./converge-swarm >> "swarm-$((i+1)).log" 2>&1 &
  RUNNERS+=( $! )
  echo "runner $((i+1)) started (pid $!, log swarm-$((i+1)).log)"
done

echo
echo "swarm running — open $WEB and watch the board. Ctrl-C stops the runners."
wait
