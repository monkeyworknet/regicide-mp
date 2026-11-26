// Unit tests for the screen copy pools and the two fixed result lines. These
// import the module the browser loads, so the assertions cover the real strings.
import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';

import {
  HEADLINES, LEDES, VICTORIES, LOSSES, NO_ANSWER,
  word, pickLine, resultHeading, wonLine, defeatLine,
} from '../../regicide/server/web/voice.js';

const pools = { HEADLINES, LEDES, VICTORIES, LOSSES };

const indexHTML = readFileSync(
  fileURLToPath(new URL('../../regicide/server/web/index.html', import.meta.url)),
  'utf8',
);

test('every copy pool is deep enough to rotate without repeating', () => {
  for (const [name, pool] of Object.entries(pools)) {
    assert.ok(pool.length >= 5, `${name} holds only ${pool.length} lines`);
  }
});

test('copy pool entries are unique, trimmed and free of double spaces', () => {
  for (const [name, pool] of Object.entries(pools)) {
    assert.equal(new Set(pool).size, pool.length, `${name} has a duplicate`);
    for (const line of pool) {
      assert.equal(line, line.trim(), `${name} entry has stray whitespace: ${JSON.stringify(line)}`);
      assert.ok(!/\s{2}/.test(line), `${name} entry has a double space: ${JSON.stringify(line)}`);
      assert.ok(line.length > 0, `${name} entry is empty`);
    }
  }
});

test('menu lines are sentences and result headings are not', () => {
  for (const line of [...HEADLINES, ...LEDES]) {
    assert.ok(line.endsWith('.'), `menu line should end in a full stop: ${line}`);
  }
  for (const line of [...VICTORIES, ...LOSSES]) {
    assert.ok(!line.endsWith('.'), `result heading should carry no full stop: ${line}`);
  }
});

test('no victory title claims the word victory above the tier line', () => {
  for (const line of VICTORIES) {
    assert.ok(!/victory/i.test(line), `victory title repeats the tier word: ${line}`);
  }
});

test('resultHeading picks from the pool that matches the outcome', () => {
  assert.equal(resultHeading(true, () => 0), VICTORIES[0]);
  assert.equal(resultHeading(false, () => 0), LOSSES[0]);
  assert.ok(VICTORIES.includes(resultHeading(true, () => 0.62)));
  assert.ok(LOSSES.includes(resultHeading(false, () => 0.62)));
});

test('neither result pool claims the word on the other screen', () => {
  for (const line of LOSSES) {
    assert.ok(!/victory/i.test(line), `loss heading mentions victory: ${line}`);
  }
});

test('no loss heading names a specific loss reason', () => {
  // The heading is drawn before the reason is known to the pool, so a line that
  // says "ran out of cards" would caption a shortfall loss wrongly.
  for (const line of LOSSES) {
    assert.ok(!/ran out|empty|could not pay|no cards/i.test(line), `loss heading names a reason: ${line}`);
  }
});

test('victory and loss headings never collide', () => {
  // The two pools are the two halves of the same sentence; a shared line would
  // make a win and a loss indistinguishable at a glance.
  const overlap = VICTORIES.filter((line) => LOSSES.includes(line));
  assert.deepEqual(overlap, []);
});

test('pickLine draws the entry the injected random selects', () => {
  assert.equal(pickLine(['a', 'b', 'c'], () => 0), 'a');
  assert.equal(pickLine(['a', 'b', 'c'], () => 0.5), 'b');
  assert.equal(pickLine(['a', 'b', 'c'], () => 0.999999), 'c');
  assert.equal(pickLine(['a'], () => 1), 'a', 'a rand of 1 must not read past the end');
  assert.equal(pickLine([], () => 0.5), '', 'an empty pool renders nothing');
  assert.equal(pickLine(null), '');
});

test('every pool entry is reachable from some draw', () => {
  for (const [name, pool] of Object.entries(pools)) {
    const seen = new Set();
    for (let i = 0; i < pool.length; i += 1) {
      seen.add(pickLine(pool, () => (i + 0.5) / pool.length));
    }
    assert.equal(seen.size, pool.length, `${name} has an unreachable entry`);
  }
});

