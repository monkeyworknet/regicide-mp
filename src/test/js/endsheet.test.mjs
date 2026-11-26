// Unit tests for the end sheet's data model. The sheet is the last screen a player
// sees, and 0.9.8 cut it from seven flat controls to four; these tests pin which
// controls exist for whom, so a host-only control cannot reappear for a guest and the
// way back to the main menu cannot be dropped again.
import test from 'node:test';
import assert from 'node:assert/strict';

import { endSheetModel, END_SHEET_ACTIONS } from '../../regicide/server/web/endsheet.js';

const ids = (actions) => actions.map((a) => a.id);
const group = (model, id) => model.actions.find((a) => a.id === id);

// A finished co-op loss, the shape the server sends at the end of a table game.
function tableLoss(extra = {}) {
  return Object.assign({
    won: false,
    lost: true,
    lostReason: 'cannot_defend',
    lostSeat: 1,
    step4Required: 9,
    step4Paid: 2,
    hand: [{ id: '2D', value: 2 }],
    numPlayers: 3,
    turnNo: 30,
    seedVisible: false,
    log: [
      { type: 'play', seat: 0, cards: ['10S'] },
      { type: 'damage', amount: 10 },
      { type: 'suffer', seat: 1, cards: ['5H'] },
      { type: 'defeat', cards: ['JS'] },
    ],
  }, extra);
}

test('a table host gets Play again, one log group, Main menu and Close', () => {
  const model = endSheetModel(tableLoss(), {
    title: 'Defeat', inRoom: true, isHost: true, seat: 0, names: ['Ada', 'Bo', 'Cy'],
  });
  assert.deepEqual(ids(model.actions), ['playAgain', 'log', 'mainMenu', 'close']);
  assert.equal(model.actions[0].kind, 'primary');
  assert.equal(model.waiting, '');
  assert.deepEqual(ids(group(model, 'log').children), ['history', 'copyLog', 'downloadLog', 'watchReplay']);
  // The log group is the only container: a player sees four controls, not seven.
  assert.equal(model.actions.filter((a) => !a.children).length, 3);
});

test('a guest gets no Play again and is told what they are waiting for', () => {
  const model = endSheetModel(tableLoss(), {
    title: 'Defeat', inRoom: true, isHost: false, seat: 2, names: ['Ada', 'Bo', 'Cy'],
  });
  assert.deepEqual(ids(model.actions), ['log', 'mainMenu', 'close']);
  assert.equal(model.actions.find((a) => a.id === 'mainMenu').kind, 'primary');
  assert.equal(model.waiting, 'Waiting for the host to deal again.');
  // The replay belongs to the host (D-47); a guest's group has no download or viewer.
  assert.deepEqual(ids(group(model, 'log').children), ['history', 'copyLog']);
});

test('the loss names the seat that ended the table, not the reader', () => {
  const model = endSheetModel(tableLoss(), {
    title: 'Defeat', inRoom: true, isHost: true, seat: 0, names: ['Ada', 'Bo', 'Cy'],
  });
  assert.match(model.lines[0].text, /^Bo could not pay the attack\.$/);
  // The shortfall is about seat 1's hand, so seat 0's sheet does not quote it.
  assert.ok(!model.lines.some((l) => /Short by/.test(l.text)));
});

test('the shortfall appears only for the seat that held the hand', () => {
  const mine = endSheetModel(tableLoss({ hand: [{ id: '2D', value: 2 }] }), {
    title: 'Defeat', inRoom: true, isHost: false, seat: 1, names: ['Ada', 'Bo', 'Cy'],
  });
  assert.match(mine.lines[1].text, /^Short by /);
  assert.equal(mine.lines[1].class, 'hint');

  const covered = endSheetModel(tableLoss(), {
    title: 'Defeat', inRoom: true, isHost: false, seat: 1, watching: true, names: ['Ada', 'Bo', 'Cy'],
  });
  assert.ok(!covered.lines.some((l) => /Short by/.test(l.text)));
});

test('a table win has no solo medal and carries four awards with seat names', () => {
  const model = endSheetModel(tableLoss({ won: true, lost: false, numPlayers: 3 }), {
    title: 'Victory', inRoom: true, isHost: true, seat: 0, names: ['Ada', 'Bo', 'Cy'],
  });
  assert.ok(!model.lines.some((l) => l.class === 'result-tier'));
  assert.match(model.lines[0].text, /^Twelve down in 30 turns\./);
  assert.equal(model.awards.length, 4);
  assert.deepEqual(model.awards.map((a) => a.key), ['damage', 'drawn', 'slain', 'jesters']);
  assert.match(model.awards[0].text, /^Ada · 10$/);
  // A 1-seat table has nobody to award.
  const solo = endSheetModel(tableLoss({ won: true, numPlayers: 1 }), { inRoom: false, seat: 0 });
  assert.equal(solo.awards.length, 0);
});

