#!/usr/bin/env bash
# Install the :8321 combo server as a boot service on this host's init system:
# systemd, or OpenRC. One-off per host; re-run after changing a template.
set -euo pipefail
cd "$(dirname "$0")/.."
ROOT=$PWD
UNIT=regicide-mp
SUDO=""
if [ "$(id -u)" -ne 0 ]; then SUDO="sudo -n"; fi

if [ ! -x bin/regicide ]; then
  echo "bin/regicide is missing; build it first: ./scripts/restart.sh" >&2
  exit 1
fi

# Hand over from a manual run.sh instance on the same port, if one is running.
# Without this the service starts and then loses the port to the old process.
if [ -f .run/regicide.pid ] && kill -0 "$(cat .run/regicide.pid)" 2>/dev/null; then
  kill "$(cat .run/regicide.pid)"
  rm -f .run/regicide.pid
  sleep 0.5
fi

if command -v systemctl >/dev/null 2>&1; then
  # The redirect must happen as root; `$SUDO sed ... > file` opens the file as the
  # caller, which fails on /etc/systemd/system.
  sed -e "s#@ROOT@#${ROOT}#g" -e "s#@USER@#$(id -un)#g" \
    scripts/${UNIT}.service.in | $SUDO tee /etc/systemd/system/${UNIT}.service >/dev/null
  $SUDO systemctl daemon-reload
  $SUDO systemctl enable ${UNIT}.service
  $SUDO systemctl restart ${UNIT}.service
  HOW="enabled at boot by systemd"
else
  SERVICE=/etc/init.d/${UNIT}
  sed -e "s#@ROOT@#${ROOT}#g" -e "s#@USER@#$(id -un)#g" \
    scripts/${UNIT}.openrc | $SUDO tee "$SERVICE" >/dev/null
  $SUDO chmod 755 "$SERVICE"
  $SUDO rc-update add ${UNIT} default
  $SUDO rc-service ${UNIT} restart
  HOW="enabled at boot by OpenRC"
fi

for _ in $(seq 1 50); do
  if curl -fsS "http://127.0.0.1:8321/api/health" >/dev/null 2>&1; then
    echo "${UNIT} is ${HOW} and running on :8321"
    curl -s "http://127.0.0.1:8321/api/health"
    echo
    exit 0
  fi
  sleep 0.2
done
echo "service started, but /api/health did not answer; see .run/regicide-boot.log" >&2
exit 1
