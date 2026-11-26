#!/usr/bin/env bash
# Merge gate. Every stage must pass; the table at the end is the evidence.
# Rules: no external deps, no `pkill -f`, every stage is time-bounded.
#
# The stages are grouped into lanes that run at the same time. Every lane owns its
# own server (an ephemeral port), its own temp data directory and its own browser
# profile, so the only thing they share is the read-only tree and bin/regicide,
# which the preflight builds before any lane starts.
#
#   ./scripts/gate.sh             all 28 stages, lanes in parallel
#   GATE_FAST=1 ./scripts/gate.sh the fast lane only; browser stages are SKIP rows
#
set -uo pipefail
cd "$(dirname "$0")/.."
export GO111MODULE=off GOPATH="$PWD"

START=$SECONDS
PASS=0; FAIL=0; SKIP=0
declare -a ROWS
LANE_DIR=$(mktemp -d)
trap 'rm -rf "$LANE_DIR"' EXIT

# record appends one table row. Inside a lane, GATE_LANE_FILE sends it to that
# lane's own file: a subshell cannot write the parent's array.
record() { # name status duration note
  local row
  row="$(printf '%-26s %-6s %6ss  %s' "$1" "$2" "$3" "$4")"
  if [ -n "${GATE_LANE_FILE:-}" ]; then
    # Status first, so the parent counts without parsing the padded row.
    printf '%s\t%s\n' "$2" "$row" >>"$GATE_LANE_FILE"
    return
  fi
  ROWS+=("$row")
  case "$2" in
    PASS) PASS=$((PASS+1)) ;;
    SKIP) SKIP=$((SKIP+1)) ;;
    *)    FAIL=$((FAIL+1)) ;;
  esac
}

stage() { # name timeout_seconds command...
  local name="$1" tmo="$2"; shift 2
  local start=$SECONDS out
  out=$(timeout "$tmo" "$@" 2>&1); local rc=$?
  local dur=$((SECONDS-start))
  if [ $rc -eq 0 ]; then record "$name" PASS "$dur" "";
  else record "$name" FAIL "$dur" "$(printf '%s' "$out" | tail -3 | tr '\n' ' ')"; fi
  return $rc
}

skip_stage() { # name why
  record "$1" SKIP 0 "$2"
}

# ---------------------------------------------------------------------------
# fast lane: tests, bench, assets. Nothing here needs a browser; the three
# preflight stages (gofmt, vet, build) run before the lanes start.
# ---------------------------------------------------------------------------
lane_fast() {
  stage "go test -race"    180 bash -c 'go test -race -cover ./src/...'

  # engine coverage >= 90% (skipped while the engine package does not exist yet)
  if [ -d src/regicide/engine ]; then
    stage "engine coverage" 120 bash -c 'go test -coverprofile=/tmp/regicide-cover.out ./src/regicide/engine >/dev/null && \
      pct=$(go tool cover -func=/tmp/regicide-cover.out | awk "/^total:/ {print \$3}" | tr -d "%") && \
      echo "engine coverage ${pct}%" && awk -v p="$pct" "BEGIN{exit !(p+0>=90)}"'
  else
    record "engine coverage" SKIP 0 "no engine package yet"
  fi

  # ES modules, parsed as modules: `node --check` treats a file as a script, and an
  # edit that left an orphan `catch` passed script-mode and failed in the browser.
  stage "js module syntax"  60 bash -c 'for f in src/regicide/server/web/*.js; do
      node --input-type=module --check < "$f" >/dev/null || { echo "$f"; exit 1; }
    done'
  # The bot's quality is a number, so a policy regression is a red gate rather than a
  # surprise in a game. The stage runs the SHIPPED policy (`bot.DefaultPolicy()` =
  # human-engine), not the greedy baseline: a floor on greedy let a regression ship.
  # 200 three-seat games take well under a second; the floor is well below the
  # measured 25-34% on this prefix.
  stage "bot bench"        120 bash -c 'go build -o bin/coopbench regicide/cmd/coopbench && \
    ./bin/coopbench -n 200 -seats 3 -policy human-engine > /tmp/coopbench.txt && \
    cat /tmp/coopbench.txt && \
    awk "/royals cleared/ { r=\$3+0; seenR=1 } /^  wins/ { w=\$2+0; seenW=1 } END { if (!seenR || !seenW || r < 9.0 || w < 18.0) exit 1 }" /tmp/coopbench.txt'
  # pipefail matters here: without it the pipeline reports tail's status and a failing
  # test is a green stage. The nested shell does not inherit the gate's shell options.
  stage "js unit tests"     90 bash -c 'set -o pipefail; node --test src/test/js/*.test.mjs | tail -4'
  stage "web data files"    60 python3 tools/assets/check_web_data.py
  stage "official assets"  120 python3 tools/assets/build_official.py --check
  stage "simple assets"    120 python3 tools/assets/build_gfx_themes.py --check
  stage "casino backs"     120 python3 tools/assets/build_casino_backs.py --check
  stage "8bit assets"      120 python3 tools/assets/build_8bit_theme.py --check
  stage "pokemon assets"   120 python3 tools/assets/build_pokemon.py --check
  stage "theme registry"    60 python3 tools/assets/build_theme_registry.py --check
  stage "credits provenance" 60 tools/assets/check_credits.sh
  stage "rules coverage"     60 tools/assets/check_rules_coverage.sh
}

