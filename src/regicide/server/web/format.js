// Pure formatting helpers. They live in their own module so the browser and the
// node unit tests use exactly the same code (no build step, no DOM).

// formatDuration renders a run length as m:ss (or h:mm:ss past an hour).
export function formatDuration(totalSeconds) {
  const s0 = Math.max(0, Math.round(Number(totalSeconds) || 0));
  const h = Math.floor(s0 / 3600);
  const m = Math.floor((s0 % 3600) / 60);
  const sec = s0 % 60;
  if (h > 0) return `${h}:${String(m).padStart(2, '0')}:${String(sec).padStart(2, '0')}`;
  return `${m}:${String(sec).padStart(2, '0')}`;
}

// discardValueOf is what a card pays with in step 4. The server sends the value
// per card; the client never derives a rule of its own.
export function discardValueOf(card) {
  if (!card) return 0;
  return card.discardValue ?? card.value ?? 0;
}

// paymentLeft is how much of the enemy attack is still owed.
export function paymentLeft(state) {
  if (!state) return 0;
  return Math.max(0, (state.step4Required || 0) - (state.step4Paid || 0));
}

// paymentAfter is what stays owed once the picked cards are forfeited.
export function paymentAfter(state, cards) {
  const paid = (cards || []).reduce((n, c) => n + discardValueOf(c), 0);
  return Math.max(0, paymentLeft(state) - paid);
}

// The four suit powers, in the order the legend lists them. Modern cards have a
// blank middle, so a phone prints the word itself on the face; the legend and the
// phone face must never drift apart.
export const SUIT_POWERS = [['spades', 'shield'], ['hearts', 'shuffle'], ['diamonds', 'draw'], ['clubs', '×2']];

// One short line per suit, printed on a phone face instead of the full sentence.
// "9C / Clubs double damage" is readable at 58 px; the full text is one long-press
// away, and the exact rule stays on the zoom sheet.
export const SUIT_SHORT = {
  spades: 'Spades shield',
  hearts: 'Hearts shuffle',
  diamonds: 'Diamonds draw',
  clubs: 'Clubs double damage',
};

export function suitShort(suit) {
  return SUIT_SHORT[suit] || '';
}

// paySelection lets a player discard several cards at once in step 4. The engine
// accepts a batch as long as no card is repeated, and — without over-paying — no
// card but the last reaches the total. This is the same rule, in card values, so the
// button can say what will happen before the server is asked.
export function paySelectionValid(left, values, allowOverpay = false) {
  if (!Array.isArray(values) || values.length === 0) return false;
  if (values.some((v) => !Number.isFinite(v) || v < 0)) return false;
  if (allowOverpay) return true;
  let paid = 0;
  for (let i = 0; i < values.length - 1; i += 1) {
    paid += values[i];
    if (paid >= left) return false; // that card already covers the attack
  }
  return true;
}

// paySelectionAfter is what the batch leaves owed: zero means the turn ends.
export function paySelectionAfter(left, values) {
  const total = values.reduce((sum, v) => sum + v, 0);
  return Math.max(0, left - total);
}

// attributeSeats answers "who did that?" for every line of a co-op log.
//
// Only some events carry a seat: a play does, but a diamond draw, a spades shield or
// an attack resolved do not, because they are consequences of the play rather than
// separate actions. A table still reads them as that player's doing, so each line is
// attributed to the seat that acted most recently in its turn.
export function attributeSeats(log) {
  const out = [];
  let actor = null;
  for (const ev of log || []) {
    if (!ev) {
      out.push(null);
      continue;
    }
    // turn_end names the seat that is about to act, not the one that just did.
    if (typeof ev.seat === 'number' && ev.type !== 'turn_end' && ev.type !== 'draw') {
      actor = ev.seat;
    }
    // A diamond draw names the seat that received the cards. That seat is the
    // subject of the draw line, but it must NOT become the actor for the lines
    // that follow: a draw round-robin ends on the last seat, which used to colour
    // the next player's shield and the royal's defeat with the wrong dot.
    out.push(ev.type === 'turn_end' ? null : ev.type === 'draw' && typeof ev.seat === 'number' ? ev.seat : actor);
  }
  return out;
}

// SEAT_COLOURS is the seat colour order the whole co-op UI uses: rail, orbs, log.
export const SEAT_COLOURS = ['red', 'blue', 'green', 'black'];

