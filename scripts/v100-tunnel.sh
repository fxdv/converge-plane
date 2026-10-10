#!/bin/bash
# Converge V100 fleet tunnel — reconnect loop.
#
# Forwards 127.0.0.1:8000-8003 to the remote GPU host, where one
# OpenAI-compatible endpoint (qwen3.8-27b) is served per port. Designed to
# run under the com.converge.v100tunnel LaunchAgent (install-v100-tunnel.sh):
#
#   - the loop reconnects on any ssh death — sleep/wake, wifi switch,
#     remote host down — with bounded backoff (3s -> 30s max, reset after a
#     healthy session);
#   - KeepAlive in the LaunchAgent respawns the loop itself if it dies;
#   - a half-open connection (ssh thinks it is alive, the path is dead) is
#     the one case the loop cannot see; v100-tunnel-watchdog.sh covers it.
#
# Required env: CONVERGE_TUNNEL_HOST (user@host), CONVERGE_TUNNEL_KEY (ssh key
# path). Optional overrides: CONVERGE_TUNNEL_LOG. Never prompts: BatchMode.
set -u
umask 077

HOST="${CONVERGE_TUNNEL_HOST:-}"
KEY="${CONVERGE_TUNNEL_KEY:-}"
if [ -z "$HOST" ] || [ -z "$KEY" ]; then
	echo "usage: CONVERGE_TUNNEL_HOST=user@host CONVERGE_TUNNEL_KEY=/path/to/ssh-key $0" >&2
	echo "  both CONVERGE_TUNNEL_HOST and CONVERGE_TUNNEL_KEY must be set (no defaults)" >&2
	exit 64
fi
LOG="${CONVERGE_TUNNEL_LOG:-$HOME/Library/Logs/converge-v100-tunnel.log}"

log() { echo "$(date -Iseconds) [tunnel] $*" >> "$LOG"; }

if [ ! -r "$KEY" ]; then
	log "key $KEY unreadable — exiting; launchd will retry"
	exit 1
fi

log "connecting $HOST"
backoff=3
while true; do
	start=$SECONDS
	ssh -N \
		-i "$KEY" \
		-o BatchMode=yes \
		-o ConnectTimeout=10 \
		-o ServerAliveInterval=15 \
		-o ServerAliveCountMax=3 \
		-o ExitOnForwardFailure=yes \
		-o StrictHostKeyChecking=accept-new \
		-L 127.0.0.1:8000:localhost:8000 \
		-L 127.0.0.1:8001:localhost:8001 \
		-L 127.0.0.1:8002:localhost:8002 \
		-L 127.0.0.1:8003:localhost:8003 \
		"$HOST"
	rc=$?
	up=$(( SECONDS - start ))
	# A session that lasted >60s was healthy; reset the backoff.
	if [ "$up" -ge 60 ]; then
		backoff=3
	fi
	log "ssh exited rc=$rc (up ${up}s) — reconnecting in ${backoff}s"
	sleep "$backoff"
	if [ "$backoff" -lt 30 ]; then
		backoff=$(( backoff * 2 ))
	fi
done
