// The replay viewer (P6, D-47): a browser tab that walks a finished game.
//
// It is deliberately a separate page with no imports from the game shell: a replay is
// read-only, and it must not be able to touch a live session. The only credential it
// holds is the ticket the host minted, in the URL, good for two minutes.
//
// The engine lives in Go, so the server rebuilds the position for a step and this page
// draws it. The server also sends the step's action and its events, which is what makes
// the page explain the game instead of only showing a position.

import { historyEntry } from '/format.js';
import { suitIcon } from '/cards.js';
import { loadThemes, jesterArt, jokerPairFor } from '/themes.js';

const params = new URLSearchParams(location.search);
const ticket = params.get('replay') || '';
const root = document.getElementById('viewer');

const SEAT_COLOURS = ['red', 'blue', 'green', 'black'];
let step = 0;
let steps = 0;
let as = 0;
let busy = false;
// jokerPair is the pair the replay was played with. The header records it; an older
// replay predates the field, so a valid pair for the theme stands in.
let jokerPair = [];

function el(tag, attrs = {}, ...kids) {
  const node = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs)) {
    if (v === false || v == null) continue;
    if (k === 'class') node.className = v;
    else if (k === 'text') node.textContent = v;
    else if (k.startsWith('on') && typeof v === 'function') node.addEventListener(k.slice(2).toLowerCase(), v);
    else node.setAttribute(k, String(v));
  }
  for (const kid of kids.flat()) {
    if (kid == null) continue;
    node.append(kid.nodeType ? kid : document.createTextNode(String(kid)));
  }
  return node;
}

const SUIT_NAME = { C: 'clubs', D: 'diamonds', H: 'hearts', S: 'spades' };

// The suit is drawn, not typed: the headless browser (and some phones) has no ♠♥♦♣
// glyph, and a label that silently loses its suit is not a card label.
function cardText(id) {
  const text = String(id);
  const suit = SUIT_NAME[text.slice(-1).toUpperCase()];
  const wrap = el('span', { class: 'rv-card-name' });
  wrap.append(document.createTextNode(suit ? text.slice(0, -1) : text));
  if (suit) wrap.append(suitIcon(suit, { className: `suit-icon suit-${suit}` }));
  return wrap;
}

function cardNode(card, theme, cls = 'rv-card') {
  const img = el('img', { class: cls, alt: card.label || String(card.id), loading: 'lazy' });
  const jester = /^j[12]$/i.test(String(card.id));
  img.src = jester
    ? jesterArt(theme, jokerPair)[String(card.id).toUpperCase() === 'J2' ? 1 : 0]
    : `/img/${theme}/${String(card.id).toLowerCase()}.webp`;
  return img;
}

function exitLink() {
  return el('a', { class: 'rv-exit', href: '/' }, 'Main menu');
}

function message(text) {
  root.replaceChildren(
    el('div', { class: 'rv-top' }, el('div', { class: 'rv-title' }, el('h1', { text: 'Replay' })), exitLink()),
    el('p', { class: 'rv-note', text }),
  );
}

async function load() {
  if (!ticket) {
    message('This link is missing its ticket. Ask the host for a new one from the result screen.');
    return;
  }
  if (busy) return;
  busy = true;
  try {
    await loadThemes();
    const res = await fetch(`/api/replay/${encodeURIComponent(ticket)}?step=${step}&as=${as}`);
    if (!res.ok) {
      const body = await res.json().catch(() => ({}));
      message(body.error === 'ticket_expired'
        ? 'This replay ticket has expired. Open the result screen in the game and mint a new one.'
        : 'That replay could not be read.');
      return;
    }
    draw(await res.json());
  } catch {
    message('That replay could not be reached. Check the connection and try again.');
  } finally {
    busy = false;
  }
}

function seatName(players, seat) {
  const row = players.find((p) => p.seat === seat);
  return (row && row.name) || `seat ${seat + 1}`;
}

