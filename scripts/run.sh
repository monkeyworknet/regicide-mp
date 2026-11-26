#!/usr/bin/env bash
# Start the game server in the background and record its pid.
# Usage: ./scripts/run.sh [--foreground] [regicide flags...]
set -euo pipefail
cd "$(dirname "$0")/.."
export GO111MODULE=off GOPATH="$PWD"
mkdir -p .run
PIDFILE=.run/regicide.pid
LOG=.run/regicide.log

if [ -f "$PIDFILE" ] && kill -0 "$(cat "$PIDFILE")" 2>/dev/null; then
  echo "already running: pid $(cat "$PIDFILE") — use ./scripts/stop.sh first"
  exit 1
fi

go build -o bin/regicide regicide/cmd/regicide

if [ "${1:-}" = "--foreground" ]; then
  shift
  exec ./bin/regicide -addr "${ADDR:-:8321}" -data "${DATA_DIR:-./data}" "$@"
fi

# The health check must poll the address the binary actually binds. Reading only
# $ADDR meant `run.sh --addr :9000` started the server on :9000 and then waited on
# :8321, so the script reported failure with the server left running.
addr="${ADDR:-:8321}"
args=("$@")
for i in "${!args[@]}"; do
  case "${args[$i]}" in
    -addr) if [ $((i + 1)) -lt ${#args[@]} ]; then addr="${args[$((i + 1))]}"; fi ;;
    -addr=*) addr="${args[$i]#-addr=}" ;;
  esac
done
port="${addr##*:}"

# :8321 belongs to the service when one is installed. run.sh stays for other ports
# (previews, A/B builds); a second instance on :8321 would only fail to bind and
# leave you staring at a stale page. Both init systems are checked: on a
# systemd host the OpenRC test alone never fires, so run.sh would start a duplicate
# next to the systemd unit.
if [ "$port" = "8321" ]; then
  SUDO=""
  if [ "$(id -u)" -ne 0 ]; then SUDO="sudo -n"; fi
  # Refuse whenever the unit is *installed*, not just when it is active. A manual
  # instance started while the unit is stopped takes the port, and the unit's next
  # start (or boot, or Restart=always) then crash-loops on bind while /api/health
  # still answers from the manual process. Test a build on
  # another port instead: run.sh --addr :9000.
  if [ -f /etc/systemd/system/regicide-mp.service ] || [ -f /lib/systemd/system/regicide-mp.service ]; then
    echo ":8321 belongs to the regicide-mp service; use ./scripts/restart.sh (or systemctl start regicide-mp). For a manual test use another port: run.sh --addr :9000" >&2
    exit 1
  elif [ -e /etc/init.d/regicide-mp ] && $SUDO rc-service regicide-mp status >/dev/null 2>&1; then
    echo ":8321 is managed by the regicide-mp service; use ./scripts/restart.sh or rc-service regicide-mp restart" >&2
    exit 1
  fi
fi
nohup ./bin/regicide -addr "$addr" -data "${DATA_DIR:-./data}" "$@" >"$LOG" 2>&1 &
pid=$!
echo "$pid" >"$PIDFILE"
for _ in $(seq 1 50); do
  if curl -fsS "http://127.0.0.1:${port}/api/health" >/dev/null 2>&1; then
    echo "started pid $pid on $addr (log $LOG)"
    exit 0
  fi
  sleep 0.1
done
echo "pid $pid did not answer /api/health — see $LOG" >&2
exit 1
