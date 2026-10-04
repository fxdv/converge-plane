#!/bin/bash
# Bravo stack tunnel — reconnect loop.
#
# Forwards local 127.0.0.1:3000 (web) and :3001 (API) to the remote host
# where docker compose publishes the Converge stack. Use after deploy when
# you want localhost in the browser without running web/api locally.
#
# Env overrides (all optional): CONVERGE_TUNNEL_HOST, CONVERGE_TUNNEL_KEY,
# CONVERGE_TUNNEL_LOG, CONVERGE_TUNNEL_REMOTE_WEB, CONVERGE_TUNNEL_REMOTE_API.
# Never prompts: BatchMode.
set -u
umask 077

HOST="${CONVERGE_TUNNEL_HOST:-user1@176.123.167.143}"
KEY="${CONVERGE_TUNNEL_KEY:-$HOME/coding/singularity.key}"
LOG="${CONVERGE_TUNNEL_LOG:-$HOME/Library/Logs/converge-bravo-tunnel.log}"
REMOTE_WEB="${CONVERGE_TUNNEL_REMOTE_WEB:-127.0.0.1:3000}"
REMOTE_API="${CONVERGE_TUNNEL_REMOTE_API:-127.0.0.1:3001}"

log() { echo "$(date -Iseconds) [bravo-tunnel] $*" >> "$LOG"; }

if [ ! -r "$KEY" ]; then
	log "key $KEY unreadable — exiting"
	exit 1
fi

log "connecting $HOST (local 3000 -> $REMOTE_WEB, local 3001 -> $REMOTE_API)"
backoff=3
while true; do
	start=$SECONDS
	ssh -N \
		-i "$KEY" \
		-o BatchMode=yes \
		-o ConnectTimeout=10 \
		-o ServerAliveInterval=30 \
		-o ServerAliveCountMax=3 \
		-o ExitOnForwardFailure=yes \
		-o StrictHostKeyChecking=accept-new \
		-L "127.0.0.1:3000:${REMOTE_WEB}" \
		-L "127.0.0.1:3001:${REMOTE_API}" \
		"$HOST"
	rc=$?
	up=$(( SECONDS - start ))
	if [ "$up" -ge 60 ]; then
		backoff=3
	fi
	log "ssh exited rc=$rc (up ${up}s) — reconnecting in ${backoff}s"
	sleep "$backoff"
	if [ "$backoff" -lt 30 ]; then
		backoff=$(( backoff * 2 ))
	fi
done
