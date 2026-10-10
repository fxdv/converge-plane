#!/bin/bash
# Install (or --uninstall) the Converge V100 tunnel daemons on macOS.
#
# Two LaunchAgents in ~/Library/LaunchAgents:
#   com.converge.v100tunnel            the reconnect loop (KeepAlive)
#   com.converge.v100tunnel-watchdog   2-minute liveness probe (StartInterval)
#
# Idempotent: safe to re-run after moving the repo or after --uninstall.
# Install:     CONVERGE_TUNNEL_HOST=user@host CONVERGE_TUNNEL_KEY=/path/to/key \
#                scripts/install-v100-tunnel.sh
#              (both are required; they are written into the plists'
#              EnvironmentVariables because launchd does not inherit your shell)
# Uninstall:   scripts/install-v100-tunnel.sh --uninstall
set -euo pipefail

SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
AGENTS="$HOME/Library/LaunchAgents"
LOGS="$HOME/Library/Logs"
UID_DOMAIN="gui/$(id -u)"

TUNNEL_LABEL=com.converge.v100tunnel
WATCHDOG_LABEL=com.converge.v100tunnel-watchdog

mkdir -p "$AGENTS" "$LOGS"

load() {
	launchctl bootout "$UID_DOMAIN/$1" 2>/dev/null || true
}

install_one() {
	local label=$1 program=$2 extra=$3
	load "$label"
	cat > "$AGENTS/$label.plist" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>$label</string>
	<key>ProgramArguments</key>
	<array>
		<string>/bin/bash</string>
		<string>$program</string>
	</array>
	$extra
	<key>EnvironmentVariables</key>
	<dict>
		<key>CONVERGE_TUNNEL_HOST</key>
		<string>$TUNNEL_HOST</string>
		<key>CONVERGE_TUNNEL_KEY</key>
		<string>$TUNNEL_KEY</string>
	</dict>
	<key>WorkingDirectory</key>
	<string>$HOME</string>
	<key>StandardOutPath</key>
	<string>$LOGS/converge-v100-tunnel.log</string>
	<key>StandardErrorPath</key>
	<string>$LOGS/converge-v100-tunnel.log</string>
</dict>
</plist>
EOF
	plutil -lint "$AGENTS/$label.plist" > /dev/null
	launchctl bootstrap "$UID_DOMAIN" "$AGENTS/$label.plist"
	# A bootstrap from a non-GUI context (ssh, agent harness) leaves the job
	# as a "partial import" — deferred until the next login. Kick it so the
	# installer works from any context; harmless if already running.
	launchctl kickstart "$UID_DOMAIN/$label" 2> /dev/null || true
	echo "installed $label -> $program"
}

if [ "${1:-}" = "--uninstall" ]; then
	for label in "$TUNNEL_LABEL" "$WATCHDOG_LABEL"; do
		load "$label"
		rm -f "$AGENTS/$label.plist"
		echo "uninstalled $label"
	done
	exit 0
fi

TUNNEL_HOST="${CONVERGE_TUNNEL_HOST:-}"
TUNNEL_KEY="${CONVERGE_TUNNEL_KEY:-}"
if [ -z "$TUNNEL_HOST" ] || [ -z "$TUNNEL_KEY" ]; then
	echo "usage: CONVERGE_TUNNEL_HOST=user@host CONVERGE_TUNNEL_KEY=/path/to/ssh-key $0 [--uninstall]" >&2
	echo "  both CONVERGE_TUNNEL_HOST and CONVERGE_TUNNEL_KEY must be set (no defaults)" >&2
	exit 64
fi
if [ ! -r "$TUNNEL_KEY" ]; then
	echo "error: CONVERGE_TUNNEL_KEY=$TUNNEL_KEY is not readable" >&2
	exit 1
fi

# The loop: always running; launchd respawns it if the script itself dies.
install_one "$TUNNEL_LABEL" "$SCRIPT_DIR/v100-tunnel.sh" "<key>KeepAlive</key>
	<string>true</string>
	<key>ThrottleInterval</key>
	<integer>5</integer>"

# The watchdog: periodic probe, not a resident.
install_one "$WATCHDOG_LABEL" "$SCRIPT_DIR/v100-tunnel-watchdog.sh" "<key>RunAtLoad</key>
	<string>true</string>
	<key>StartInterval</key>
	<integer>120</integer>"

echo
echo "State:"
launchctl print "$UID_DOMAIN/$TUNNEL_LABEL" 2>/dev/null | grep -E '^\s*state' | head -1 | sed 's/^/  tunnel   /'
launchctl print "$UID_DOMAIN/$WATCHDOG_LABEL" 2>/dev/null | grep -E '^\s*state' | head -1 | sed 's/^/  watchdog /'
echo "Log: $LOGS/converge-v100-tunnel.log"
