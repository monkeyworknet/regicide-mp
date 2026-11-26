#!/usr/bin/env python3
"""Play a co-op game from the command line, against a running server.

This is the pre-UI test harness: it creates a room, seats N players (each with a
real seat token), starts the game, and then plays legal actions until the table
wins, loses, or runs out of patience. Every action goes through the same HTTP API
the browser will use, so it tests the real thing.

    ./scripts/coop-play.py                          # 3 seats on :8321
    ./scripts/coop-play.py --seats 4 --theme 8bit
    ./scripts/coop-play.py --url http://127.0.0.1:8321 --verbose
"""

import argparse
import json
import sys
import urllib.error
import urllib.request


def call(base, method, path, body=None, seat=None, watch=None):
    data = json.dumps(body).encode() if body is not None else None
    req = urllib.request.Request(base + path, method=method, data=data,
                                 headers={"Content-Type": "application/json"})
    if seat:
        req.add_header("X-Regicide-Seat", "%d:%s" % seat)
    if watch:
        req.add_header("X-Regicide-Watch", watch)
    try:
        with urllib.request.urlopen(req, timeout=10) as resp:
            raw = resp.read()
            return resp.status, (json.loads(raw) if raw else {})
    except urllib.error.HTTPError as err:
        raw = err.read()
        return err.code, (json.loads(raw) if raw else {})


class Seat:
    def __init__(self, base, code, index, token, name):
        self.base, self.code, self.index = base, code, index
        self.token, self.name = token, name
        self.sent = 0

    def state(self):
        status, body = call(self.base, "GET", "/api/rooms/" + self.code, seat=(self.index, self.token))
        if status != 200:
            raise SystemExit("seat %d cannot read the room: %s %s" % (self.index, status, body))
        return body

    def act(self, action):
        # One id per action. Reusing an id is a retry by definition, and the server
        # answers a retry from its cache without applying anything.
        self.sent += 1
        status, body = call(self.base, "POST", "/api/rooms/%s/actions" % self.code,
                            {"actionId": "%d-%d" % (self.index, self.sent), "action": action},
                            seat=(self.index, self.token))
        if status != 200:
            raise SystemExit("seat %d action %s rejected: %s %s" % (self.index, action, status, body))
        return body


def pick(state):
    """A plain, sensible player: answer a pending choice, then hit hard, then pay
    with the smallest cards, and yield only when the hand is nearly gone.

    Deliberately simple -- this harness exercises the rules over HTTP, it is not the
    solo bot (docs/BOT-PLAN.md).
    """
    legal = state["legal"]
    hand = {c["id"]: c for c in state.get("hand", [])}

    def by(kind):
        return [a for a in legal if a["type"] == kind]

    # A Jester asks who goes next: hand it to the seat holding the most cards.
    choices = by("choose_next_seat")
    if choices:
        counts = {p["seat"]: p["handCount"] for p in state.get("players", [])}
        return max(choices, key=lambda a: counts.get(a["seat"], 0))

    if by("end_payment"):
        return by("end_payment")[0]

    plays = by("play")
    if plays:
        incoming = state.get("incoming", 0)
        # Prefer a play that covers the attack; otherwise the hardest hit available.
        covering = [a for a in plays if a.get("attack", 0) >= incoming]
        pool = covering or plays
        return max(pool, key=lambda a: (a.get("attack", 0), -len(a.get("cardIds", []))))

    payments = by("pay_damage")
    if payments:
        # Smallest card first: the hand has to survive the rest of the castle.
        return min(payments, key=lambda a: hand.get(a["cardIds"][0], {}).get("discardValue", 99))

    if by("yield"):
        return by("yield")[0]
    return legal[0]


