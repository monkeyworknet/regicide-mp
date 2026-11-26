#!/usr/bin/env bash
# Stop the :8321 server: the service if one is installed (systemd or
# OpenRC), else the pid recorded by scripts/run.sh (a manual or
# non-standard-port instance).
set -euo pipefail
cd "$(dirname "$0")/.."
SUDO=""
if [ "$(id -u)" -ne 0 ]; then SUDO="sudo -n"; fi

# systemd first. Without this branch a systemd host falls through to the pidfile
# path, finds no pidfile (the unit owns the process), and reports "nothing to
# stop" while the server keeps serving.
if [ -f /etc/systemd/system/regicide-mp.service ] || [ -f /lib/systemd/system/regicide-mp.service ]; then
  $SUDO systemctl stop regicide-mp.service
  echo "stopped the regicide-mp service (systemd)"
  exit 0
fi

if [ -e /etc/init.d/regicide-mp ] && $SUDO rc-service regicide-mp status >/dev/null 2>&1; then
  $SUDO rc-service regicide-mp stop
  echo "stopped the regicide-mp service"
  exit 0
fi

PIDFILE=.run/regicide.pid
[ -f "$PIDFILE" ] || { echo "no pidfile (.run/regicide.pid) — nothing to stop"; exit 0; }
pid=$(cat "$PIDFILE")
if ! kill -0 "$pid" 2>/dev/null; then
  echo "pid $pid is not running; clearing stale pidfile"
  rm -f "$PIDFILE"
  exit 0
fi
cmd=$(tr '\0' ' ' <"/proc/$pid/cmdline" 2>/dev/null || true)
case "$cmd" in
  *bin/regicide*) kill "$pid"; echo "stopped pid $pid" ;;
  *) echo "refusing to kill pid $pid: it is not our server ($cmd)" >&2; exit 1 ;;
esac
rm -f "$PIDFILE"
