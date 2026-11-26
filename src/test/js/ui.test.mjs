// Unit tests for the pure UI helpers. Run with: node --test src/test/js
// They import the very modules the browser loads, so a change here breaks the
// gate rather than only the UI.
import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';

// The shipped registry, read from disk: themes.js loads the same file over HTTP at
// runtime, so these assertions cover the real data rather than the built-in
// fallback.
const registryPath = fileURLToPath(new URL('../../regicide/server/web/data/themes.json', import.meta.url));
const registry = JSON.parse(readFileSync(registryPath, 'utf8'));

import {
  formatDuration, discardValueOf, paymentLeft, paymentAfter, SUIT_POWERS,
  paySelectionValid, paySelectionAfter, attributeSeats, seatColour, seatBubbles, bubbleFor, historyEntry, revealsFor, royalName, matchAwards,
} from '../../regicide/server/web/format.js';
import { themeInfo, backInfo, artUrl, backUrl, registryDefaultBack, jesterArt, themes, backs, themeJoker } from '../../regicide/server/web/themes.js';
import { suitSymbol, rankLabel } from '../../regicide/server/web/cards.js';

test('formatDuration renders m:ss and h:mm:ss', () => {
  assert.equal(formatDuration(0), '0:00');
  assert.equal(formatDuration(9), '0:09');
  assert.equal(formatDuration(65), '1:05');
  assert.equal(formatDuration(600), '10:00');
  assert.equal(formatDuration(3661), '1:01:01');
  assert.equal(formatDuration(undefined), '0:00');
  assert.equal(formatDuration(-5), '0:00');
});

test('discardValueOf prefers the server discard value', () => {
  assert.equal(discardValueOf({ id: '5H', value: 5, discardValue: 5 }), 5);
  // A future rule could make them differ; the client must follow the server.
  assert.equal(discardValueOf({ id: 'AH', value: 1, discardValue: 9 }), 9);
  assert.equal(discardValueOf({ id: 'AH', value: 1 }), 1);
  assert.equal(discardValueOf(null), 0);
});

test('paymentLeft counts down and never goes negative', () => {
  assert.equal(paymentLeft({ step4Required: 15, step4Paid: 0 }), 15);
  assert.equal(paymentLeft({ step4Required: 15, step4Paid: 6 }), 9);
  assert.equal(paymentLeft({ step4Required: 15, step4Paid: 15 }), 0);
  assert.equal(paymentLeft({ step4Required: 15, step4Paid: 20 }), 0);
  assert.equal(paymentLeft({}), 0);
});

test('paymentAfter shows what is still owed after the picked cards', () => {
  const state = { step4Required: 15, step4Paid: 5 }; // 10 left
  assert.equal(paymentAfter(state, [{ discardValue: 4 }]), 6);
  assert.equal(paymentAfter(state, [{ discardValue: 4 }, { discardValue: 6 }]), 0);
  assert.equal(paymentAfter(state, [{ discardValue: 12 }]), 0);
  assert.equal(paymentAfter(state, []), 10);
});

test('the shipped registry matches what the UI expects', () => {
  const ids = registry.themes.map((t) => t.id).sort();
  assert.deepEqual(ids, ['8bit', 'official', 'pokemon', 'simple']);
  const defaults = Object.fromEntries(registry.themes.map((t) => [t.id, t.defaultBack]));
  assert.deepEqual(defaults, {
    simple: 'red', pokemon: 'pokemon', '8bit': '8bit', official: 'official',
  });
  const byId = Object.fromEntries(registry.themes.map((t) => [t.id, t]));
  assert.deepEqual(byId.official.defaultJokers, ['official', 'official']);
  assert.deepEqual(byId.pokemon.defaultJokers, ['ditto', 'porygon']);
  assert.equal(byId.simple.defaultJokers, undefined, 'Simple asks for a random pair');
  assert.equal(byId['8bit'].defaultJokers, undefined, 'Standard asks for a random pair');
  const jokerIDs = registry.jokers.map((j) => j.id);
  for (const id of ['official', 'jester', 'ditto', 'porygon', 'bower', 'staff', 'ribbon', 'puppet']) {
    assert.ok(jokerIDs.includes(id), `${id} is in the catalogue`);
  }
  for (const retired of ['letterpress', 'house-clown', 'filigree', 'ruff', 'starlight', 'tambourine', 'bell', 'harlequin', 'vaudeville', 'minstrel', 'high-wheel']) {
    assert.ok(!jokerIDs.includes(retired), `${retired} is retired`);
  }
  const backIDs = registry.backs.map((b) => b.id);
  for (const t of registry.themes) {
    assert.ok(backIDs.includes(t.defaultBack), `${t.id} points at a real back`);
    assert.ok(t.dir && t.dir.startsWith('/img/'), `${t.id} has an image directory`);
  }
  assert.equal(registry.default, 'official');
  assert.equal(registry.defaultBack, 'black');
});