# ---------------------------------------------------------------------------
# ui lane: boot the binary on a free port, drive it with headless chromium.
# ---------------------------------------------------------------------------
ui_stage() {
  local start=$SECONDS
  if ! command -v chromium >/dev/null; then
    record "ui smoke" SKIP 0 "chromium not installed"; return
  fi
  local port tmp pid ok=0 note=""
  port=$(python3 - <<'PY'
import socket
s = socket.socket(); s.bind(("127.0.0.1", 0)); print(s.getsockname()[1]); s.close()
PY
)
  tmp=$(mktemp -d)
  ./bin/regicide -addr "127.0.0.1:$port" -data "$tmp" >"$tmp/server.log" 2>&1 &
  pid=$!
  trap 'kill "${pid:-}" 2>/dev/null; rm -rf "${tmp:-}"' RETURN

  for _ in $(seq 1 50); do
    if curl -fsS "http://127.0.0.1:$port/api/health" >/dev/null 2>&1; then ok=1; break; fi
    sleep 0.1
  done
  if [ $ok -eq 0 ]; then
    record "ui smoke" FAIL $((SECONDS-start)) "server did not answer /api/health"
    kill "$pid" 2>/dev/null; rm -rf "$tmp"; return
  fi

  local shots="artifacts/screens"; mkdir -p "$shots"
  local vp name dom fails=0
  for vp in "360,740" "390,844" "1440,900"; do
    name=$(echo "$vp" | tr ',' 'x')
    dom=$(timeout 60 chromium --headless=new --no-sandbox --disable-gpu --hide-scrollbars \
      --user-data-dir="$tmp/chrome-$name" --virtual-time-budget=8000 --window-size="$vp" \
      --dump-dom "http://127.0.0.1:$port/?test=smoke" 2>/dev/null)
    if ! printf '%s' "$dom" | grep -q '"pass":true'; then
      fails=$((fails+1))
      note="harness failed at ${name}: $(printf '%s' "$dom" | grep -o '"name":"[^"]*","ok":false' | head -3 | tr '\n' ' ')"
    fi
    timeout 60 chromium --headless=new --no-sandbox --disable-gpu --hide-scrollbars \
      --user-data-dir="$tmp/shots-$name" --virtual-time-budget=6000 --window-size="$vp" \
      --screenshot="$shots/$name.png" "http://127.0.0.1:$port/" >/dev/null 2>&1 \
      || note="$note screenshot ${name} failed"
  done

  kill "$pid" 2>/dev/null
  rm -rf "$tmp"
  local dur=$((SECONDS-start))
  # Merge rule 3 cites the screenshots, so a missing file is a failure, not a note.
  for name in 360x740 390x844 1440x900; do
    [ -s "$shots/$name.png" ] || { fails=$((fails+1)); note="$note missing $name.png"; }
  done
  if [ $fails -eq 0 ]; then record "ui smoke (2 viewports)" PASS "$dur" "screens in $shots"
  else record "ui smoke (2 viewports)" FAIL "$dur" "$note"; fi
}

# ---- exact kill (C4/C6): the browser proof the Phase C campaign could not force.
# Seed `EXACTKILL-0` opens on the Jack of Spades (20 health) with 10C in hand; clubs
# double to exactly 20. The script drives the real menu, hand and Play button.
exact_kill_stage() {
  local start=$SECONDS
  if ! command -v chromium >/dev/null; then
    record "exact kill (C4/C6)" SKIP 0 "chromium not installed"; return
  fi
  local out rc
  out=$(timeout 120 node scripts/exact-kill.mjs 2>&1); rc=$?
  local dur=$((SECONDS-start))
  if [ $rc -eq 0 ]; then record "exact kill (C4/C6)" PASS "$dur" "$(printf '%s' "$out" | grep -o '"pass":true' | head -1)"
  else record "exact kill (C4/C6)" FAIL "$dur" "$(printf '%s' "$out" | tail -2 | tr '\n' ' ')"; fi
}

# ---- seat lifecycle: the second playtest's room rules, driven through the client
# functions the buttons call — kick, add a bot, cover a seat and return, leave.
seats_stage() {
  local start=$SECONDS
  if ! command -v chromium >/dev/null; then
    record "seat lifecycle smoke" SKIP 0 "chromium not installed"; return
  fi
  local port tmp pid ok=0 dom
  port=$(python3 -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1",0)); print(s.getsockname()[1]); s.close()')
  tmp=$(mktemp -d)
  ./bin/regicide -addr "127.0.0.1:$port" -data "$tmp" >"$tmp/server.log" 2>&1 &
  pid=$!
  trap 'kill "${pid:-}" 2>/dev/null; rm -rf "${tmp:-}"' RETURN
  for _ in $(seq 1 50); do
    if curl -fsS "http://127.0.0.1:$port/api/health" >/dev/null 2>&1; then ok=1; break; fi
    sleep 0.1
  done
  if [ $ok -eq 0 ]; then
    record "seat lifecycle smoke" FAIL $((SECONDS-start)) "server did not answer /api/health"
    kill "$pid" 2>/dev/null; rm -rf "$tmp"; return
  fi
  dom=$(timeout 90 chromium --headless=new --no-sandbox --disable-gpu --hide-scrollbars \
    --user-data-dir="$tmp/chrome" --virtual-time-budget=20000 --window-size=1440,900 \
    --dump-dom "http://127.0.0.1:$port/?test=seats" 2>/dev/null)
  kill "$pid" 2>/dev/null
  rm -rf "$tmp"
  local dur=$((SECONDS-start))
  if printf '%s' "$dom" | grep -q '"pass":true'; then
    record "seat lifecycle smoke" PASS "$dur" "kick, add bot, cover/return, leave, join replaces"
  else
    record "seat lifecycle smoke" FAIL "$dur" "$(printf '%s' "$dom" | grep -o '"name":"seats[^"]*","ok":false[^}]*' | head -3 | tr '\n' ' ')"
  fi
}

lane_ui() {
  ui_stage
  exact_kill_stage
  seats_stage
}

# ---------------------------------------------------------------------------
# table lane: the co-op table on both transports, its piles, and the solo board
# geometry baseline (the two modes share one DOM, so a room rule can flatten solo).
# ---------------------------------------------------------------------------
# ---- co-op stage: a real room, two seats, a card played from the DOM (F-1)
# Two transports for one release (T1.5): the stream is the default, the poll path is
# the fallback, and both must play a card.
coop_stage() {
  local start=$SECONDS
  if ! command -v chromium >/dev/null; then
    record "co-op smoke" SKIP 0 "chromium not installed"; return
  fi
  local port tmp pid ok=0 dom fails=0 note="" mode query label
  port=$(python3 -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1",0)); print(s.getsockname()[1]); s.close()')
  tmp=$(mktemp -d)
  ./bin/regicide -addr "127.0.0.1:$port" -data "$tmp" >"$tmp/server.log" 2>&1 &
  pid=$!
  trap 'kill "${pid:-}" 2>/dev/null; rm -rf "${tmp:-}"' RETURN

  for _ in $(seq 1 50); do
    if curl -fsS "http://127.0.0.1:$port/api/health" >/dev/null 2>&1; then ok=1; break; fi
    sleep 0.1
  done
  if [ $ok -eq 0 ]; then
    record "co-op smoke" FAIL $((SECONDS-start)) "server did not answer /api/health"
    kill "$pid" 2>/dev/null; rm -rf "$tmp"; return
  fi

  for mode in sse poll; do
    if [ "$mode" = "poll" ]; then query="?test=coop&transport=poll"; else query="?test=coop"; fi
    dom=$(timeout 90 chromium --headless=new --no-sandbox --disable-gpu --hide-scrollbars \
      --user-data-dir="$tmp/chrome-$mode" --virtual-time-budget=25000 --window-size=1440,900 \
      --dump-dom "http://127.0.0.1:$port/$query" 2>/dev/null)
    if printf '%s' "$dom" | grep -q '"pass":true'; then :; else
      fails=$((fails+1))
      note="$note $mode: $(printf '%s' "$dom" | grep -o '"name":"[^"]*","ok":false[^}]*' | head -3 | tr '\n' ' ')"
    fi
  done

  kill "$pid" 2>/dev/null
  rm -rf "$tmp"
  local dur=$((SECONDS-start))
  if [ $fails -eq 0 ]; then
    record "co-op smoke (sse+poll)" PASS "$dur" "both transports played a card"
  else
    record "co-op smoke (sse+poll)" FAIL "$dur" "$note"
  fi
}

# ---- co-op piles: two playtest notes were visual (the second tavern back printed over
# the label; the empty discard slot overflowed its art box). Measured as boxes at four
# widths, because a screenshot comparison would flake on fonts and catch neither.
piles_stage() {
  local start=$SECONDS
  if ! command -v chromium >/dev/null; then
    record "co-op piles" SKIP 0 "chromium not installed"; return
  fi
  local out rc
  out=$(timeout 240 node scripts/coop-shots.mjs 2>&1); rc=$?
  local dur=$((SECONDS-start))
  if [ $rc -eq 0 ]; then record "co-op piles (5 widths)" PASS "$dur" "one tavern back, placeholder inside its box, stats in one line, hand centred, rail fits"
  else record "co-op piles (5 widths)" FAIL "$dur" "$(printf '%s' "$out" | grep FAIL | head -3 | tr '\n' ' ')"; fi
}

# ---- layout stage: the co-op table shares the solo DOM, so a room-only CSS rule can
# silently flatten the solo board (it happened). Metrics against a committed baseline,
# not pixels: no font or antialiasing flake, and the failure names the element.
layout_stage() {
  local start=$SECONDS
  if ! command -v chromium >/dev/null; then
    record "solo layout" SKIP 0 "chromium not installed"; return
  fi
  local out
  out=$(timeout 180 node scripts/layout-check.mjs 2>&1); local rc=$?
  local dur=$((SECONDS-start))
  if [ $rc -eq 0 ]; then record "solo layout" PASS "$dur" "board geometry matches docs/layout-baseline.json"
  else record "solo layout" FAIL "$dur" "$(printf '%s' "$out" | tail -4 | tr '\n' ' ')"; fi
}

lane_table() {
  coop_stage
  piles_stage
  layout_stage
}

# ---------------------------------------------------------------------------
# gecko lane: the engine the players actually use. Chromium is the default
# everywhere else; a Blink-only bug has shipped twice because of that.
# ---------------------------------------------------------------------------
gecko_stage() {
  local start=$SECONDS
  if ! command -v geckodriver >/dev/null || ! command -v firefox-esr >/dev/null; then
    record "gecko smoke" SKIP 0 "firefox-esr or geckodriver not installed"; return
  fi
  local port tmp pid ok=0 out rc note=""
  port=$(python3 -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1",0)); print(s.getsockname()[1]); s.close()')
  tmp=$(mktemp -d)
  ./bin/regicide -addr "127.0.0.1:$port" -data "$tmp" >"$tmp/server.log" 2>&1 &
  pid=$!
  for _ in $(seq 1 50); do
    if curl -fsS "http://127.0.0.1:$port/api/health" >/dev/null 2>&1; then ok=1; break; fi
    sleep 0.1
  done
  if [ $ok -eq 0 ]; then
    record "gecko smoke" FAIL $((SECONDS-start)) "server did not answer"
    kill "$pid" 2>/dev/null; rm -rf "$tmp"; return
  fi
  out=$(timeout 240 node scripts/gecko-smoke.mjs --base "http://127.0.0.1:$port" 2>&1); rc=$?
  note=$(printf '%s' "$out" | grep -E "FAIL" | head -2 | tr '\n' ' ')
  kill "$pid" 2>/dev/null
  rm -rf "$tmp"
  local dur=$((SECONDS-start))
  if [ $rc -eq 0 ]; then record "gecko smoke" PASS "$dur" "solo, co-op sse, co-op poll in Firefox"
  else record "gecko smoke" FAIL "$dur" "$note"; fi
}

# The co-op phone board in Gecko, as a contract: one column, no auto-placed grid items,
# pile art containing its cards, and a hand that keeps its width with the peek open.
gecko_layout_stage() {
  local start=$SECONDS
  if ! command -v geckodriver >/dev/null || ! command -v firefox-esr >/dev/null; then
    record "gecko phone layout" SKIP 0 "firefox-esr or geckodriver not installed"; return
  fi
  local port tmp pid ok=0 out rc
  port=$(python3 -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1",0)); print(s.getsockname()[1]); s.close()')
  tmp=$(mktemp -d)
  ./bin/regicide -addr "127.0.0.1:$port" -data "$tmp" >"$tmp/server.log" 2>&1 &
  pid=$!
  for _ in $(seq 1 50); do
    if curl -fsS "http://127.0.0.1:$port/api/health" >/dev/null 2>&1; then ok=1; break; fi
    sleep 0.1
  done
  if [ $ok -eq 0 ]; then
    record "gecko phone layout" FAIL $((SECONDS-start)) "server did not answer"
    kill "$pid" 2>/dev/null; rm -rf "$tmp"; return
  fi
  out=$(timeout 240 node scripts/layout-gecko.mjs --base "http://127.0.0.1:$port" 2>&1); rc=$?
  kill "$pid" 2>/dev/null
  rm -rf "$tmp"
  local dur=$((SECONDS-start))
  if [ $rc -eq 0 ]; then record "gecko phone layout" PASS "$dur" "one column, no auto-placed items, at 360 and 390"
  else record "gecko phone layout" FAIL "$dur" "$(printf '%s' "$out" | grep FAIL | head -2 | tr '\n' ' ')"; fi
}

lane_gecko() {
  gecko_stage
  gecko_layout_stage
}

# ---------------------------------------------------------------------------
# net lane: paused TCP link and disconnect/reclaim.
# ---------------------------------------------------------------------------
# ---- stream drop (campaign B13): a paused TCP link proves the 12 s silence watchdog
# raises the connection chip, and that a resumed link applies the in-flight action once.
# CDP network emulation did not freeze the SSE stream, so this uses a proxy that stops
# forwarding bytes while keeping both sockets open.
drop_stage() {
  local start=$SECONDS
  if ! command -v chromium >/dev/null; then
    record "stream drop (B13)" SKIP 0 "chromium not installed"; return
  fi
  local out rc
  out=$(timeout 120 node scripts/stream-drop.mjs 2>&1); rc=$?
  local dur=$((SECONDS-start))
  if [ $rc -eq 0 ]; then record "stream drop (B13)" PASS "$dur" "chip at 12s, recovers, one action"
  else record "stream drop (B13)" FAIL "$dur" "$(printf '%s' "$out" | grep '"ok":false' | head -2 | tr '\n' ' ')"; fi
}

# ---- disconnect/reclaim/end sheet (campaign B7, B8, B16): the shared table never
# observed these. Two real profiles, a short bot-fill window (`-botfill 3s`, the real
# code path without a real minute), then a forced loss for the end sheet: guest has no
# Play again, the host's download is a JSONL and Watch the replay mints a ticket.
disconnect_stage() {
  local start=$SECONDS
  if ! command -v chromium >/dev/null; then
    record "disconnect + end sheet" SKIP 0 "chromium not installed"; return
  fi
  local out rc
  out=$(timeout 180 node scripts/coop-disconnect.mjs 2>&1); rc=$?
  local dur=$((SECONDS-start))
  if [ $rc -eq 0 ]; then record "disconnect + end sheet" PASS "$dur" "bot at 3s, reclaim, sheet roles"
  else record "disconnect + end sheet" FAIL "$dur" "$(printf '%s' "$out" | grep '"ok":false' | head -2 | tr '\n' ' ')"; fi
}

# ---- reveal pacing (0.9.8): while a reveal is on screen the room sequence may not
# move. A real room, two bot seats and a host seat played over the API; the sampler
# compares the panel element with the sequence, so a queue handover is not a violation.
reveal_stage() {
  local start=$SECONDS
  if ! command -v chromium >/dev/null; then
    record "reveal pacing" SKIP 0 "chromium not installed"; return
  fi
  local out rc
  out=$(timeout 120 node scripts/reveal-pace.mjs 2>&1); rc=$?
  local dur=$((SECONDS-start))
  if [ $rc -eq 0 ]; then record "reveal pacing" PASS "$dur" "$(printf '%s' "$out" | grep -o '"detail":"[^"]*"' | tail -2 | tr '\n' ' ')"
  else record "reveal pacing" FAIL "$dur" "$(printf '%s' "$out" | grep '"ok":false' | head -2 | tr '\n' ' ')"; fi
}

lane_net() {
  drop_stage
  disconnect_stage
  reveal_stage
}

# ---------------------------------------------------------------------------
# run
# ---------------------------------------------------------------------------
LANE_ORDER=(fast ui table gecko net)
declare -A LANE_FN=(
  [fast]=lane_fast
  [ui]=lane_ui
  [table]=lane_table
  [gecko]=lane_gecko
  [net]=lane_net
)
# The browser stages of each lane, for the SKIP rows a limited run prints.
SKIP_UI=("ui smoke (2 viewports)" "exact kill (C4/C6)" "seat lifecycle smoke")
SKIP_TABLE=("co-op smoke (sse+poll)" "co-op piles (5 widths)" "solo layout")
SKIP_GECKO=("gecko smoke" "gecko phone layout")
SKIP_NET=("stream drop (B13)" "disconnect + end sheet" "reveal pacing")

lane_enabled() { # name
  [ "${GATE_FAST:-0}" = "1" ] || return 0
  [ "$1" = "fast" ]
}

skip_lane() { # name why
  local -a names=()
  case "$1" in
    ui) names=("${SKIP_UI[@]}") ;;
    table) names=("${SKIP_TABLE[@]}") ;;
    gecko) names=("${SKIP_GECKO[@]}") ;;
    net) names=("${SKIP_NET[@]}") ;;
  esac
  local n
  for n in "${names[@]}"; do skip_stage "$n" "$2"; done
}

# Preflight: the browser lanes need bin/regicide, so these three run before any lane
# and stop the run if they fail. They are three of the 28 rows.
pf_failed=0
stage "gofmt"    60  bash -c 'test -z "$(gofmt -l src)"' || pf_failed=1
stage "go vet"   120 bash -c 'go vet ./src/...' || pf_failed=1
stage "go build" 120 bash -c 'go build -o bin/regicide regicide/cmd/regicide' || pf_failed=1
if [ $pf_failed -ne 0 ]; then
  printf '\n=== gate ===\n'
  printf '%s\n' "${ROWS[@]}"
  printf '\npreflight failed: no lane started\n'
  exit 1
fi

# Each enabled lane runs in its own subshell and writes its own rows. A lane that is
# off prints SKIP rows for its stages, so the table always lists all 28.
declare -A LANE_PID=()
for lane in "${LANE_ORDER[@]}"; do
  if lane_enabled "$lane"; then
    ( GATE_LANE_FILE="$LANE_DIR/$lane.txt"; "${LANE_FN[$lane]}" ) &
    LANE_PID[$lane]=$!
  else
    skip_lane "$lane" "GATE_FAST=1"
  fi
done

for lane in "${LANE_ORDER[@]}"; do
  if [ -n "${LANE_PID[$lane]:-}" ]; then wait "${LANE_PID[$lane]}"; fi
done

# Assemble the table in lane order, then count.
for lane in "${LANE_ORDER[@]}"; do
  if [ -f "$LANE_DIR/$lane.txt" ]; then
    while IFS=$'\t' read -r status row; do
      ROWS+=("$row")
      case "$status" in
        PASS) PASS=$((PASS+1)) ;;
        SKIP) SKIP=$((SKIP+1)) ;;
        *)    FAIL=$((FAIL+1)) ;;
      esac
    done <"$LANE_DIR/$lane.txt"
  fi
done

printf '\n=== gate ===\n'
for row in "${ROWS[@]}"; do printf '%s\n' "$row"; done
printf '\n%d passed, %d failed, %d skipped — %ds wall\n' "$PASS" "$FAIL" "$SKIP" "$((SECONDS-START))"

# A lane that dies before writing its rows must not shrink the gate silently: the
# table is the evidence, and 28 is the contract (docs/PROCESS.md).
EXPECTED_STAGES=28
if [ $((PASS + FAIL + SKIP)) -ne $EXPECTED_STAGES ]; then
  printf 'gate listed %d stages, expected %d: a lane died before reporting\n' \
    "$((PASS + FAIL + SKIP))" "$EXPECTED_STAGES"
  exit 1
fi
[ "$FAIL" -eq 0 ] || exit 1
