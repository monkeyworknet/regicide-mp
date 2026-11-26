#!/usr/bin/env python3
"""One co-op seat, driven from the command line.

This is the tool a human seat would otherwise be: it creates or joins a room, waits
for the host to start, and then plays one seat sensibly at human speed. It exists so a
table can be exercised (or demonstrated) without four browsers, and so each player can
be a separate process with its own seat token.

    coop-seat.py host   --out /tmp/coop/room.json
    coop-seat.py join   --room /tmp/coop/room.json --out /tmp/coop/seat1.json
    coop-seat.py start  --room /tmp/coop/room.json --seats 3
    coop-seat.py play   --seat-file /tmp/coop/seat1.json --speed 3-6
    coop-seat.py watch  --room /tmp/coop/room.json --as 0     # spectator view

Every call is the same HTTP API the browser uses; nothing here is privileged.
"""

import argparse
import json
import os
import random
import sys
import time
import urllib.error
import urllib.request


def call(base, method, path, body=None, seat=None, watch=None):
    data = json.dumps(body).encode() if body is not None else None
    req = urllib.request.Request(base + path, method=method, data=data,
                                 headers={"Content-Type": "application/json"})
    if seat:
        req.add_header("X-Regicide-Seat", "%d:%s" % (seat[0], seat[1]))
    if watch:
        req.add_header("X-Regicide-Watch", watch)
    try:
        with urllib.request.urlopen(req, timeout=15) as resp:
            raw = resp.read()
            return resp.status, (json.loads(raw) if raw else {})
    except urllib.error.HTTPError as err:
        raw = err.read()
        return err.code, (json.loads(raw) if raw else {})


def load(path):
    with open(path) as fh:
        return json.load(fh)


def save(path, payload):
    tmp = path + ".tmp"
    with open(tmp, "w") as fh:
        json.dump(payload, fh)
    os.replace(tmp, path)


# ---------------------------------------------------------------- strategy

def legal_of(state):
    return state.get("legal") or []


def hand_map(state):
    return {c["id"]: c for c in state.get("hand") or []}


def incoming(state):
    return state.get("incoming", 0)


def remaining_health(state):
    enemy = state.get("enemy") or {}
    return max(0, enemy.get("value", 0) * 2 - state.get("damage", 0)) if enemy else 0


def play_attack_estimate(action, state):
    """The server already prices each legal play, so trust it and add the judgement a
    player makes: does this kill the enemy, does it cover the attack, is it cheap."""
    return action.get("attack", 0)


def choose(state):
    """Pick a move the way a careful human would.

    Order of concerns: exact kill > cover the incoming attack > value per card spent,
    with the suit powers that are actually live (clubs double, spades shield, hearts
    recycle the discard) and one rule about Jesters: a Jester is worth playing when it
    cancels an immunity the table cannot otherwise use.
    """
    legal = legal_of(state)
    if not legal:
        return None
    by = lambda kind: [a for a in legal if a["type"] == kind]

    # A Jester asks who goes next: give the turn to the seat holding most cards.
    if by("choose_next_seat"):
        counts = {p["seat"]: p["handCount"] for p in state.get("players") or []}
        return max(by("choose_next_seat"), key=lambda a: counts.get(a["seat"], 0))

    if by("end_payment"):
        return by("end_payment")[0]

    hand = hand_map(state)
    enemy = state.get("enemy") or {}
    enemy_suit = (enemy.get("suit") or "").lower()
    immune = bool(enemy_suit) and not state.get("immunityCancelled")
    need = incoming(state)
    health_left = remaining_health(state)

    plays = by("play")
    if plays:
        hand_value = sum(c.get("discardValue", 0) for c in hand.values())

        def score(a):
            attack = a.get("attack", 0)
            ids = a.get("cardIds") or []
            cards = [hand[i] for i in ids if i in hand]
            suits = {(c.get("suit") or "").lower() for c in cards}
            value = 0.0
            # An exact kill is the best move in the game.
            if attack and health_left and attack == health_left:
                value += 100
            # Covering the attack means nobody pays this turn.
            if need and attack >= need:
                value += 40 + (attack - need) * 0.1
            # Otherwise raw damage, with a nudge for the damage it does not over-spend.
            value += attack
            # Suit powers that are live right now.
            if "clubs" in suits and not (enemy_suit == "clubs" and immune):
                value += 4
            if "hearts" in suits:
                value += 2
            if "spades" in suits and need >= 8:
                value += 3
            # Every card spent is a card that cannot pay a later attack.
            spent = sum(c.get("discardValue", 0) for c in cards)
            value -= spent * 0.5
            # The trap that ended the first scripted game: spend the hand on damage,
            # then be unable to pay the attack this very turn, because the payment
            # comes out of the same hand. Unless the play kills the enemy, the hand
            # must still cover what is coming.
            will_kill = bool(attack) and health_left and attack >= health_left
            if need and not will_kill and (hand_value - spent) < need:
                value -= 60
            # A Jester is a resource: use it when the immunity blocks the table.
            if any(c.get("rank") == 0 for c in cards):
                holding_blocked = enemy_suit and any(
                    (c.get("suit") or "").lower() == enemy_suit for c in hand.values()
                )
                value = 30 if (immune and holding_blocked) else -25
            return value

        best = max(plays, key=score)
        if score(best) > 0:
            return best

    payments = by("pay_damage")
    if payments:
        # Pay with the smallest cards: the hand is the only thing that survives.
        def cost(a):
            cid = (a.get("cardIds") or [None])[0]
            return hand.get(cid, {}).get("discardValue", 99)
        cheapest = sorted(payments, key=cost)
        return cheapest[0]

    if by("yield"):
        # Yielding skips the attack only when the hand can still pay afterwards; the
        # server prices the payment, so yield when every play is worth less than the
        # cheapest payment we would otherwise make.
        return by("yield")[0]
    return legal[0]


