#!/bin/bash
# Converge V100 fleet watchdog — periodic liveness probe.
#
# Runs every 2 minutes under the com.converge.v100tunnel-watchdog
# LaunchAgent (install-v100-tunnel.sh). The tunnel loop reconnects when ssh
# notices its connection is dead, but a half-open TCP connection — typical
# after Mac sleep/wake through a silent NAT, or after the remote vLLM
# process is replaced — fools ssh into thinking the tunnel is alive. The
# watchdog probes the fleet; if the endpoint does not answer, it kills the
# stale ssh so the loop re-establishes fresh. Bounded recovery: <= interval.
set -u
umask 077

PORT="${CONVERGE_TUNNEL_PROBE_PORT:-8000}"
KEY="${CONVERGE_TUNNEL_KEY:-$HOME/coding/singularity.key}"
LOG="${CONVERGE_TUNNEL_LOG:-$HOME/Library/Logs/converge-v100-tunnel.log}"

# /v1/models is a token-free liveness probe.
if curl -sf -m 8 -o /dev/null "http://127.0.0.1:${PORT}/v1/models"; then
	exit 0
fi

echo "$(date -Iseconds) [watchdog] port ${PORT} unresponsive — killing stale tunnel for fresh reconnect" >> "$LOG"
# Match the fleet tunnel's ssh by its unique key path; the loop wrapper
# restarts the connection within seconds.
pkill -f "ssh -N -i ${KEY}" 2>/dev/null || true
