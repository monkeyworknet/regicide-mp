#!/usr/bin/env bash
# Fails when a committed asset under the UI tree is missing from docs/CREDITS.md.
set -euo pipefail
cd "$(dirname "$0")/../.."
credits=docs/CREDITS.md
[ -f "$credits" ] || { echo "missing $credits"; exit 1; }
missing=0
while IFS= read -r f; do
  case "$f" in
    src/regicide/server/web/fonts/*|src/regicide/server/web/sfx/*|src/regicide/server/web/img/*|src/regicide/server/web/data/audio.json)
      base=$(basename "$f")
      if ! grep -qF "$base" "$credits"; then
        echo "not in CREDITS.md: $f"
        missing=$((missing+1))
      fi
      ;;
  esac
done < <(git ls-files)
if [ "$missing" -gt 0 ]; then echo "$missing asset(s) undocumented"; exit 1; fi
echo "credits: all committed assets documented"
