# Process rules

Short, and enforced by habit rather than tooling.

## Branches and worktrees

- `main` is the integration branch. **No work is committed to `main` directly.**
- Every change starts in a worktree off `main`:

  ```bash
  git -C <repo> worktree add -b <branch> ../<branch>
  ln -sfn <repo>/artifacts ../<branch>/artifacts
  ```

- `artifacts/` (source art packs, ~60 MB) is symlinked into each worktree; it is gitignored.
- One writer per worktree. Read-only work (reviews, research) can share a checkout.

## Merge gate

A branch merges only when **all three** hold:

1. `./scripts/gate.sh` is green (28 stages; `GATE_FAST=1` skips the browser lanes).
2. A review with no stake in the change has read the branch diff, and its findings are either fixed
   or deferred with a reason recorded in the merge commit.
3. The change was checked at 390 px and 1440 px, or the diff cannot affect layout.

Merge with `--no-ff`, then remove the worktree and delete the branch:
`git worktree remove <path>`, `git branch -d <branch>`.

## Ports

- `:8321` belongs to the installed service. `scripts/restart.sh` rebuilds and restarts it;
  `scripts/run.sh` refuses to start a second instance there.
- A test instance uses another port: `./scripts/run.sh --addr :9000 -data $(mktemp -d)`.
  The gate picks an ephemeral port itself.

## Ownership

- Tests own behaviour: a rule, a wire format or a client invariant changes together with its test.
- One writer per worktree; a browser profile belongs to one process.
- Kill servers by pid, never by pattern.

## Documents

- `docs/INDEX.md` labels every document as live or reference.
- `docs/DECISIONS.md` is append-only and wins over every plan.
- Operational measurements (gate timing, bench numbers) are stated with the host and date they were
  taken on, because they are calibration, not a contract.
