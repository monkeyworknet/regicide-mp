// The end sheet as data, so the controls a player is offered can be tested without a
// DOM. 0.9.8: the multiplayer sheet had seven controls, one of them a duplicate of the
// backdrop and none of them a way back to the menu. The model decides what exists and in
// which order; app.js only renders it and wires the handlers.
//
// Pure module: no DOM, no globals. It imports the same copy the browser uses, so a
// wording change lands in both.
import { matchAwards } from './format.js';
import { wonLine, defeatLine, NO_ANSWER } from './voice.js';

// The actions the renderer knows how to wire. Anything not in this list is a bug in the
// model, not a control that silently does nothing.
export const END_SHEET_ACTIONS = [
  'newRun', 'playAgain', 'mainMenu', 'leaderboard', 'close', 'log',
  'history', 'copyLog', 'downloadLog', 'watchReplay',
];

// jestersPlayed counts Jester *cards* played from the log. A co-op win uses the card
// count; solo uses the two flips, which the state already carries.
function jestersPlayed(log) {
  let n = 0;
  for (const ev of log || []) {
    if (!ev || ev.type !== 'play') continue;
    for (const id of ev.cards || []) {
      if (/^j[12]$/i.test(String(id))) n += 1;
    }
  }
  return n;
}

// seatName is the display name for a seat: the room's rail first, then the state's own
// player list, then "Seat N". Names are supplied, not read, so the model stays pure.
function seatName(names, seat) {
  const name = names[seat];
  return name || `Seat ${seat + 1}`;
}

// endSheetModel builds the result sheet. Options:
//   title       the memoised random heading (the renderer owns the memo)
//   inRoom      a room code is set: this is a table, not a solo run
//   isHost      the room's crown is on this seat
//   seat        this tab's seat (for the "your shortfall" line)
//   watching    this tab is a spectator
//   hasSession  a solo session exists, so a replay can be downloaded
//   names       seat -> display name
export function endSheetModel(state, opts = {}) {
  const s = state || {};
  const inRoom = Boolean(opts.inRoom);
  const isHost = Boolean(opts.isHost);
  const names = Array.isArray(opts.names) ? opts.names : [];
  const won = Boolean(s.won);
  const model = {
    title: opts.title || '',
    lines: [],
    awards: [],
    seed: null,
    leaderboard: null,
    waiting: '',
    actions: [],
  };

  if (won) {
    // A medal is a solo score: it ranks a run by Jesters spent. At a table the win is
    // the table's, and "GOLD VICTORY" implies a ranking nobody played for.
    if (!inRoom) {
      model.lines.push({ text: `${s.tier ? String(s.tier).toUpperCase() : ''} VICTORY`, class: 'result-tier' });
    }
    model.lines.push({
      text: wonLine(s, { coop: inRoom, jesters: jestersPlayed(s.log) }),
    });
    if (inRoom && (s.numPlayers || 1) > 1) {
      model.awards = matchAwards(s.log || [], s.numPlayers || 1).map((award) => ({
        key: award.key,
        label: award.label,
        text: award.amount > 0
          ? `${award.seats.map((seat) => seatName(names, seat)).join(' & ')} · ${award.amount}`
          : '—',
      }));
    }
  } else {
    // The defeat line is the *actor's* shortfall, which is right in solo and wrong at a
    // table: the sheet used to read "No cards and no jesters left" while another seat
    // still held five cards. In a room, name the seat and why the table ended.
    const lostSeat = Number.isInteger(s.lostSeat) ? s.lostSeat : -1;
    const who = lostSeat >= 0 ? seatName(names, lostSeat) : '';
    const why = s.lostReason === 'cannot_defend'
      ? (!inRoom ? 'You could not pay the attack.'
        : who ? `${who} could not pay the attack.` : 'The table could not pay the attack.')
      : s.lostReason === 'cannot_act'
        ? (!inRoom ? 'You had no card, no Jester and no yield left.'
          : who ? `${who} had no card, no Jester and no yield left.` : 'No seat had a legal action left.')
        : (!inRoom ? 'The run ended.' : 'The table fell.');
    model.lines.push({ text: why });
    // The shortfall is a number about one hand; show it only when that hand is here.
    // Solo has exactly one hand and the sheet belongs to it. A table needs the seat
    // match, and a spectator never sees it (they hold no hand).
    const short = defeatLine(s);
    const mine = inRoom ? (lostSeat === opts.seat && !opts.watching) : true;
    if (short.length > 0 && short !== NO_ANSWER && mine) {
      model.lines.push({ text: short, class: 'hint' });
    }
  }

  model.seed = s.seedVisible && s.seed
    ? { value: s.seed }
    : { note: 'This run used a server-chosen seed; it is revealed here at the end of the game.' };

  if (inRoom) {
    // The replay contains every hand: only the host may take it away (D-47).
    const log = [{ id: 'history', label: 'History' }, { id: 'copyLog', label: 'Copy log' }];
    if (isHost) {
      log.push({ id: 'downloadLog', label: 'Download the log' });
      log.push({ id: 'watchReplay', label: 'Watch the replay' });
    }
    model.actions = isHost
      ? [
        { id: 'playAgain', label: 'Play again', kind: 'primary' },
        { id: 'log', label: 'Log & replay', kind: 'ghost', children: log },
        { id: 'mainMenu', label: 'Main menu', kind: 'ghost' },
        { id: 'close', label: 'Close', kind: 'ghost' },
      ]
      : [
        { id: 'log', label: 'Log & replay', kind: 'ghost', children: log },
        { id: 'mainMenu', label: 'Main menu', kind: 'primary' },
        { id: 'close', label: 'Close', kind: 'ghost' },
      ];
    // Only the host may re-deal, so a guest is told what they are waiting for instead
    // of being offered a control that would refuse them.
    if (!isHost) model.waiting = 'Waiting for the host to deal again.';
    return model;
  }

  // Solo: a new run is the point of the screen; the log and the menu are beside it.
  const log = [{ id: 'history', label: 'History' }, { id: 'copyLog', label: 'Copy log' }];
  if (opts.hasSession) {
    log.push({ id: 'downloadLog', label: 'Download the log' });
    log.push({ id: 'watchReplay', label: 'Watch the replay' });
  }
  model.actions = [
    { id: 'newRun', label: 'New run', kind: 'primary' },
  ];
  if (opts.hasSession) {
    model.actions.push({ id: 'leaderboard', label: 'Leaderboard', kind: 'ghost' });
  }
  model.actions.push(
    { id: 'log', label: 'Log & replay', kind: 'ghost', children: log },
    { id: 'mainMenu', label: 'Main menu', kind: 'ghost' },
    { id: 'close', label: 'Close', kind: 'ghost' },
  );

  // The hall needs a solo session to submit; without one there is nothing to offer
  // and nothing to explain.
  if (opts.hasSession) {
    const eligible = won && Boolean(s.seedRandom) && !(s.rules && s.rules.chaos);
    model.leaderboard = {
      eligible,
      note: eligible ? '' : (won
        ? (s.rules && s.rules.chaos
          ? 'Chaos runs are excluded from the leaderboard.'
          : 'Chosen seeds are excluded from the leaderboard.')
        : ''),
    };
  }
  return model;
}
