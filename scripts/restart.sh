#!/usr/bin/env bash
# Rebuild and restart the :8321 server. When the OpenRC service is installed the
# service owns the process (and starts it at boot); otherwise this falls back to
# the manual run.sh/stop.sh pair.
set -euo pipefail
cd "$(dirname "$0")/.."
export GO111MODULE=off GOPATH="$PWD"
go build -o bin/regicide regicide/cmd/regicide

SUDO=""
if [ "$(id -u)" -ne 0 ]; then SUDO="sudo -n"; fi

# systemd and OpenRC both own the process when a service is
# installed. Checking both is deliberate: without the systemd branch a systemd host
# silently falls through to run.sh, starts a second instance of the server, and the
# port clash is only visible as a failed health check.
if [ -f /etc/systemd/system/regicide-mp.service ] || [ -f /lib/systemd/system/regicide-mp.service ]; then
  $SUDO systemctl restart regicide-mp.service
  for _ in $(seq 1 50); do
    if curl -fsS "http://127.0.0.1:8321/api/health" >/dev/null 2>&1; then
      curl -s "http://127.0.0.1:8321/api/health"
      echo
      exit 0
    fi
    sleep 0.2
  done
  echo "service restarted, but /api/health did not answer; see .run/regicide-boot.log and journalctl -u regicide-mp" >&2
  exit 1
fi

if [ -e /etc/init.d/regicide-mp ]; then
  $SUDO rc-service regicide-mp restart
  for _ in $(seq 1 50); do
    if curl -fsS "http://127.0.0.1:8321/api/health" >/dev/null 2>&1; then
      curl -s "http://127.0.0.1:8321/api/health"
      echo
      exit 0
    fi
    sleep 0.2
  done
  echo "service restarted, but /api/health did not answer; see .run/regicide-boot.log" >&2
  exit 1
fi

./scripts/stop.sh || true
sleep 0.3
./scripts/run.sh "$@"
