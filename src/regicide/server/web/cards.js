// Card rendering. One component for every card set:
//  - themes whose art has a blank middle (modern, classic, dark) also get the
//    authored rule text placed inside it;
//  - every other theme is pure art and explains itself through the hover helper.
// Both are plain DOM: no framework, no build step.

import { artUrl, themeInfo, backUrl, jesterArt } from './themes.js';
import { suitShort } from './format.js';

let cardText = null;
let cardTextPromise = null;

export async function loadCardText() {
  if (cardText) return cardText;
  if (!cardTextPromise) {
    cardTextPromise = fetch('/data/cardtext.json')
      .then((r) => (r.ok ? r.json() : { cards: {} }))
      .then((j) => (cardText = j.cards || {}))
      .catch(() => (cardText = {}));
  }
  return cardTextPromise;
}

export function textFor(id) {
  return (cardText && cardText[id]) || null;
}

const SUIT_SYMBOL = { clubs: '♣', diamonds: '♦', hearts: '♥', spades: '♠', none: '★' };

export function suitSymbol(card) {
  return SUIT_SYMBOL[card.suit] || '★';
}

// Suit glyphs are drawn, not typed. Fonts on some phones, and on the headless
// Chromium used for screenshots, have no ♠♥♦♣ — the glyph silently disappears and
// "Played 8♥" becomes "Played 8". These paths always render.
const SUIT_PATH = {
  spades: 'M12 2C12 2 3.5 9.2 3.5 14.2A4.8 4.8 0 0 0 12 17c-.5 2.1-1.8 3.4-3.4 4.2h6.8c-1.6-.8-2.9-2.1-3.4-4.2a4.8 4.8 0 0 0 8.5-2.8C20.5 9.2 12 2 12 2z',
  hearts: 'M12 21.2S3.2 15.4 3.2 9.9A4.9 4.9 0 0 1 12 6.6a4.9 4.9 0 0 1 8.8 3.3c0 5.5-8.8 11.3-8.8 11.3z',
  diamonds: 'M12 1.8 20 12l-8 10.2L4 12z',
  clubs: 'M12 2.4a3.9 3.9 0 0 0-3.2 6.1 3.9 3.9 0 1 0 1.5 7.4c-.3 1.9-1.4 3.3-2.8 4.1h9c-1.4-.8-2.5-2.2-2.8-4.1a3.9 3.9 0 1 0 1.5-7.4A3.9 3.9 0 0 0 12 2.4z',
};

// suitIcon returns an inline SVG for a suit, sized in ems so it follows the text.
export function suitIcon(suit, { className = 'suit-icon' } = {}) {
  const d = SUIT_PATH[suit];
  const ns = 'http://www.w3.org/2000/svg';
  const svg = document.createElementNS(ns, 'svg');
  svg.setAttribute('viewBox', '0 0 24 24');
  svg.setAttribute('aria-hidden', 'true');
  svg.setAttribute('focusable', 'false');
  svg.setAttribute('class', className);
  if (!d) {
    svg.setAttribute('width', '0.9em');
    svg.setAttribute('height', '0.9em');
    return svg;
  }
  const path = document.createElementNS(ns, 'path');
  path.setAttribute('d', d);
  path.setAttribute('fill', 'currentColor');
  svg.append(path);
  return svg;
}

// isRoyalRank marks the three cards whose value is not their name: J, Q, K. The Ace
// is worth 1, which it says.
export function isRoyalRank(rank) {
  return rank >= 11 && rank <= 13;
}

export function rankLabel(rank) {
  return { 0: '★', 1: 'A', 11: 'J', 12: 'Q', 13: 'K' }[rank] ?? String(rank);
}

function ariaLabel(card) {
  const info = textFor(card.id);
  const name = info ? info.title : `${rankLabel(card.rank)}${suitSymbol(card)}`;
  return `${name} — value ${card.value}`;
}

// cardElement builds the button for one card.
// A Jester has no face in the deck art: its face comes from the joker catalogue, so
// every place that draws one (hand, zoom sheet, peek) must pass the same pick.
export function isJesterId(id) {
  return id === 'J1' || id === 'J2';
}