test('themeInfo falls back to a real theme for unknown ids', () => {
  const fallback = themeInfo('does-not-exist');
  assert.ok(fallback && fallback.id, 'fallback theme has an id');
  assert.equal(artUrl('does-not-exist', 'AS'), artUrl(fallback.id, 'AS'));
});

test('artUrl lowercases the card id and uses the theme directory', () => {
  const id = themeInfo(themes()[0].id).id;
  assert.match(artUrl(id, '10H'), /\/10h\.webp$/);
  assert.match(artUrl(id, 'KD'), /\/kd\.webp$/);
});

test('every registry back resolves to a file path', () => {
  for (const b of backs()) {
    assert.match(backUrl(b.id), /\.webp$/, `back ${b.id}`);
  }
  assert.match(backUrl('no-such-back'), /\.webp$/);
  assert.equal(registryDefaultBack(), backInfo(registryDefaultBack()).id);
});

test('jesterArt returns both Jesters from the catalogue', () => {
  // Node never calls loadThemes(), so this exercises the built-in fallback: two
  // identical faces from the joker catalogue, never a per-theme j1/j2 path. The
  // live registry (and an explicit choice) is covered by the browser smoke test.
  const [first, second] = jesterArt(themes()[0].id);
  assert.equal(first, second);
  assert.match(first, /^\/img\/jokers\/[a-z-]+\.webp$/);
  assert.match(jesterArt('official', 'no-such-face')[0], /^\/img\/jokers\//);
  assert.equal(themeJoker('pokemon'), themeJoker('pokemon'));
});

test('suitSymbol and rankLabel cover every suit and royal', () => {
  assert.equal(suitSymbol({ suit: 'spades' }), '♠');
  assert.equal(suitSymbol({ suit: 'hearts' }), '♥');
  assert.equal(suitSymbol({ suit: 'diamonds' }), '♦');
  assert.equal(suitSymbol({ suit: 'clubs' }), '♣');
  assert.equal(suitSymbol({ suit: 'none' }), '★');
  assert.equal(suitSymbol({ suit: 'bogus' }), '★');
  assert.equal(rankLabel(0), '★');
  for (const [rank, label] of [[1, 'A'], [10, '10'], [11, 'J'], [12, 'Q'], [13, 'K']]) {
    assert.equal(rankLabel(rank), label, `rank ${rank}`);
  }
});

test('the suit powers have one home and never go missing', () => {
  assert.deepEqual(SUIT_POWERS.map(([suit]) => suit), ['spades', 'hearts', 'diamonds', 'clubs']);
  for (const [, word] of SUIT_POWERS) assert.ok(word.length > 0);
});

test('joker catalogue: set pairs, no duplicate ids, the retired faces gone', () => {
  const jokers = registry.jokers || [];
  assert.ok(jokers.length >= 10, `${jokers.length} jokers`);
  const ids = jokers.map((j) => j.id);
  assert.equal(new Set(ids).size, ids.length, 'duplicate joker ids');
  for (const j of jokers) {
    assert.ok(j.path.startsWith('/img/jokers/'), `${j.id} path ${j.path}`);
    assert.ok(j.label && j.label.length > 0, `${j.id} has no label`);
    assert.ok(!j.label.includes('-'), `${j.id} label is a slug: ${j.label}`);
  }
  assert.ok(ids.includes(registry.defaultJoker), 'defaultJoker is not in the catalogue');
  // Official brings the Original twice; Pokémon brings Ditto and Porygon. Simple and
  // Standard ask the client for a pair, so they name none.
  const byId = Object.fromEntries(registry.themes.map((t) => [t.id, t]));
  assert.deepEqual(byId.official.defaultJokers, ['official', 'official']);
  assert.deepEqual(byId.pokemon.defaultJokers, ['ditto', 'porygon']);
  assert.equal(byId.simple.defaultJokers, undefined);
  assert.equal(byId['8bit'].defaultJokers, undefined);
  // Every named pair is real.
  for (const t of registry.themes) {
    for (const id of t.defaultJokers || []) {
      assert.ok(ids.includes(id), `${t.id} names ${id}`);
    }
  }
});

test('paySelection mirrors the engine rule for discarding several cards', () => {
  // Nothing selected, or a bad value, is never a payment.
  assert.equal(paySelectionValid(10, []), false);
  assert.equal(paySelectionValid(10, [undefined]), false);
  assert.equal(paySelectionValid(10, [-1]), false);

  // A batch may stop short of the total, and the last card may reach or pass it.
  assert.equal(paySelectionValid(17, [10, 7]), true);
  assert.equal(paySelectionValid(17, [10, 3]), true);
  assert.equal(paySelectionValid(17, [10, 10]), true); // the last card may overshoot
  assert.equal(paySelectionValid(10, [10]), true);

  // An earlier card may not already cover the attack (stop-at-threshold).
  assert.equal(paySelectionValid(10, [10, 2]), false);
  assert.equal(paySelectionValid(12, [12, 5]), false);
  assert.equal(paySelectionValid(12, [10, 5]), true); // 10 alone does not cover 12
  assert.equal(paySelectionValid(12, [10, 1, 5]), true);

  // Over-paying allowed: any batch is legal and the player ends it themselves.
  assert.equal(paySelectionValid(10, [10, 2], true), true);
  assert.equal(paySelectionValid(10, [20], true), true);

  assert.equal(paySelectionAfter(17, [10, 7]), 0);
  assert.equal(paySelectionAfter(17, [10, 3]), 4);
});

test('the host crown marks exactly one seat, and hand size reads against the limit', async () => {
  // seatSummary is the rail's whole data model without a DOM, which is what makes
  // this testable in node. The DOM shape is pinned by the browser screenshots.
  const { seatSummary, CROWN_PATH } = await import('../../regicide/server/web/coop.js');
  assert.equal(CROWN_PATH, '/img/brand/crown.svg', 'the host uses the brand crown');

  const room = {
    code: 'ABCDE', hostSeat: 1, capacity: 4, players: 3, lobby: false,
    seats: [
      { seat: 0, name: 'Agnes', ready: true, you: false, away: false },
      { seat: 1, name: 'Victoria', ready: true, you: true, away: false },
      { seat: 2, name: 'Pepin', ready: true, you: false, away: true },
    ],
    nextRules: { players: 3, maxHand: 6, jestersInTavern: 1 },
  };
  const state = {
    players: [
      { seat: 0, name: 'Agnes', handCount: 4, handLimit: 6, active: false },
      { seat: 1, name: 'Victoria', handCount: 6, handLimit: 6, active: true },
      { seat: 2, name: 'Pepin', handCount: 2, handLimit: 6, active: false },
    ],
  };

  const rows = room.seats.map((seat) => seatSummary(room, state, seat));
  assert.deepEqual(rows.map((r) => r.host), [false, true, false], 'one host');
  assert.deepEqual(rows.map((r) => r.count), ['4/6', '6/6', '2/6']);
  assert.deepEqual(rows.map((r) => r.handFull), [false, true, false]);
  assert.deepEqual(rows.map((r) => r.active), [false, true, false]);
  assert.equal(rows[2].away, true);
  assert.equal(rows[1].you, true);

  // Before the deal there are no counts, and the rail says so rather than 0/0.
  const undealt = room.seats.map((seat) => seatSummary(room, null, seat));
  assert.deepEqual(undealt.map((r) => r.count), ['—', '—', '—']);
  assert.deepEqual(undealt.map((r) => r.handFull), [false, false, false]);
});

test('a spectator reads the watch endpoint, a seat reads the room', async () => {
  // This is the bug that froze the first watched game: a spectator polling the seat
  // route with only a watch id gets 401 forever, so the board never updates.
  const { roomPath } = await import('../../regicide/server/web/coop.js');
  assert.equal(roomPath('G23MX', false, -1), '/api/rooms/G23MX');
  assert.equal(roomPath('G23MX', true, -1), '/api/rooms/G23MX/watch');
  assert.equal(roomPath('G23MX', true, 0), '/api/rooms/G23MX/watch?as=0');
  assert.equal(roomPath('G23MX', true, 2), '/api/rooms/G23MX/watch?as=2');
});

test('a co-op log attributes every line to the seat that acted', () => {
  // Only plays carry a seat; a draw or a shield is a consequence of that play and
  // still belongs to the same player on screen.
  const log = [
    { type: 'setup', turn: 0 },
    { type: 'enemy_reveal', turn: 1, seat: 0 },
    { type: 'play', turn: 1, seat: 0, cards: ['9C'] },
    { type: 'damage', turn: 1 },
    { type: 'draw', turn: 1 },
    { type: 'turn_end', turn: 1, seat: 1 },
    { type: 'play', turn: 2, seat: 1, cards: ['5H'] },
    { type: 'shield', turn: 2 },
    { type: 'yield', turn: 3, seat: 2 },
    { type: 'enemy_attack', turn: 3 },
  ];
  assert.deepEqual(attributeSeats(log), [null, 0, 0, 0, 0, null, 1, 1, 2, 2]);

  // A diamond draw round-robin names each recipient, and the lines that follow a draw
  // still belong to the seat that played the diamond. The draw used to move the
  // carried actor to the last seat drawn, so the next player's shield and the royal's
  // defeat carried the wrong dot (note 10).
  const roundRobin = [
    { type: 'enemy_reveal', turn: 1, seat: 0 },
    { type: 'play', turn: 1, seat: 0, cards: ['9D'] },
    { type: 'draw', turn: 1, seat: 1 },
    { type: 'draw', turn: 1, seat: 2 },
    { type: 'draw', turn: 1, seat: 1 },
    { type: 'shield', turn: 1 },
    { type: 'defeat', turn: 1 },
  ];
  assert.deepEqual(attributeSeats(roundRobin), [0, 0, 1, 2, 1, 0, 0]);

  // turn_end is dropped from the history and must not re-attribute anything.
  assert.deepEqual(attributeSeats([{ type: 'turn_end', turn: 1, seat: 3 }]), [null]);
  // Robust against an empty or absent log.
  assert.deepEqual(attributeSeats([]), []);
  assert.deepEqual(attributeSeats(undefined), []);

  // Seat colours are one order for the whole UI: rail, orbs, log.
  assert.deepEqual(
    [0, 1, 2, 3, 4, -1, null].map(seatColour),
    ['red', 'blue', 'green', 'black', '', '', ''],
  );
});

test('a seat bubble names the action and the cards behind it', () => {
  // A play is one event; a payment is one event per card, so the bubble must collect
  // the whole batch rather than report only its last card.
  const log = [
    { type: 'setup', turn: 0 },                                             // 0
    { type: 'enemy_reveal', turn: 1, seat: 0 },                             // 1
    { type: 'play', turn: 1, seat: 0, cards: ['9S'], amount: 9 },           // 2
    { type: 'shield', turn: 1 },                                            // 3
    { type: 'damage', turn: 1, amount: 9 },                                 // 4
    { type: 'suffer', turn: 1, cards: ['8C'], amount: 8, note: 'paid 8/9' },  // 5
    { type: 'suffer', turn: 1, cards: ['7H'], amount: 7, note: 'paid 15/9' }, // 6
    { type: 'play', turn: 2, seat: 1, cards: ['JH'], amount: 10 },          // 7
    { type: 'play', turn: 2, seat: 1, cards: ['J1'], amount: 0, note: 'jester: immunity cancelled' }, // 8
    { type: 'yield', turn: 3, seat: 2 },                                    // 9
  ];
  const actors = attributeSeats(log);
  assert.deepEqual(actors.slice(0, 3), [null, 0, 0]);

  // A single card prints its own value, so the bubble adds no number of its own.
  assert.deepEqual(bubbleFor(log, actors, 2), {
    seat: 0, text: 'Attacked with', cards: ['9S'], detail: '',
  });
  // Payment: captured from its last card, reported as the pair.
  assert.deepEqual(bubbleFor(log, actors, 6), {
    seat: 0, text: 'Defended with', cards: ['8C', '7H'], detail: '',
  });
  // A combo does need its total.
  const combo = [
    { type: 'play', turn: 1, seat: 0, cards: ['9S', '9H'], amount: 18 },
  ];
  assert.equal(bubbleFor(combo, attributeSeats(combo), 0).detail, '18');
  // A Jester is not an attack.
  assert.equal(bubbleFor(log, actors, 8).text, 'Cancelled immunity');
  assert.equal(bubbleFor(log, actors, 9).text, 'Yielded');

  // A defence is its own action (no play before it), and still reads correctly.
  const defence = [
    { type: 'suffer', turn: 4, seat: 1, cards: ['8C'], amount: 8, note: 'paid 8/9' },
    { type: 'suffer', turn: 4, seat: 1, cards: ['7H'], amount: 7, note: 'paid 15/9' },
    { type: 'enemy_attack', turn: 4, amount: 9, note: 'defended' },
    { type: 'turn_end', turn: 4, seat: 1 },
  ];
  assert.deepEqual(bubbleFor(defence, attributeSeats(defence), 1), {
    seat: 1, text: 'Defended with', cards: ['8C', '7H'], detail: '',
  });

  // One bubble per seat, newest wins: seat 0 last defended, so that is what shows.
  const bubbles = seatBubbles(log);
  assert.equal(bubbles.get(0).text, 'Defended with');
  assert.deepEqual(bubbles.get(0).cards, ['8C', '7H']);
  assert.equal(bubbles.get(1).text, 'Cancelled immunity');
  assert.equal(bubbles.get(2).text, 'Yielded');

  // Non-actions produce nothing at all.
  assert.equal(bubbleFor(log, actors, 1), null);
  assert.equal(bubbleFor(log, actors, 3), null);
  assert.equal(bubbleFor([], [], 0), null);
  assert.equal(bubbleFor(undefined, undefined, 0), null);
});

test('history copy names the recipient, the shortage and the next actor', () => {
  const state = {
    numPlayers: 2,
    players: [{ seat: 0, name: 'Bors' }, { seat: 1, name: 'Ysolde' }],
    rules: { passOnDefeat: false },
  };
  // A draw names who received the cards: "you drew 2" was ambiguous at a table (note 10).
  assert.equal(historyEntry({ type: 'draw', seat: 1, amount: 2 }, state).text, 'Ysolde drew 2 cards');
  assert.equal(historyEntry({ type: 'draw', seat: 1, amount: 1 }, state).text, 'Ysolde drew 1 card');
  // Solo keeps the old, shorter line.
  assert.equal(historyEntry({ type: 'draw', seat: 0, amount: 2 }, { numPlayers: 1 }).impact, '2 cards');

  // Hearts says how many actually went back, and what the card asked for.
  const short = historyEntry({ type: 'hearts', cards: ['5H'], amount: 3, note: 'requested 5' }, state);
  assert.equal(short.impact, '3 cards returned under the tavern (5 asked — the discard pile was short)');
  const full = historyEntry({ type: 'hearts', cards: ['5H'], amount: 5, note: 'requested 5' }, state);
  assert.equal(full.impact, '5 cards returned under the tavern');

  // The killing blow explains who faces the next royal, because a repeated turn
  // without an explanation reads as a bug (note 3).
  const kept = historyEntry({ type: 'defeat', cards: ['JD'], note: 'exact' }, state);
  assert.match(kept.impact, /the same player faces the next royal/);
  assert.match(kept.impact, /on top of the tavern/);
  const passed = historyEntry({ type: 'defeat', cards: ['JD'], note: 'discard' }, { ...state, rules: { passOnDefeat: true } });
  assert.match(passed.impact, /the turn passes to the next seat/);
  assert.match(passed.impact, /discard pile/);
});

test('a paid card line never prints undefined', () => {
  // The toast read "forfeited undefined" when a suffer event carried no amount
  // (campaign B12). The history line must degrade to zero, not the word.
  const line = historyEntry({ type: 'suffer', cards: ['2H'] }, { numPlayers: 1 });
  assert.ok(line);
  assert.ok(!/undefined/.test(JSON.stringify(line)), JSON.stringify(line));
});

test('a Jester choice in the log names the seat it picked', () => {
  const state = { players: [{ seat: 0, name: 'Jane' }, { seat: 1, name: 'Edith' }], numPlayers: 2 };
  const line = historyEntry({ type: 'choose_next_seat', seat: 0, amount: 1, note: 'seat chosen' }, state);
  assert.equal(line.text, 'Chose Edith to go next');
});

test('revealsFor gathers a whole attack, a whole defence, and the Jester', () => {
  // A play is one event; the damage that follows is not a second reveal.
  const playLog = [
    { type: 'play', seat: 1, cards: ['7S', 'AH'], amount: 9 },
    { type: 'damage', amount: 9 },
  ];
  assert.deepEqual(revealsFor(playLog, attributeSeats(playLog))[0], {
    kind: 'attack', seat: 1, cards: ['7S', 'AH'],
  });

  // A payment is one suffer event per card: one reveal with every card, in order.
  // The suffer events carry the payer, because a payment is its own action: without
  // the seat the defense reveal was dropped and the bubble kept the last attack.
  const pay = [
    { type: 'suffer', seat: 2, cards: ['8C'] },
    { type: 'suffer', seat: 2, cards: ['7H'] },
    { type: 'enemy_attack', amount: 10, note: 'defended' },
  ];
  assert.deepEqual(revealsFor(pay, attributeSeats(pay)), [{
    kind: 'defend', seat: 2, cards: ['8C', '7H'],
  }]);

  // A Jester has its own symbol; it is still a play with a seat.
  const jesterLog = [{ type: 'play', seat: 2, cards: ['J1'], note: 'jester: immunity cancelled' }];
  assert.deepEqual(revealsFor(jesterLog, attributeSeats(jesterLog))[0], {
    kind: 'jester', seat: 2, cards: ['J1'],
  });

  assert.deepEqual(revealsFor([{ type: 'damage' }], [0]), []);
});

test('revealsFor orders a royal defeat and the next royal entering', () => {
  // The engine logs the defeat, the recruit and the next reveal in one action. The
  // table must hear them in that order, after the blow that caused them.
  const log = [
    { type: 'play', seat: 1, cards: ['10C'], amount: 20 },
    { type: 'damage', amount: 20, note: 'total 20' },
    { type: 'defeat', cards: ['JS'], amount: 20, note: 'exact' },
    { type: 'recruit', cards: ['JS'] },
    { type: 'enemy_reveal', cards: ['JH'], seat: 1 },
  ];
  const items = revealsFor(log, attributeSeats(log));
  assert.deepEqual(items.map((i) => i.kind), ['attack', 'defeat', 'enter']);
  // Royal news carries no seat: a defeat is the table's, not the striker's.
  assert.deepEqual(items[1], { kind: 'defeat', seat: null, cards: ['JS'], royal: true });
  assert.deepEqual(items[2], { kind: 'enter', seat: null, cards: ['JH'], royal: true });

  // The last royal ends the game: a defeat with no reveal after it.
  const last = [{ type: 'defeat', cards: ['KS'], amount: 40 }, { type: 'win', note: 'gold' }];
  assert.deepEqual(revealsFor(last, attributeSeats(last)).map((i) => i.kind), ['defeat']);
});

test('royalName is the royal fallback before cardtext loads', () => {
  assert.equal(royalName('JS'), 'Jack of Spades');
  assert.equal(royalName('QD'), 'Queen of Diamonds');
  assert.equal(royalName('KH'), 'King of Hearts');
  // A non-royal keeps its id rather than printing "undefined".
  assert.equal(royalName('10C'), '10C');
  assert.equal(royalName(''), '');
});


test('matchAwards credits damage, draws, royals and Jesters to the right seats', () => {
  const log = [
    { type: 'play', seat: 0, cards: ['10S'] },
    { type: 'damage', amount: 10 },
    { type: 'play', seat: 1, cards: ['5H', '5D'] },
    { type: 'draw', seat: 1, amount: 5 },
    { type: 'play', seat: 2, cards: ['J1'] },
    { type: 'defeat', cards: ['JS'] },
    { type: 'play', seat: 0, cards: ['9S'] },
    { type: 'damage', amount: 9 },
    { type: 'defeat' },
  ];
  const awards = matchAwards(log, 3);
  const byKey = Object.fromEntries(awards.map((a) => [a.key, a]));
  assert.deepEqual(byKey.damage, { key: 'damage', label: 'Most Damage', amount: 19, seats: [0] });
  assert.deepEqual(byKey.drawn, { key: 'drawn', label: 'Most Drawn', amount: 5, seats: [1] });
  assert.deepEqual(byKey.slain, { key: 'slain', label: 'Most Slain Royals', amount: 1, seats: [0, 2] });
  assert.deepEqual(byKey.jesters, { key: 'jesters', label: 'Most Jesters', amount: 1, seats: [2] });
  const empty = matchAwards([], 3);
  assert.ok(empty.every((a) => a.amount === 0 && a.seats.length === 0));
});