// seatColour is the colour name for a seat index, or '' when there is no seat.
export function seatColour(seat) {
  if (typeof seat !== 'number' || seat < 0 || seat >= SEAT_COLOURS.length) return '';
  return SEAT_COLOURS[seat];
}

// bubbleFor turns the newest log entry for a seat into a one-line report the seat rail
// can show beside that player's name: "Attacked with 9♠", "Defended with 8♣ and 7♥".
//
// It reads the log *backwards* from index i because a payment is recorded card by card
// (one suffer event each): the bubble wants the whole batch, not its last card. The
// actor of each event comes from attributeSeats, so a draw or a shield is credited to
// the seat that played the card.
const BUBBLE_TYPES = new Set(['play', 'suffer', 'yield', 'jester_flip', 'choose_next_seat']);

export function bubbleFor(log, actors, i) {
  if (!log || i < 0 || i >= log.length) return null;
  const ev = log[i];
  if (!ev || !BUBBLE_TYPES.has(ev.type)) return null;
  const seat = actors[i];
  if (typeof seat !== 'number') return null;

  if (ev.type === 'suffer') {
    // Walk back over the whole payment: same seat, consecutive suffer events.
    const cards = [];
    let j = i;
    while (j >= 0 && log[j].type === 'suffer' && actors[j] === seat) {
      cards.unshift(...(log[j].cards || []));
      j -= 1;
    }
    if (cards.length === 0) return null;
    return { seat, text: 'Defended with', cards, detail: '' };
  }
  if (ev.type === 'play') {
    const cards = ev.cards || [];
    if (cards.length === 0) return null;
    // A Jester deals no damage: it cancels immunity and hands the turn on.
    if (ev.note && ev.note.includes('jester')) {
      return { seat, text: 'Cancelled immunity', cards, detail: 'jester' };
    }
    return {
      seat,
      text: 'Attacked with',
      cards,
      // One card prints its own value; a combo needs the total it added up to.
      detail: cards.length > 1 && ev.amount ? String(ev.amount) : '',
    };
  }
  if (ev.type === 'yield') return { seat, text: 'Yielded', cards: [], detail: '' };
  if (ev.type === 'jester_flip') return { seat, text: 'Used a Jester', cards: [], detail: '' };
  if (ev.type === 'choose_next_seat') {
    return { seat, text: 'Chose who goes next', cards: [], detail: '' };
  }
  return null;
}

// seatBubbles collects the bubble that is current for each seat. Newest event wins, so
// a player who has acted since still shows their latest action.
export function seatBubbles(log) {
  const out = new Map();
  const actors = attributeSeats(log);
  for (let i = (log || []).length - 1; i >= 0; i -= 1) {
    const bubble = bubbleFor(log, actors, i);
    if (!bubble || out.has(bubble.seat)) continue;
    out.set(bubble.seat, { ...bubble, key: bubbleKey(log[i]) });
  }
  return out;
}

// revealsFor turns the events of one action into the table reveals, in the order the
// table should hear them: "Edith attacked with 7♠ A♥", then — when the blow lands —
// "Jack of Spades is Defeated" and "Jack of Hearts enters the fight".
//
// A play is one event; a payment is one `suffer` event per card, so the whole batch is
// gathered. Every card of a multi-card play is shown at once. The royal items carry no
// seat: a defeat is the table's news, not the striker's.
//
// The caller passes the events of ONE action (the fresh suffix of the log); feeding it
// the whole 40-event window would replay the game.
export function revealsFor(log, actors) {
  const out = [];
  if (!Array.isArray(log) || log.length === 0) return out;
  for (let i = 0; i < log.length; i += 1) {
    const ev = log[i];
    if (!ev) continue;
    if (ev.type === 'play') {
      const seat = typeof ev.seat === 'number' ? ev.seat : actors[i];
      const cards = Array.isArray(ev.cards) ? ev.cards.slice() : [];
      if (typeof seat !== 'number' || cards.length === 0) continue;
      const jester = cards.some((id) => /^j[12]$/i.test(String(id))) || /jester/i.test(ev.note || '');
      out.push({ kind: jester ? 'jester' : 'attack', seat, cards });
      continue;
    }
    if (ev.type === 'suffer') {
      const seat = actors[i];
      const cards = [];
      let j = i;
      while (j < log.length && log[j] && log[j].type === 'suffer' && actors[j] === seat) {
        cards.push(...(log[j].cards || []));
        j += 1;
      }
      i = j - 1;
      if (typeof seat !== 'number' || cards.length === 0) continue;
      out.push({ kind: 'defend', seat, cards });
      continue;
    }
    if (ev.type === 'defeat' && Array.isArray(ev.cards) && ev.cards.length > 0) {
      out.push({ kind: 'defeat', seat: null, cards: [ev.cards[0]], royal: true });
      continue;
    }
    if (ev.type === 'enemy_reveal' && Array.isArray(ev.cards) && ev.cards.length > 0) {
      out.push({ kind: 'enter', seat: null, cards: [ev.cards[0]], royal: true });
    }
  }
  return out;
}