def play(base, args, seed="", quiet=False):
    """Create a room, seat the players, deal, and play to an ending.

    Returns (phase, state, actions), or None when the action budget ran out.
    """
    say = (lambda *a: None) if quiet else print

    status, health = call(base, "GET", "/api/health")
    if status != 200:
        raise SystemExit("no server at %s (%s)" % (base, status))

    status, host = call(base, "POST", "/api/rooms", {"theme": args.theme, "seed": seed})
    if status != 201:
        raise SystemExit("could not create a room: %s %s" % (status, host))
    code = host["code"]
    seats = [Seat(base, code, host["seat"], host["token"], host["room"]["seats"][0]["name"])]
    say("room %s on %s (%s), host %s" % (code, base, health.get("version"), seats[0].name))

    for _ in range(args.seats - 1):
        status, joined = call(base, "POST", "/api/rooms/%s/join" % code)
        if status != 200:
            raise SystemExit("join failed: %s %s" % (status, joined))
        seat = joined["seat"]
        seats.append(Seat(base, code, seat, joined["token"], joined["room"]["seats"][seat]["name"]))
        say("  seat %d: %s" % (seat, seats[-1].name))

    status, seated = call(base, "GET", "/api/rooms/" + code, seat=(0, seats[0].token))
    rules = seated["room"]["nextRules"]
    say("dealing for %d players: hand limit %d, %d Jester(s) in the tavern"
        % (rules["players"], rules["maxHand"], rules["jestersInTavern"]))

    status, body = call(base, "POST", "/api/rooms/%s/start" % code, {"expectedSeq": seated["seq"]}, seat=(0, seats[0].token))
    if status != 200:
        raise SystemExit("start failed: %s %s" % (status, body))

    # A spectator joins with no seat at all, to prove the watch path while playing.
    status, watcher = call(base, "POST", "/api/rooms/%s/watch" % code)
    if status == 200:
        if watcher["state"]["hand"]:
            raise SystemExit("a spectator was given a hand")
        say("  spectator watching (id %s...)" % watcher["watchId"][:8])

    actions = 0
    while actions < args.max_actions:
        actor, state = None, None
        for s in seats:
            snapshot = s.state()
            phase = snapshot["state"]["phase"]
            if phase in ("won", "lost"):
                if not quiet:
                    report(snapshot["state"], actions)
                return phase, snapshot["state"], actions
            if snapshot["state"]["legal"]:
                actor, state = s, snapshot["state"]
                break
        if actor is None:
            raise SystemExit("no seat has a legal action after %d actions" % actions)

        action = pick(state)
        payload = {"type": action["type"]}
        if action.get("cardIds"):
            payload["cardIds"] = action["cardIds"]
        if "seat" in action:
            payload["seat"] = action["seat"]
        actor.act(payload)
        actions += 1
        if args.verbose:
            say("  t%-3d seat %d (%s) %s" % (state["turnNo"], actor.index, actor.name,
                                             payload["type"] + " " + ",".join(map(str, payload.get("cardIds", [])))))
    return None


def main():
    args = parse_args()
    base = args.url.rstrip("/")
    if args.repeat > 1:
        return repeat(base, args)
    outcome = play(base, args, args.seed)
    if outcome is None:
        print("stopped after %d actions without an ending" % args.max_actions)
    return 0


def parse_args():
    ap = argparse.ArgumentParser()
    ap.add_argument("--url", default="http://127.0.0.1:8321")
    ap.add_argument("--seats", type=int, default=3, choices=range(2, 5))
    ap.add_argument("--theme", default="simple", choices=("simple", "8bit"))
    ap.add_argument("--seed", default="")
    ap.add_argument("--max-actions", type=int, default=400)
    ap.add_argument("--repeat", type=int, default=1,
                    help="run N fresh rooms and summarise (default 1)")
    ap.add_argument("--verbose", action="store_true")
    return ap.parse_args()


def repeat(base, args):
    """Run N rooms and summarise. A first test wants breadth, not one drama."""
    wins, deepest, lost_at, failures = 0, 0, [], []
    for i in range(args.repeat):
        seed = args.seed or ("DEMO-%02d" % i)
        print("--- run %d/%d (seed %s)" % (i + 1, args.repeat, seed))
        try:
            outcome = play(base, args, seed, quiet=True)
        except SystemExit as err:
            failures.append(str(err))
            continue
        if outcome is None:
            failures.append("no ending")
            continue
        phase, state, actions = outcome
        played = 12 - state["castleCount"]
        deepest = max(deepest, played)
        if phase == "won":
            wins += 1
        else:
            lost_at.append(played)
    print("\n%d runs, %d seats, %s deck: %d win(s), deepest castle %d/12"
          % (args.repeat, args.seats, args.theme, wins, deepest))
    if lost_at:
        print("  losses cleared %s castles (median %d)"
              % (lost_at, sorted(lost_at)[len(lost_at) // 2]))
    if failures:
        print("  %d run(s) did not finish: %s" % (len(failures), failures[:3]))
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