def describe(state, action):
    ids = ",".join(action.get("cardIds") or [])
    seat = action.get("seat")
    kind = action["type"]
    if kind == "choose_next_seat":
        return "asks for seat %s to go next" % seat
    if kind == "play":
        return "plays %s (%d damage)" % (ids, action.get("attack", 0))
    if kind == "pay_damage":
        return "pays %s" % ids
    return kind


# ---------------------------------------------------------------- commands

def cmd_host(args):
    theme = args.theme
    status, room = call(args.url, "POST", "/api/rooms", {"theme": theme})
    if status != 201:
        raise SystemExit("could not create the room: %s %s" % (status, room))
    seat = (room["seat"], room["token"])
    # Spectators on, hands visible: a game played out in the open is the point here.
    status, _ = call(args.url, "POST", "/api/rooms/%s/watching" % room["code"],
                     {"allowed": True, "hands": True}, seat=seat)
    if status != 200:
        raise SystemExit("could not enable spectators: %s" % status)
    info = {
        "url": args.url,
        "code": room["code"],
        "seat": room["seat"],
        "token": room["token"],
        "name": room["room"]["seats"][room["seat"]]["name"],
        "join": "/?room=%s" % room["code"],
        "watch": "/?room=%s&watch=1" % room["code"],
        "theme": theme,
    }
    save(args.out, info)
    print(json.dumps(info, indent=2))
    print("\ninvite:    %s%s" % (args.url, info["join"]))
    print("spectate:  %s%s" % (args.url, info["watch"]))
    return 0


def cmd_join(args):
    room = load(args.room)
    status, joined = call(args.url or room["url"], "POST", "/api/rooms/%s/join" % room["code"])
    if status != 200:
        raise SystemExit("could not join: %s %s" % (status, joined))
    seat = joined["seat"]
    info = {
        "url": args.url or room["url"],
        "code": room["code"],
        "seat": seat,
        "token": joined["token"],
        "name": joined["room"]["seats"][seat]["name"],
    }
    save(args.out, info)
    print(json.dumps(info, indent=2))
    print("joined as seat %d (%s)" % (seat, info["name"]))
    return 0


def cmd_start(args):
    room = load(args.room)
    seat = (room["seat"], room["token"])
    deadline = time.time() + args.timeout
    while time.time() < deadline:
        status, body = call(room["url"], "GET", "/api/rooms/%s" % room["code"], seat=seat)
        if status != 200:
            raise SystemExit("cannot read the room: %s %s" % (status, body))
        seated = len(body["room"]["seats"])
        print("  %d/%d seated" % (seated, body["room"]["capacity"]))
        if seated >= args.seats:
            break
        time.sleep(2)
    else:
        raise SystemExit("only %d seats filled after %ds" % (seated, args.timeout))
    status, body = call(room["url"], "POST", "/api/rooms/%s/start" % room["code"], {"expectedSeq": body["seq"]}, seat=seat)
    if status != 200:
        raise SystemExit("start refused: %s %s" % (status, body))
    print("started: %d players, hand limit %d" % (len(body["room"]["seats"]), body["state"]["maxHand"]))
    return 0