function moveLine(data, players) {
  const action = data.action;
  if (!action) return { who: '', verb: 'The deal', cards: [], tail: '' };
  const who = seatName(players, action.seat);
  switch (action.type) {
    case 'play': {
      const playEvent = (data.events || []).find((ev) => ev.type === 'play');
      const jester = /jester/i.test((playEvent && playEvent.note) || '');
      const amount = playEvent && playEvent.amount;
      const tail = jester ? 'a Jester — immunity cancelled' : amount ? `${amount} attack` : '';
      return { who, verb: 'played', cards: action.cards || [], tail };
    }
    case 'pay_damage':
      return { who, verb: 'defended with', cards: action.cards || [], tail: '' };
    case 'end_payment':
      return { who, verb: 'finished paying', cards: [], tail: '' };
    case 'flip_jester':
      return { who, verb: 'flipped a Jester', cards: [], tail: '' };
    case 'yield':
      return { who, verb: 'yielded the turn', cards: [], tail: '' };
    case 'choose_next_seat':
      return { who, verb: `chose ${seatName(players, action.seatTarget)} to go next`, cards: [], tail: '' };
    default:
      return { who, verb: String(action.type).replace(/_/g, ' '), cards: [], tail: '' };
  }
}

function lostText(reason, solo) {
  if (reason === 'cannot_defend') return solo ? 'you could not pay the attack' : 'the attack could not be paid';
  if (reason === 'cannot_act') return solo ? 'you had no card, no Jester and no yield left' : 'no seat had a legal action left';
  return solo ? 'the run ended' : 'the table fell';
}

function eventLine(ev, state) {
  const entry = historyEntry(ev, state);
  if (!entry) return null;
  const text = entry.text;
  const bold = el('b');
  const parts = text.split('{cards}');
  if (parts.length > 1 && entry.cards && entry.cards.length) {
    parts.forEach((part, i) => {
      if (part) bold.append(document.createTextNode(part));
      if (i < parts.length - 1) {
        for (const id of entry.cards) bold.append(cardText(id), ' ');
      }
    });
  } else {
    bold.append(document.createTextNode(text));
  }
  return el('li', {}, bold, entry.impact ? el('span', { text: entry.impact }) : null);
}

