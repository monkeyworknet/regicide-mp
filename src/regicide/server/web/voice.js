// Screen copy. The menu tagline and the victory title are pulled at random from
// fixed pools (a Minecraft-style rotating line), so the same screen does not say
// the same thing every run. Loss and win-detail lines are fixed templates: they
// carry numbers, and a number that changes wording is a number nobody trusts.
//
// Pure module: no DOM, no globals, so the node unit tests exercise exactly the
// strings the browser renders.

import { discardValueOf } from './format.js';

// Menu pools. The setup screen draws one headline and one lede independently every
// time it is shown.
export const HEADLINES = [
  'Twelve crowns to break, and one hand to break them.',
  'In the castle twelve sleep. Your weapon supply is eight deep.',
  'Every royal falls the same way: card by card.',
  'Twelve royals, four suits, eight cards to do it with.',
  'The court is a stack. You are a hand.',
  'A castle is just a house of cards.',
];

export const LEDES = [
  'A card wounds; the same card pays. Both come out of the same eight you were dealt.',
  'To strike is to spend, and to survive is to spend again, and your hand holds fewer cards than the castle holds royals.',
  'You are not dealt reinforcements. Cards wound, cards pay, and the same eight serve all twelve.',
  'Break a royal and you spend cards; live through it and you spend again.',
  'Every wound is paid in cards, and every card spent is one fewer for the next royal.',
];

// Victory titles. Shown above the tier line ("GOLD VICTORY"), so none of them
// may contain the word victory. Both result headings are drawn once per game.
export const VICTORIES = [
  'The court is broken',
  'Twelve royals, none standing',
  'The castle has no one left to answer',
  'Every throne is empty',
  'The hand outlasted the court',
];

// Loss headings. Same register as the victory titles. Every line has to be true
// of BOTH loss reasons (the hand could not cover the attack, and the hand was
// empty at the start of a turn), because the heading does not know which one it
// is captioning — the line underneath it does.
export const LOSSES = [
  'The court holds',
  'Twelve royals, still standing',
  'The hand fell short',
  'The castle keeps its answer',
  'The court is still twelve',
];

// NO_ANSWER is the loss line used when the hand ran out instead of the cards
// being short: there is no shortfall to quote, because the player never got to
// step 4 with a hand at all.
export const NO_ANSWER = 'No cards and no Jesters left.';

const NUMBER_WORDS = [
  'no', 'one', 'two', 'three', 'four', 'five', 'six', 'seven', 'eight', 'nine', 'ten',
  'eleven', 'twelve', 'thirteen', 'fourteen', 'fifteen', 'sixteen', 'seventeen', 'eighteen',
  'nineteen', 'twenty',
];

// word renders 0-20 as a word ("nine"), and anything larger as digits. The pools
// above are written as prose, so the numbers in the result lines read as prose.
export function word(n) {
  const i = Math.max(0, Math.round(Number(n) || 0));
  return i < NUMBER_WORDS.length ? NUMBER_WORDS[i] : String(i);
}

// pickLine draws one entry from a pool. `rand` is injectable so tests are exact;
// an empty pool returns '' rather than crashing a screen.
export function pickLine(pool, rand = Math.random) {
  if (!Array.isArray(pool) || pool.length === 0) return '';
  const at = Math.floor(Math.max(0, Math.min(0.999999, Number(rand()) || 0)) * pool.length);
  return pool[at];
}

// resultHeading draws the heading for a finished game. Callers memoise it: it
// is drawn once per game, so a re-render cannot change the heading under the
// player's eyes.
export function resultHeading(won, rand = Math.random) {
  return pickLine(won ? VICTORIES : LOSSES, rand);
}

// jestersSpent is "Two Jesters" / "One Jester" / "No Jesters". Capitalised
// because it opens the second sentence of each won line.
function jestersSpent(used) {
  const n = Math.max(0, Math.round(Number(used) || 0));
  if (n > 2) return `${n} Jesters`;
  const count = n === 0 ? 'No' : word(n).replace(/^./, (c) => c.toUpperCase());
  return `${count} Jester${n === 1 ? '' : 's'}`;
}

// wonLine is the fixed victory detail: turns taken and Jesters. Solo Jesters are the
// two flips; in co-op a Jester is a card, so the caller passes how many were played.
export function wonLine(state, opts = {}) {
  const turns = Math.max(0, Math.round(Number(state && state.turnNo) || 0));
  const coop = Boolean(opts.coop);
  const used = Math.max(0, Math.round(Number(coop ? opts.jesters : state && state.jestersUsed) || 0));
  const verb = coop ? 'played' : 'spent';
  return `Twelve down in ${turns} ${turns === 1 ? 'turn' : 'turns'}. ${jestersSpent(used)} ${verb}.`;
}

// defeatLine answers "by how much". A cannot_defend loss only happens when the
// hand cannot cover the attack, so the shortfall is the attack still owed minus
// everything left in hand; it is quoted, not described.
export function defeatLine(state) {
  if (!state) return NO_ANSWER;
  if (state.lostReason !== 'cannot_defend') return NO_ANSWER;
  const owed = Math.max(0, (state.step4Required || 0) - (state.step4Paid || 0));
  const inHand = (state.hand || []).reduce((n, c) => n + discardValueOf(c), 0);
  const short = owed - inHand;
  // A cannot_defend state always leaves a real shortfall; if a future rule change
  // makes that untrue, say nothing rather than print "Short by no".
  return short > 0 ? `Short by ${word(short)}.` : NO_ANSWER;
}