def cmd_play(args):
    me = load(args.seat_file)
    seat = (me["seat"], me["token"])
    lo, _, hi = args.speed.partition("-")
    lo, hi = float(lo), float(hi or lo)
    rng = random.Random()
    actions = 0
    while actions < args.max_actions:
        status, body = call(me["url"], "GET", "/api/rooms/%s" % me["code"], seat=seat)
        if status == 401:
            raise SystemExit("seat %d lost its token (taken over?)" % me["seat"])
        if status == 404 or status == 410:
            raise SystemExit("the room has ended")
        state = body.get("state") or {}
        phase = state.get("phase")
        if phase in ("won", "lost"):
            return report(me, state, actions)
        legal = legal_of(state)
        if not legal:
            time.sleep(1.0)
            continue
        action = choose(state)
        payload = {"type": action["type"]}
        if action.get("cardIds"):
            payload["cardIds"] = action["cardIds"]
        if "seat" in action:
            payload["seat"] = action["seat"]
        # Human speed: a beat to look at the board before every move.
        time.sleep(rng.uniform(lo, hi))
        status, out = call(me["url"], "POST", "/api/rooms/%s/actions" % me["code"],
                           {"actionId": "%d-%d" % (me["seat"], actions + 1), "action": payload},
                           seat=seat)
        if status != 200:
            print("  rejected %s: %s %s" % (payload["type"], status, out.get("error")))
            time.sleep(1.0)
            continue
        actions += 1
        print("  t%-3d %s %s" % (state.get("turnNo", 0), me["name"], describe(state, action)), flush=True)
    print("%s: %d actions, game still running" % (me["name"], actions))
    return 0


def report(me, state, actions):
    phase = state["phase"]
    rail = ", ".join("%s %d/%d" % (p["name"], p["handCount"], p["handLimit"])
                     for p in state.get("players") or [])
    print("\n%s for %s after %d actions" % (phase.upper(), me["name"], actions))
    print("  turn %d, castle %d, damage %d" % (state.get("turnNo", 0), state.get("castleCount", 0), state.get("damage", 0)))
    print("  seats: %s" % rail)
    if phase == "lost":
        print("  lost: %s (seat %s)" % (state.get("lostReason"), state.get("lostSeat", 0)))
    return 0 if phase == "won" else 2


def cmd_watch(args):
    room = load(args.room)
    status, joined = call(room["url"], "POST", "/api/rooms/%s/watch" % room["code"])
    if status != 200:
        raise SystemExit("cannot watch: %s %s" % (status, joined))
    watch = joined["watchId"]
    status, body = call(room["url"], "GET", "/api/rooms/%s/watch?as=%d" % (room["code"], args.as_seat), watch=watch)
    if status != 200:
        raise SystemExit("cannot read as seat %d: %s" % (args.as_seat, status))
    state = body.get("state") or {}
    print("watching %s as seat %s: watchingAs=%s, hand=%s" % (
        room["code"], args.as_seat, body.get("watchingAs"),
        [c["id"] for c in state.get("hand") or []]))
    return 0


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--url", default="http://127.0.0.1:8321")
    sub = ap.add_subparsers(dest="cmd", required=True)

    p = sub.add_parser("host")
    p.add_argument("--out", default="/tmp/coop/room.json")
    p.add_argument("--theme", default="simple", choices=("simple", "8bit"))

    p = sub.add_parser("join")
    p.add_argument("--room", default="/tmp/coop/room.json")
    p.add_argument("--out", required=True)

    p = sub.add_parser("start")
    p.add_argument("--room", default="/tmp/coop/room.json")
    p.add_argument("--seats", type=int, default=3)
    p.add_argument("--timeout", type=int, default=240)

    p = sub.add_parser("play")
    p.add_argument("--seat-file", required=True)
    p.add_argument("--speed", default="3-6", help="seconds between moves, min-max")
    p.add_argument("--max-actions", type=int, default=400)

    p = sub.add_parser("watch")
    p.add_argument("--room", default="/tmp/coop/room.json")
    p.add_argument("--as", dest="as_seat", type=int, default=0)

    args = ap.parse_args()
    return {
        "host": cmd_host, "join": cmd_join, "start": cmd_start,
        "play": cmd_play, "watch": cmd_watch,
    }[args.cmd](args)


if __name__ == "__main__":
    sys.exit(main())