function draw(data) {
  steps = data.steps;
  const theme = (data.header && data.header.theme) || 'simple';
  const headerPair = data.header && Array.isArray(data.header.jokers) ? data.header.jokers : [];
  if (headerPair.length === 2) jokerPair = headerPair;
  else if (jokerPair.length !== 2) jokerPair = jokerPairFor(theme);
  const players = (data.header && data.header.players) || [];
  const state = data.state || {};
  const result = data.result;
  const won = Boolean(result && result.phase === 'won');
  const outcome = result ? (won ? 'Won' : 'Lost') : 'In progress';

  const title = el('div', { class: 'rv-title' },
    el('h1', {}, `Replay · ${data.code || 'table'}`,
      el('span', { class: `rv-badge ${won ? 'is-won' : 'is-lost'}`, text: outcome })),
    el('p', {
      class: 'rv-sub',
      text: `${steps} actions · ${players.length} player${players.length === 1 ? '' : 's'}` +
        (result && !won ? ` · ${lostText(result.lostReason, players.length === 1)}` : ''),
    }),
  );
  const top = el('div', { class: 'rv-top' }, title, exitLink());

  const controls = el('div', { class: 'rv-controls' },
    el('button', {
      type: 'button', class: 'rv-btn', text: 'Prev', 'aria-label': 'Previous action',
      disabled: step <= 0, onclick: () => { if (step > 0) { step -= 1; load(); } },
    }),
    el('input', {
      type: 'range', class: 'rv-range', min: 0, max: steps, value: step, 'aria-label': 'Action',
      oninput: (ev) => { step = Number(ev.currentTarget.value); load(); },
    }),
    el('button', {
      type: 'button', class: 'rv-btn', text: 'Next', 'aria-label': 'Next action',
      disabled: step >= steps, onclick: () => { if (step < steps) { step += 1; load(); } },
    }),
    el('span', { class: 'rv-step', text: `${step} / ${steps}` }),
  );

  const move = moveLine(data, players);
  const moveWhat = el('p', { class: 'rv-move-what' });
  if (move.who) moveWhat.append(el('b', {}, move.who), ' ');
  moveWhat.append(move.verb);
  for (const id of move.cards) moveWhat.append(' ', cardText(id));
  if (move.tail) moveWhat.append(` — ${move.tail}`);
  const moveCards = el('div', { class: 'rv-move-cards' });
  for (const id of move.cards) moveCards.append(cardNode({ id, label: id }, theme));
  const events = el('ul', { class: 'rv-events' });
  for (const ev of (data.events || [])) {
    const line = eventLine(ev, state);
    if (line) events.append(line);
  }
  if (!events.childElementCount) events.append(el('li', {}, el('span', { text: step === 0 ? 'Opening position.' : 'No further events.' })));
  const movePanel = el('section', { class: 'rv-move', 'aria-label': 'The move' },
    el('div', { class: 'rv-move-head' },
      el('h2', { text: 'The move' }),
      el('span', { class: 'rv-step', text: step === 0 ? 'opening' : `step ${step} of ${steps}` }),
    ),
    moveWhat,
    moveCards,
    events,
  );

  // The royal and the pile counts: the position the move left behind.
  const facts = el('dl', { class: 'rv-stats' });
  const add = (k, v) => { facts.append(el('dt', { text: k }), el('dd', { text: String(v) })); };
  add('turn', state.turnNo ?? '—');
  add('health', `${state.enemyHealth ?? '—'}${state.damage ? ` · ${state.damage} damage` : ''}`);
  add('attack', state.enemyAttack ?? '—');
  add('castle', state.castleCount ?? '—');
  add('tavern', state.tavernCount ?? '—');
  add('discard', state.discardCount ?? '—');
  add('jesters', state.jestersLeft ?? '—');
  const royal = state.enemy && state.enemy.id
    ? el('div', { class: 'rv-royal' }, cardNode(state.enemy, theme), el('span', { class: 'rv-royal-name' }, cardText(state.enemy.id)))
    : null;
  const tablePanel = el('section', { class: 'rv-panel' },
    el('h2', { text: 'The table' }),
    el('div', { class: 'rv-table' }, royal, facts),
  );

  const seats = el('div', { class: 'rv-seats' });
  for (const p of players) {
    const view = (state.players || []).find((x) => x.seat === p.seat)
      || (players.length === 1 && p.seat === 0
        ? { handCount: (state.hand || []).length, active: state.activeSeat === 0 }
        : {});
    seats.append(el('button', {
      type: 'button',
      class: `rv-seat${p.seat === as ? ' is-on' : ''}${view.active ? ' is-active' : ''}`,
      style: `--c: var(--seat-${SEAT_COLOURS[p.seat % SEAT_COLOURS.length]})`,
      'aria-pressed': String(p.seat === as),
      onclick: () => { as = p.seat; load(); },
    },
      el('span', { class: 'rv-seat-name', text: p.name || `seat ${p.seat}` }),
      el('span', {
        class: 'rv-seat-count',
        text: `${view.handCount ?? '—'} cards${view.active ? ' · playing' : ''}`,
      })));
  }
  const hand = el('div', { class: 'rv-hand' });
  for (const card of (state.hand || [])) {
    hand.append(el('div', { class: 'rv-card-wrap' }, cardNode(card, theme), el('span', { class: 'rv-card-label' }, cardText(card.id))));
  }
  if (!hand.childElementCount) hand.append(el('p', { class: 'rv-empty', text: 'No cards in hand.' }));
  const handPanel = el('section', { class: 'rv-panel' },
    el('h2', { text: `Hand — ${seatName(players, as)}` }),
    seats,
    hand,
  );

  root.replaceChildren(top, controls, movePanel,
    el('div', { class: 'rv-grid' }, tablePanel, handPanel),
    el('p', { class: 'rv-foot', text: 'Read-only replay. Arrow keys step; the game is rebuilt and verified by the server.' }),
  );
}

// Arrow keys step, because a replay is something you read with one hand.
document.addEventListener('keydown', (ev) => {
  if (ev.target && /input|select|textarea/i.test(ev.target.tagName || '')) return;
  if (ev.key === 'ArrowLeft' && step > 0) { step -= 1; load(); }
  if (ev.key === 'ArrowRight' && step < steps) { step += 1; load(); }
});

load();