test('word spells the shortfalls that can actually occur', () => {
  assert.equal(word(0), 'no');
  assert.equal(word(9), 'nine');
  assert.equal(word(11), 'eleven');
  assert.equal(word(20), 'twenty');
  assert.equal(word(21), '21', 'past twenty the number is printed as digits');
  assert.equal(word(-3), 'no');
});

test('wonLine reports turns and Jesters spent', () => {
  assert.equal(wonLine({ turnNo: 34, jestersUsed: 2 }), 'Twelve down in 34 turns. Two Jesters spent.');
  assert.equal(wonLine({ turnNo: 27, jestersUsed: 1 }), 'Twelve down in 27 turns. One Jester spent.');
  assert.equal(wonLine({ turnNo: 19, jestersUsed: 0 }), 'Twelve down in 19 turns. No Jesters spent.');
  assert.equal(wonLine({ turnNo: 1, jestersUsed: 1 }), 'Twelve down in 1 turn. One Jester spent.');
  assert.equal(wonLine({}), 'Twelve down in 0 turns. No Jesters spent.');
  // Co-op: a Jester is a card in a play, not the solo flip.
  assert.equal(
    wonLine({ turnNo: 45, jestersUsed: 0 }, { coop: true, jesters: 2 }),
    'Twelve down in 45 turns. Two Jesters played.',
  );
  assert.equal(
    wonLine({ turnNo: 45, jestersUsed: 0 }, { coop: true, jesters: 0 }),
    'Twelve down in 45 turns. No Jesters played.',
  );
});

test('defeatLine quotes the shortfall in words', () => {
  const lost = {
    lostReason: 'cannot_defend',
    step4Required: 20,
    step4Paid: 0,
    hand: [{ discardValue: 2 }, { discardValue: 7 }],
  };
  assert.equal(defeatLine(lost), 'Short by eleven.');
  assert.equal(defeatLine({ ...lost, step4Paid: 6 }), 'Short by five.');
  assert.equal(defeatLine({ ...lost, hand: [] }), 'Short by twenty.');
});

test('defeatLine falls back to the plain loss line when nothing is owed', () => {
  const odd = { lostReason: 'cannot_defend', step4Required: 5, step4Paid: 0, hand: [{ discardValue: 9 }] };
  assert.equal(defeatLine(odd), NO_ANSWER);
  assert.equal(defeatLine({ lostReason: 'cannot_act', hand: [] }), NO_ANSWER);
  assert.equal(defeatLine(null), NO_ANSWER);
});

test('the no-JS fallback in index.html is one of the pool lines', () => {
  // index.html ships a headline and a lede for a browser that never runs app.js.
  // If a pool edit forgets them, the file claims copy the pools no longer hold.
  const headline = indexHTML.match(/<h1>([^<]+)<\/h1>/);
  const lede = indexHTML.match(/<p class="lede">([^<]+)<\/p>/);
  assert.ok(headline, 'index.html should carry a static headline');
  assert.ok(lede, 'index.html should carry a static lede');
  assert.ok(HEADLINES.includes(headline[1]), `static headline is not a pool line: ${headline[1]}`);
  assert.ok(LEDES.includes(lede[1]), `static lede is not a pool line: ${lede[1]}`);
});

test('defeatLine matches the engine defensive loss that pays part of the attack', () => {
  // transition.go can lose after a partial payment; the shortfall is then
  // (required - paid) - hand, exactly as the engine compares them.
  const partial = {
    lostReason: 'cannot_defend',
    step4Required: 20,
    step4Paid: 12,
    hand: [{ discardValue: 3, value: 3 }, { discardValue: 4, value: 4 }],
  };
  assert.equal(defeatLine(partial), 'Short by one.');
});

test('defeatLine reads a card that only carries value', () => {
  const bare = { lostReason: 'cannot_defend', step4Required: 10, step4Paid: 0, hand: [{ value: 7 }] };
  assert.equal(defeatLine(bare), 'Short by three.');
  assert.equal(defeatLine({ ...bare, hand: [{}] }), 'Short by ten.');
});