// royalName is the fallback name of a royal when the card text has not loaded yet:
// "JS" -> "Jack of Spades". Only the twelve royals have a name here; anything else
// keeps its id, because a reveal must never print "undefined".
const ROYAL_RANK = { J: 'Jack', Q: 'Queen', K: 'King' };
const SUIT_NAME = { C: 'Clubs', D: 'Diamonds', H: 'Hearts', S: 'Spades' };

export function royalName(id) {
  const text = String(id || '');
  const rank = ROYAL_RANK[text.charAt(0).toUpperCase()];
  const suit = SUIT_NAME[text.charAt(text.length - 1).toUpperCase()];
  return rank && suit ? `${rank} of ${suit}` : text;
}

// bubbleKey identifies an event well enough to know the bubble is the same one after a
// poll re-render, so its lifetime is not restarted every 1.5 seconds.
export function bubbleKey(ev) {
  if (!ev) return '';
  return [ev.turn, ev.type, (ev.cards || []).join('+'), ev.amount || 0, ev.seat || 0].join('|');
}

// historyCard renders a card id as "5♠" for the history list.
const CARD_SUIT_SYMBOL = { C: '♣', D: '♦', H: '♥', S: '♠' };

function historyCard(id) {
  const text = String(id);
  const suit = CARD_SUIT_SYMBOL[text.slice(-1)];
  return suit ? `${text.slice(0, -1)}${suit}` : text;
}

// historySeat names a seat from the public player list; a missing row still reads.
function historySeat(state, seat) {
  const row = ((state && state.players) || []).find((p) => p.seat === seat);
  return (row && row.name) || `seat ${seat + 1}`;
}

