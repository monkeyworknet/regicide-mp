#!/usr/bin/env bash
# Cross-checks three sets of R-IDs:
#   dossier matrix (docs/research/01-rules-dossier.md Appendix B)
#   ledger        (docs/rules-coverage.md)
#   go tests      (func TestR0xx_*)
# IDs are normalised by removing non-digits so R-013 == R013 == TestR013_.
# It also resolves every literal `TestName` the ledger cites, so the ledger cannot
# point at a test that was renamed or never written.
set -euo pipefail
cd "$(dirname "$0")/../.."

norm() { grep -oE 'R-?[0-9]{3}' "$@" 2>/dev/null | sed 's/R-\?/R/' | sort -u || true; }
dossier=$(norm docs/research/01-rules-dossier.md)
ledger=$(norm docs/rules-coverage.md)
# BusyBox grep has no --include, which silently made this check vacuous once. Use
# find + xargs so the scan is portable and cannot degrade to "no tests found".
tests=$(find src -name '*_test.go' -print0 2>/dev/null \
  | xargs -0 grep -hoE 'func TestR[0-9]{3}_' 2>/dev/null \
  | grep -oE 'R[0-9]{3}' | sort -u || true)

total=$(printf '%s\n' "$dossier" | grep -c . || true)
[ "$total" -eq 0 ] && { echo "no R-IDs found in dossier"; exit 1; }

miss_ledger=0; miss_tests=0
while read -r id; do
  [ -z "$id" ] && continue
  printf '%s\n' "$ledger" | grep -qx "$id" || { echo "ledger missing $id"; miss_ledger=$((miss_ledger+1)); }
  printf '%s\n' "$tests" | grep -qx "$id" || miss_tests=$((miss_tests+1))
done <<< "$dossier"

covered=$(( total - miss_tests ))
echo "rules: $covered/$total matrix rows have tests; ledger gaps: $miss_ledger"
if [ "$miss_ledger" -gt 0 ]; then exit 1; fi
# A zero-test state is only acceptable while there is no engine package at all;
# otherwise the scan itself is broken and must fail loudly.
if [ "$(printf '%s' "$tests" | grep -c . || true)" -eq 0 ]; then
  if [ -d src/regicide/engine ]; then
    echo "FAIL: engine exists but no TestR0xx_* tests were found (broken scan?)"; exit 1
  fi
  echo "WARN: no engine package yet"; exit 0
fi
if [ "$miss_tests" -gt 0 ]; then echo "$miss_tests matrix rows without tests"; exit 1; fi

# Named tests in the ledger: literal backticked Test* names (an ellipsis means the row
# cites a family, not one function, and is skipped).
all_tests=$(find src -name '*_test.go' -print0 2>/dev/null | xargs -0 grep -hoE '^func (Test[A-Za-z0-9_]+)' 2>/dev/null | awk '{print $2}' | sort -u || true)
named=$(grep -oE '`Test[A-Za-z0-9_]+`' docs/rules-coverage.md 2>/dev/null | tr -d '`' | sort -u || true)
missing=0
while read -r name; do
  [ -z "$name" ] && continue
  printf '%s\n' "$all_tests" | grep -qE "^${name}(_|$)" || { echo "ledger cites a missing test: $name"; missing=$((missing+1)); }
done <<< "$(printf '%s\n' "$named" | grep -vx 'TestName')"
if [ "$missing" -gt 0 ]; then echo "$missing cited test(s) do not exist"; exit 1; fi