export function cardElement(card, { theme = 'official', selected = false, state = '', size = '', jokers = null } = {}) {
  const info = themeInfo(theme);
  const btn = document.createElement('button');
  btn.type = 'button';
  btn.className = ['card', `theme-${theme}`, size, state, selected ? 'is-selected' : '']
    .filter(Boolean)
    .join(' ');
  btn.dataset.card = card.id;
  btn.dataset.suit = card.suit;
  // The hand is a listbox of options, so the selected state is aria-selected;
  // aria-pressed is for toggle buttons and is ignored on role=option.
  btn.setAttribute('aria-selected', selected ? 'true' : 'false');
  btn.setAttribute('aria-label', ariaLabel(card));
  btn.setAttribute('role', 'option');

  const text = textFor(card.id);
  const img = document.createElement('img');
  img.className = 'card-img';
  img.alt = ariaLabel(card);
  img.decoding = 'async';
  img.loading = 'lazy';
  // J1 and J2 can carry different faces; the player picks each in Settings.
  img.src = isJesterId(card.id)
    ? jesterArt(theme, jokers)[String(card.id).toUpperCase() === 'J2' ? 1 : 0]
    : artUrl(theme, card.id);
  if (info.pixelated) img.classList.add('is-pixelated');
  img.addEventListener('error', () => {
    // Art missing: fall back to a clean index card rather than a broken image.
    // This is the only place an index is drawn — every deck already prints its own.
    // Guarded: a restored or re-inserted image can fire error twice.
    if (btn.classList.contains('missing-art')) return;
    btn.classList.add('missing-art');
    img.remove();
    btn.append(fallbackIndex(card));
  });
  btn.append(img);

  // A Jester face prints its own lettering; rule text goes under the card in
  // the zoom sheet, never over the art.
  if (info.hasText && !isJesterId(card.id)) btn.append(cardBody(text, card));
  btn.append(counterSlot(card));
  return btn;
}

// compactRef is the short index used on a phone face: rank + suit letter — two
  // glyphs for a pip card ("9C"), three for a ten ("10C").
const SUIT_LETTER = { clubs: 'C', diamonds: 'D', hearts: 'H', spades: 'S' };

export function compactRef(card) {
  return `${rankLabel(card.rank)}${SUIT_LETTER[card.suit] || ''}`;
}

// fallbackIndex is the corner rank+suit used when a face image fails to load.
function fallbackIndex(card) {
  const index = document.createElement('span');
  index.className = 'card-index';
  index.setAttribute('aria-hidden', 'true');
  index.append(el2('span', 'card-rank', rankLabel(card.rank)));
  const suitSlot = el2('span', 'card-suit', '');
  suitSlot.append(suitIcon(card.suit));
  index.append(suitSlot);
  return index;
}

// el2 is a tiny element helper for the card internals (kept local so cards.js does
// not depend on app.js).
function el2(tag, className, text) {
  const node = document.createElement(tag);
  node.className = className;
  node.textContent = text;
  return node;
}

// cardBody is the rule text that fills the template's blank middle.
function cardBody(info, card) {
  const body = document.createElement('span');
  body.className = 'card-body';
  const title = document.createElement('span');
  title.className = 'card-title';
  // J, Q and K do not say what they are worth (10, 15, 20), and the value line is
  // gone, so the title carries it: "Jack of Clubs (10)".
  const name = info ? info.title : `${rankLabel(card.rank)}${suitSymbol(card)}`;
  title.textContent = isRoyalRank(card.rank) ? `${name} (${card.value})` : name;
  const value = document.createElement('span');
  value.className = 'card-value';
  value.textContent = info ? info.value : `${card.value}`;
  const power = document.createElement('span');
  power.className = 'card-power';
  power.textContent = info ? info.power : '';
  const note = document.createElement('span');
  note.className = 'card-note';
  note.textContent = info ? info.note : '';
  // A phone cannot fit the sentence, so the face prints the suit power in words
  // ("Clubs double damage"); the long form stays on the sheet and the zoom.
  // Phone-only face (modern has a blank middle and a corner index a few pixels
  // tall): "9C" above "Clubs double damage". The full sentence is a long-press away.
  const ref = el2('span', 'card-ref-compact', compactRef(card));
  ref.setAttribute('aria-hidden', 'true');
  body.append(ref, title, value, power, note, el2('span', 'card-power-phone', suitShort(card.suit)));
  return body;
}

// backCard builds a face-down card (tavern stack, spent Jester, hidden pile).
export function backCard(backId, { state = '', size = '', label = 'Face-down card' } = {}) {
  const card = document.createElement('div');
  card.className = ['card', 'card-back', size, state].filter(Boolean).join(' ');
  card.setAttribute('role', 'img');
  card.setAttribute('aria-label', label);
  const img = document.createElement('img');
  img.className = 'card-img';
  img.alt = '';
  img.decoding = 'async';
  img.loading = 'lazy';
  img.src = backUrl(backId);
  card.append(img);
  return card;
}

function counterSlot(card) {
  const wrap = document.createElement('span');
  wrap.className = 'card-counters';
  wrap.dataset.for = card.id;
  return wrap;
}