// historyEntry is the history line for one log event: what happened, and the impact a
// player can act on. It is pure — no DOM, no module state — so the copy is testable
// (T1.4). The caller decides how to draw it.
export function historyEntry(ev, s) {
  const cards = (ev.cards || []).map(historyCard).join(' ');
  switch (ev.type) {
    case 'setup':
      return { text: 'Game set up', impact: ev.note || '' };
    case 'enemy_reveal':
      return { text: 'Enemy appears: {cards}', cards: ev.cards, impact: '' };
    case 'play': {
      // A Jester is played alone and has no attack; the log used to print
      // "undefined attack" when the event carried no amount (campaign A3).
      const jester = Array.isArray(ev.cards) && ev.cards.some((id) => /^j[12]$/i.test(String(id)));
      return {
        text: 'Played {cards}',
        cards: ev.cards,
        impact: ev.note ? ev.note : jester ? 'jester' : `${Number(ev.amount) || 0} attack`,
      };
    }
    case 'hearts': {
      // The engine records how many cards actually went back, and the attack value it
      // asked for: a 5♥ with only three cards in the discard returns three, not five.
      const asked = Number(String(ev.note || '').replace(/[^0-9]/g, '')) || 0;
      const back = ev.amount || 0;
      const fewer = asked > back;
      return {
        text: cards ? `Hearts power (${cards})` : 'Hearts power',
        impact: `${back} card${back === 1 ? '' : 's'} returned under the tavern` +
          (fewer ? ` (${asked} asked — the discard pile was short)` : ''),
      };
    }
    case 'draw': {
      const who = (s.numPlayers || 1) > 1 && typeof ev.seat === 'number' ? historySeat(s, ev.seat) : '';
      return who
        ? { text: `${who} drew ${ev.amount} card${ev.amount === 1 ? '' : 's'}`, impact: '' }
        : { text: 'Drew to hand', impact: `${ev.amount} card${ev.amount === 1 ? '' : 's'}` };
    }
    case 'shield':
      return { text: 'Spades absorbed the attack', impact: `shield ${ev.amount}` };
    case 'damage':
      return { text: 'Damage dealt', impact: `${ev.amount}${ev.note ? ` (${ev.note})` : ''}` };
    case 'defeat': {
      const base = ev.note === 'exact' ? 'exact kill — the card goes on top of the tavern' : 'the card goes to the discard pile';
      // The rulebook is explicit: the killer skips step 4 and faces the next royal.
      // Say so, because a repeated turn reads as a bug when it is not explained.
      const next = s.rules && s.rules.passOnDefeat
        ? ' · the turn passes to the next seat'
        : ' · the same player faces the next royal';
      return { text: 'Defeated {cards}', cards: ev.cards, impact: base + next };
    }
    case 'recruit':
      return { text: '{cards} joins your deck', cards: ev.cards, impact: 'add it to your hand' };
    case 'suffer':
      return { text: 'Forfeited {cards}', cards: ev.cards, impact: `${ev.amount ? `${ev.amount} value — ` : ''}${ev.note || ''}` };
    case 'enemy_attack':
      if (ev.note === 'defended') return { text: 'Attack survived', impact: `paid ${ev.amount}` };
      if (ev.amount) return { text: 'Attack survived', impact: `paid ${ev.amount}` };
      return { text: 'Attack resolved', impact: 'no damage — nothing to pay' };
    case 'jester_flip':
      return { text: 'Jester flipped', impact: `hand refilled (${ev.amount} cards)` };
    case 'yield':
      return { text: 'Yielded the turn', impact: '' };
    case 'choose_next_seat':
      // The Jester's whole point is who goes next; "seat chosen" told the reader
      // nothing (playtest 4, note 7).
      return { text: `Chose ${historySeat(s, ev.amount)} to go next`, impact: '' };
    case 'win':
      return { text: 'Victory', impact: s.tier ? `${s.tier} tier` : '' };
    case 'lose':
      return { text: 'Defeat', impact: s.lostReason === 'cannot_defend' ? 'the attack could not be paid' : 'no cards and no Jesters' };
    case 'tier':
      return { text: 'Ranked', impact: ev.note || '' };
    default:
      return ev.note ? { text: ev.type.replace(/_/g, ' '), impact: ev.note } : null;
  }
}

// matchAwards is the multiplayer result board: who did the most of each thing the
// table can observe. Damage and defeats are consequences without a seat, so they use
// attributeSeats' actor; draws name their recipient; a Jester is a card in a play.
export function matchAwards(log, seats) {
  const n = Math.max(1, Number(seats) || 1);
  const damage = new Array(n).fill(0);
  const drawn = new Array(n).fill(0);
  const slain = new Array(n).fill(0);
  const jesters = new Array(n).fill(0);
  const actors = attributeSeats(log);
  const at = (seat) => Number.isInteger(seat) && seat >= 0 && seat < n;
  for (let i = 0; i < (log || []).length; i += 1) {
    const ev = log[i];
    if (!ev) continue;
    if (ev.type === 'damage' && at(actors[i])) damage[actors[i]] += Number(ev.amount) || 0;
    if (ev.type === 'defeat' && at(actors[i])) slain[actors[i]] += 1;
    if (ev.type === 'draw') {
      const seat = at(ev.seat) ? ev.seat : actors[i];
      if (at(seat)) drawn[seat] += Number(ev.amount) || 0;
    }
    if (ev.type === 'play' && at(ev.seat)) {
      for (const id of ev.cards || []) {
        if (/^j[12]$/i.test(String(id))) jesters[ev.seat] += 1;
      }
    }
  }
  const award = (key, label, values) => {
    const amount = Math.max(...values);
    const winners = amount > 0 ? values.flatMap((v, seat) => (v === amount ? [seat] : [])) : [];
    return { key, label, amount, seats: winners };
  };
  return [
    award('damage', 'Most Damage', damage),
    award('drawn', 'Most Drawn', drawn),
    award('slain', 'Most Slain Royals', slain),
    award('jesters', 'Most Jesters', jesters),
  ];
}