test('a solo win keeps its medal and offers New run first', () => {
  const model = endSheetModel({
    won: true, lost: false, tier: 'gold', numPlayers: 1, turnNo: 20, jestersUsed: 1,
    seedVisible: true, seed: 'ABC-1', seedRandom: true, rules: {}, log: [],
  }, { title: 'Victory', inRoom: false, seat: 0, hasSession: true });
  assert.deepEqual(ids(model.actions), ['newRun', 'leaderboard', 'log', 'mainMenu', 'close']);
  assert.equal(model.actions[0].kind, 'primary');
  assert.equal(model.lines[0].class, 'result-tier');
  assert.equal(model.lines[0].text, 'GOLD VICTORY');
  assert.deepEqual(model.seed, { value: 'ABC-1' });
  assert.equal(model.leaderboard.eligible, true);
  assert.deepEqual(ids(group(model, 'log').children), ['history', 'copyLog', 'downloadLog', 'watchReplay']);
});

test('a solo loss quotes the shortfall to its only player', () => {
  const model = endSheetModel({
    won: false, lost: true, lostReason: 'cannot_defend', lostSeat: 0,
    step4Required: 9, step4Paid: 2, hand: [{ id: '2D', value: 2 }],
    turnNo: 12, numPlayers: 1, seedVisible: false, rules: {}, log: [],
  }, { inRoom: false, seat: -1 });
  assert.match(model.lines[1].text, /^Short by /);
  assert.equal(model.lines[1].class, 'hint');
});

test('a solo loss without a session has no leaderboard and no replay controls', () => {
  const model = endSheetModel({
    won: false, lost: true, lostReason: 'cannot_act', turnNo: 12, numPlayers: 1,
    seedVisible: false, rules: {}, log: [],
  }, { title: 'Defeat', inRoom: false, seat: 0, hasSession: false });
  assert.deepEqual(ids(model.actions), ['newRun', 'log', 'mainMenu', 'close']);
  assert.deepEqual(ids(group(model, 'log').children), ['history', 'copyLog']);
  assert.equal(model.lines[0].text, 'You had no card, no Jester and no yield left.');
  assert.match(model.seed.note, /server-chosen seed/);
  assert.equal(model.leaderboard, null);
});

test('the leaderboard explains why a solo win is excluded', () => {
  const chosen = endSheetModel({
    won: true, numPlayers: 1, turnNo: 9, jestersUsed: 0, seedVisible: true, seed: 'X',
    seedRandom: false, rules: {}, log: [],
  }, { inRoom: false, seat: 0, hasSession: true });
  assert.equal(chosen.leaderboard.eligible, false);
  assert.match(chosen.leaderboard.note, /Chosen seeds are excluded/);

  const chaos = endSheetModel({
    won: true, numPlayers: 1, turnNo: 9, jestersUsed: 0, seedVisible: true, seed: 'X',
    seedRandom: true, rules: { chaos: true }, log: [],
  }, { inRoom: false, seat: 0, hasSession: true });
  assert.equal(chaos.leaderboard.eligible, false);
  assert.match(chaos.leaderboard.note, /Chaos runs are excluded/);
});

test('every model action id has a renderer handler in the contract', () => {
  const models = [
    endSheetModel(tableLoss(), { inRoom: true, isHost: true }),
    endSheetModel(tableLoss(), { inRoom: true, isHost: false }),
    endSheetModel({ won: true, numPlayers: 1, log: [] }, { inRoom: false, hasSession: true }),
    endSheetModel({ lost: true, numPlayers: 1, log: [] }, { inRoom: false, hasSession: false }),
  ];
  const seen = new Set();
  for (const model of models) {
    for (const action of model.actions) {
      seen.add(action.id);
      for (const child of action.children || []) seen.add(child.id);
    }
  }
  for (const id of seen) assert.ok(END_SHEET_ACTIONS.includes(id), `${id} is not in the action contract`);
  // And the contract has no id the model never produces, which would be dead code.
  for (const id of END_SHEET_ACTIONS) assert.ok(seen.has(id), `${id} is never produced`);
});
