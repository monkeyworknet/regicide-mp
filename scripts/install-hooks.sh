#!/usr/bin/env bash
# Install a pre-commit hook that runs the fast half of the merge gate. The full
# gate (scripts/gate.sh) stays the contract; a hook is the cheap guard that keeps
# a broken build or a stray gofmt out of a commit in the first place.
set -euo pipefail
cd "$(dirname "$0")/.."
# In a worktree .git is a file, so ask git for the hook path (it resolves to the
# shared hooks directory, which is what a commit in any worktree uses).
hook=$(git rev-parse --git-path hooks/pre-commit)

# Never destroy a hook someone else wrote: chain to it unless --force is given.
if [ -e "$hook" ] && ! grep -q "regicide: fast pre-commit" "$hook" 2>/dev/null; then
  if [ "${1:-}" != "--force" ]; then
    echo "$hook already exists and is not ours; re-run with --force to replace it," >&2
    echo "or move it aside first." >&2
    exit 1
  fi
  cp "$hook" "$hook.bak"
  echo "backed up the previous hook to $hook.bak" >&2
fi

cat > "$hook" <<'HOOK'
#!/usr/bin/env bash
# regicide: fast pre-commit checks: formatting, vet, build, unit tests (Go + JS).
# Delete this file to skip it; the merge gate is still scripts/gate.sh.
set -uo pipefail
cd "$(git rev-parse --show-toplevel)"
export GO111MODULE=off GOPATH="$PWD"
fail=0
check() { local name="$1"; shift; if out=$("$@" 2>&1); then :; else echo "pre-commit: $name failed"; echo "$out" | tail -5; fail=1; fi; }
check "gofmt"  bash -c 'test -z "$(gofmt -l src)"'
check "go vet"  go vet ./src/...
check "go build" go build -o bin/regicide regicide/cmd/regicide
check "go test" go test ./src/...
check "js test" node --test src/test/js/*.test.mjs
exit $fail
HOOK
chmod +x "$hook"
echo "installed $hook"
