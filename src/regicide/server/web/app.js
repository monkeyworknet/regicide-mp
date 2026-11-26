// Regicide UI — application shell.
// Vanilla ES modules, no build step. The server owns all rules; this file only
// renders the sanitised state and posts actions.

import { AudioManager } from './audio.js';
import { cardElement, backCard, loadCardText, textFor, suitSymbol, suitIcon, rankLabel, isRoyalRank, isJesterId } from './cards.js';
import {
  loadThemes, themes, backs, jokers, themeInfo, backInfo, jokerInfo, jesterArt, artUrl, backUrl,
  themeJoker, themeJokers, jokerPairFor, resolveJokerId,
  registryDefaultTheme, registryDefaultBack,
} from './themes.js';
import { SUIT_POWERS, formatDuration, discardValueOf, paymentLeft, paySelectionValid, paySelectionAfter, attributeSeats, seatColour, seatBubbles, historyEntry, revealsFor, royalName } from './format.js';
import { HEADLINES, LEDES, pickLine, resultHeading, defeatLine } from './voice.js';
import { endSheetModel } from './endsheet.js';
import {
  coop, adopt, seatHeader, host as hostRoom, join as joinRoom, watch as watchRoom, reclaim as reclaimSeat,
  refresh as refreshRoom, setReady, startGame, setWatching, seatRail, lobbyBody, isHost, isSpectator,
  startPolling, stopPolling, loadSeat, viewAs, play as playRoom, forgetSeat, forgetLostSeat, SEAT_COLOURS, invite, takeSeat, closeTable, playAgain, setBotSeat,
  startStreaming, stopStreaming, handOverHost, copyToClipboard,
  leaveTable, stopWatching, returnToOwnSeat, returnSeat, coveringSeat, kick as kickSeat, addBot, setRules, setBotFill, setTheme,
} from './coop.js';

const PREFS_KEY = 'regicide.prefs';
const SESSION_KEY = 'regicide.sessionId';

const DEFAULT_PREFS = {
  theme: 'official',
  // Set once the player picks a set on purpose; a fresh phone then keeps its
  // readable default instead of being switched back to art that cannot be read.
  themeExplicit: false,
  back: 'black',
  // The two Jester faces, in tray order. A set brings a pair (Original; Ditto and
  // Porygon) or asks for a random pair (Simple, Standard); the player can give each
  // Jester its own face, or the same face twice.
  jokers: [],
  // jokersPicked marks a pair the player chose by hand. The server keeps a hand
  // pick on any set; a generated pair follows the set's own rule instead.
  jokersPicked: false,
  animations: 'normal', // off | normal | fast
  hints: true,
  sound: true,
  volume: 0.6,
  // Rule options apply to the next run (a game's rules are frozen once it starts).
  rules: { soloJester: true, exactDamage: true, allowSoloYield: false, allowOverpayDiscard: false, chaos: false },
  // In a room, whose art this tab draws: the table's dealt set, or the player's own.
  // Cosmetic only; the server and the other seats never see it (D-87).
  tableDeck: 'table',
};

const app = {
  prefs: { ...DEFAULT_PREFS },
  audio: new AudioManager(),
  sessionId: null,
  seq: 0,
  state: null,
  selected: new Set(),
  busy: false,
  lastEvents: [],
  firstRender: true,
};

// ---------------------------------------------------------------- utilities

// Ids for action idempotency. crypto.randomUUID only exists in secure contexts
// (https or localhost), so a LAN deployment over plain http would throw on every
// move. Fall back to getRandomValues, then to a counter+time mix.
let actionSeq = 0;
function newActionId() {
  try {
    if (typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function') return crypto.randomUUID();
    if (typeof crypto !== 'undefined' && typeof crypto.getRandomValues === 'function') {
      const buf = new Uint8Array(16);
      crypto.getRandomValues(buf);
      return Array.from(buf, (b) => b.toString(16).padStart(2, '0')).join('');
    }
  } catch {
    /* fall through */
  }
  actionSeq += 1;
  return `a${Date.now().toString(36)}-${actionSeq}-${Math.random().toString(36).slice(2, 10)}`;
}

const $ = (sel, root = document) => root.querySelector(sel);
const $$ = (sel, root = document) => [...root.querySelectorAll(sel)];

function loadPrefs() {
  try {
    const raw = localStorage.getItem(PREFS_KEY);
    if (raw) app.prefs = { ...DEFAULT_PREFS, ...JSON.parse(raw) };
  } catch {
    /* ignore */
  }
  app.prefs.rules = { ...DEFAULT_PREFS.rules, ...(app.prefs.rules || {}) };
  // The 0.9.x phone fallback to Simple is gone: Official is the default everywhere.
  // Drop the bookkeeping keys an older save may still carry.
  delete app.prefs.themeAuto;
  delete app.prefs.themeStored;
  delete app.prefs.backStored;
  // Migrate the single-face preference from 0.8.7 and earlier.
  if (!Array.isArray(app.prefs.jokers) || app.prefs.jokers.length === 0) {
    app.prefs.jokers = app.prefs.joker ? [app.prefs.joker, app.prefs.joker] : [];
  }
  delete app.prefs.joker;
  // A save from 0.9.6 or earlier has no provenance flag. Treating it as a generated
  // pair is what the rule wants: a stale set-face pair is replaced at the next deal,
  // and a pair that fits the set is kept. New saves record a hand pick explicitly.
  if (typeof app.prefs.jokersPicked !== 'boolean') app.prefs.jokersPicked = false;
  // The pointer-dot layer is gone (D-85); a saved key would be re-persisted forever.
  delete app.prefs.playerDots;
  app.audio.setEnabled(app.prefs.sound);
  app.audio.setVolume(app.prefs.volume);
}

function savePrefs() {
  try {
    // Persist what the player actually picked; there is no viewport substitution.
    const out = { ...app.prefs };
    delete out.themeStored;
    delete out.backStored;
    delete out.themeAuto;
    localStorage.setItem(PREFS_KEY, JSON.stringify(out));
  } catch {
    /* ignore */
  }
  applyPrefs();
}

// Fine-detail art that does not survive a 58 px hand.
const SMALL_ART_THEMES = new Set(['official', 'pokemon']);

// COOP_SETS mirrors the server's allow-list (room.go coopThemes): the sets a table may
// deal. The server is the authority; this only decides what Host a table sends.
const COOP_SETS = new Set(['official', 'simple', '8bit', 'pokemon']);

function isPhone() {
  return window.matchMedia('(max-width: 559px)').matches;
}

// renderThemeNote warns on a phone when the chosen set is hard to read there.
function renderThemeNote() {
  const note = $('#theme-note');
  if (!note) return;
  const art = themeInfo(app.prefs.theme);
  if (!isPhone() || !SMALL_ART_THEMES.has(art.id)) {
    note.hidden = true;
    note.replaceChildren();
    return;
  }
  note.hidden = false;
  note.replaceChildren(
    el('span', { text: `${art.label} art is detailed — small on a phone. ` }),
  );
  const swap = el('button', { type: 'button', class: 'linkish', text: 'Use Simple' });
  swap.addEventListener('click', () => {
    app.prefs.theme = 'simple';
    app.prefs.themeExplicit = true;
    const preferred = themeInfo('simple').defaultBack;
    if (preferred) app.prefs.back = preferred;
    // applyPrefs re-renders the tiles and the preview, so the menu agrees with the
    // switch the player just made.
    savePrefs();
  });
  note.append(swap);
}

function applyPrefs() {
  document.body.dataset.theme = app.prefs.theme;
  if ($('#theme-choices')) renderThemeChoices();
  document.body.classList.toggle('anim-off', app.prefs.animations === 'off');
  document.body.classList.toggle('anim-fast', app.prefs.animations === 'fast');
}

async function api(path, options = {}) {
  const res = await fetch(path, {
    headers: { 'content-type': 'application/json', accept: 'application/json' },
    ...options,
  });
  const text = await res.text();
  const body = text ? JSON.parse(text) : null;
  if (!res.ok && res.status !== 409) {
    const message = body && (body.error || body.message) ? body.error || body.message : res.statusText;
    const err = new Error(message || `request failed (${res.status})`);
    err.status = res.status;
    throw err;
  }
  return { status: res.status, body };
}

// ---------------------------------------------------------------- session

async function createGame({ seed = '', theme = app.prefs.theme, rules = undefined } = {}) {
  const { body } = await api('/api/sessions', {
    method: 'POST',
    body: JSON.stringify({ seed, theme, rules: rules || app.prefs.rules, jokers: app.prefs.jokers, jokersCustom: app.prefs.jokersPicked }),
  });
  adoptSession(body);
  return body;
}

async function resumeGame(id) {
  const { status, body } = await api(`/api/sessions/${id}`, { method: 'GET' }).catch((err) => ({ status: err.status, body: null }));
  if (status !== 200 || !body) {
    try {
    localStorage.removeItem(SESSION_KEY);
  } catch {
    /* nothing to clear */
  }
    return false;
  }
  adoptSession(body);
  return true;
}

// leaveSolo drops the solo run this tab was holding.
function leaveSolo() {
  app.sessionId = null;
  try {
    localStorage.removeItem(SESSION_KEY);
  } catch {
    /* storage blocked: nothing to forget */
  }
  app.state = null;
  app.selected.clear();
}

function adoptSession(body) {
  // A solo run takes the tab back from any room it was showing.
  if (coop.code) {
    stopUpdates();
    coop.code = '';
    coop.seat = -1;
    coop.token = '';
    coop.watchId = '';
    coop.watchingAs = -1;
    coop.state = null;
    coop.room = null;
    coop.seq = 0;
  }
  // Every path that loads a run goes through here, so the per-game result
  // heading is cleared here rather than in each caller.
  endTitle = null;
  app.sessionId = body.sessionId;
  app.seq = body.seq;
  app.state = body.state;
  app.selected.clear();
  app.lastEvents = [];
  app.firstRender = false;
  // A new deal (or a restart) starts a new story: a solo royal panel from the last
  // one must not drain over it.
  clearTableReveals();
  localStorage.setItem(SESSION_KEY, body.sessionId);
  if (location.search.includes('session=')) {
    const url = new URL(location.href);
    url.searchParams.delete('session');
    history.replaceState(null, '', url);
  }
  showBoard();
  render();
}

async function act(action) {
  // The mode is decided by where the board came from. A room never posts to a solo
  // session and a solo run never posts to a room (F-1b), whatever ids are left over.
  if (coop.code) return actRoom(action);
  if (!app.sessionId || app.busy) return null;
  app.busy = true;
  setBusyUI(true);
  try {
    const { status, body } = await api(`/api/sessions/${app.sessionId}/actions`, {
      method: 'POST',
      body: JSON.stringify({
        actionId: newActionId(),
        expectedSeq: app.seq,
        action,
      }),
    });
    if (status === 409 && body && body.state) {
      // Stale view: adopt the server's truth and tell the player. The selection is
      // dropped too — an id that no longer exists in the hand would otherwise keep
      // the action bar labelled for a discard the click cannot perform.
      app.seq = body.seq ?? app.seq;
      app.state = body.state;
      app.selected.clear();
      toast('Synchronised with the server');
      render();
      return null;
    }
    if (!body) return null;
    app.seq = body.seq;
    app.state = body.state;
    app.selected.clear();
    animateEvents(body.events || []);
    render();
    return body;
  } catch (err) {
    toast(err.message || 'That move was not allowed');
    return null;
  } finally {
    app.busy = false;
    setBusyUI(false);
  }
}

// actRoom posts one action to the table this seat belongs to. The server answers a
// refusal with the caller's own current state, so a desynchronised board repairs in
// one round trip instead of retrying a move that was legal a moment ago.
async function actRoom(action) {
  if (app.busy || !coop.state) return null;
  if (coop.watchId) {
    // Read-only by construction: the server would refuse anyway, but a spectator
    // should never be offered a button that cannot work.
    toast('Watching — this table is read-only.');
    return null;
  }
  app.busy = true;
  setBusyUI(true);
  try {
    const body = await playRoom(action, newActionId());
    app.seq = coop.seq;
    app.state = coop.state;
    app.selected.clear();
    animateEvents(body.events || []);
    render();
    return body;
  } catch (err) {
    if ((err.status === 409 || err.status === 403) && err.body && err.body.state) {
      adopt(err.body);
      app.seq = coop.seq;
      if (coop.state) app.state = coop.state;
      app.selected.clear();
      toast(err.status === 409 ? 'Synchronised with the server' : 'Not your turn yet');
      render();
      return null;
    }
    if (err.status === 401) {
      // The seat died: the host took it back, or the room was reset. Drop it and go
      // back to the lobby rather than leave a board that cannot act.
      loseRoomSeat();
      return null;
    }
    toast(err.message || 'That move was not allowed');
    return null;
  } finally {
    app.busy = false;
    setBusyUI(false);
  }
}

// clearRoom forgets the table entirely: identity, board and transport. The mode flag
// (coop.code) goes last, because everything above keys off it.
function clearRoom() {
  stopUpdates();
  forgetSeat(coop.code);
  coop.code = '';
  coop.seat = -1;
  coop.token = '';
  coop.watchId = '';
  coop.watchingAs = -1;
  coop.state = null;
  coop.room = null;
  coop.lobby = true;
  coop.seq = 0;
  coop.status = '';
  coop.presence = null;
  app.state = null;
  app.selected.clear();
  // A solo run after a table is solo: the co-op classes decided layout, the tavern
  // count and the action-bar gate, and nothing else removed them.
  document.body.classList.remove('is-coop');
  document.body.classList.remove('is-over');
  // The rail is rebuilt only by renderSeats while a room exists. Leaving the table
  // returned to the menu with four stale seat rows still in the hidden board (playtest
  // 3 campaign, A17). Clear the co-op furniture with the state that fed it.
  const rail = $('#seats');
  if (rail) { rail.replaceChildren(); rail.hidden = true; }
  const hand = $('#hand');
  if (hand) hand.replaceChildren();
  // A reveal belongs to the table, not the menu: a kick or a close during one used to
  // leave the panel over the setup screen and drain the queue there.
  clearTableReveals();
  const banner = $('#turn-banner');
  if (banner) { banner.replaceChildren(); banner.hidden = true; }
}

// leaveTableAndMenu tells the server this seat is gone, then returns to the menu. The
// server is what frees the chair: the old button only hid the screen, so the seat stayed
// occupied until the away timer ran out (note 2).
async function leaveTableAndMenu() {
  if (coop.code && coop.watchId) {
    // A spectator's exit is its own route: skipping the leave call left the watch slot
    // held until the 60-second expiry.
    return stopWatchingAndLeave();
  }
  if (coop.code) {
    try {
      await leaveTable();
    } catch (err) {
      // A room that has already ended has nothing to leave, and refusing to move
      // would strand the player on a dead table. Any other failure means the chair is
      // still this seat's: forgetting the token would make that permanent.
      const gone = err && (err.status === 401 || err.status === 404 || err.status === 409);
      if (!gone) {
        toast(err.message || 'Could not leave the table — try again');
        return;
      }
    }
  }
  stopUpdates();
  clearRoom();
  history.replaceState(null, '', '/');
  showSetup();
  toast('Left the table');
}

// stopWatchingAndLeave gives up a spectator slot and returns to the menu.
async function stopWatchingAndLeave() {
  try {
    await stopWatching();
  } catch (err) {
    if (!err || err.status !== 401) toast(err.message || 'Could not stop watching');
  }
  stopUpdates();
  clearRoom();
  history.replaceState(null, '', '/');
  showSetup();
  toast('Stopped watching');
}

// dropWatchOnExit releases the spectator slot when the tab goes away. The server
// expires a silent watcher after 60 s anyway; this is the difference between a seat
// freeing up when someone leaves and a minute later.
function dropWatchOnExit() {
  if (!coop.code || !coop.watchId) return;
  // The menu's "Stop watching" calls the authenticated route; this is the unload path
  // and cannot await anything.
  try {
    fetch(`/api/rooms/${encodeURIComponent(coop.code)}/watch`, {
      method: 'DELETE',
      headers: { 'X-Regicide-Watch': coop.watchId },
      keepalive: true,
    }).catch(() => {});
  } catch {
    /* a page on its way out cannot report its own failure */
  }
}

// loseRoomSeat forgets the identity the server no longer honours. The tab has no
// way to read the room any more, so it goes back to the lobby and says why rather
// than leaving a board on screen that cannot act.
//
// It never joins on its own: a tab whose chair was taken used to fall through to
// join() on the next entry and land in the lowest free chair, holding that chair's
// hand without a word (playtest 3, Phase B seat-identity incident).
function loseRoomSeat(reason) {
  stopUpdates();
  // A tab that was covering another chair still owns the one in its return record.
  const own = returnSeat();
  forgetLostSeat(coop.code);
  coop.token = '';
  coop.seat = -1;
  coop.watchingAs = -1;
  coop.state = null;
  coop.lobby = true;
  coop.status = 'seat_lost';
  app.state = null;
  app.selected.clear();
  showLobby();
  // The URL must stop naming a seat this tab no longer holds: a reload would re-enter
  // the room with no stored seat and join the lowest free chair without asking — the
  // playtest-3 incident, one refresh later.
  history.replaceState(null, '', '/');
  seatLostScreen(
    reason || `Your seat at table ${coop.code} is gone. Another device took it, or the host removed it.`,
    own,
  );
}

// seatLostScreen draws the explanation and, when there is a way back, the one tap that
// takes it. Nothing here joins or switches on its own: a tab whose chair was taken used
// to land in the lowest free chair with that chair's hand and no word said (playtest 3,
// Phase B seat-identity incident). Joining a dealt table means taking the bot's or the
// departed player's cards, so the screen says so before the tap.
function seatLostScreen(reason, own) {
  const body = $('#lobby-body');
  if (!body) return;
  const note = el('p', { class: 'lobby-note' }, reason);
  const hint = el('p', { class: 'lobby-note', id: 'seat-lost-hint' }, '');
  hint.hidden = true;
  let action;
  if (own) {
    // The covered chair died; this tab's own chair is still the server's.
    action = el('button', { type: 'button', class: 'primary', id: 'btn-return-seat' }, 'Return to your own seat');
    action.addEventListener('click', async () => {
      action.disabled = true;
      hint.hidden = true;
      try {
        await returnToOwnSeat();
      } catch (err) {
        hint.hidden = false;
        hint.textContent = err.message || 'That seat is no longer yours either.';
        return;
      }
      coop.status = '';
      history.replaceState(null, '', `/?room=${coop.code}`);
      renderRoomScreen();
      startUpdates(() => renderRoomScreen());
      toast('Back in your own seat');
    });
  } else {
    action = el('button', { type: 'button', class: 'primary', id: 'btn-take-chair' }, 'Take a free chair');
    action.addEventListener('click', async () => {
      action.disabled = true;
      hint.hidden = true;
      try {
        await joinRoomRoom(coop.code);
      } catch (err) {
        hint.hidden = false;
        const code = err && err.body && err.body.error;
        if (err.status === 409 && code === 'game_started') {
          hint.textContent = 'That table has no free chair. Ask the host for a reconnect link.';
        } else if (err.status === 409 && code === 'game_cancelled') {
          hint.textContent = 'The host closed that table.';
        } else if (err.status === 404) {
          hint.textContent = 'That table has ended.';
        } else {
          hint.textContent = err.message || 'Could not join the table.';
        }
        // A table with no chair, or no table, will not answer differently on a retry.
        if (err.status === 404 || (err.status === 409 && (code === 'game_started' || code === 'game_cancelled'))) return;
        action.disabled = false;
        return;
      }
      const mine = ((coop.room && coop.room.seats) || []).find((row) => row.you);
      const name = mine && mine.name ? `${mine.name}'s chair` : 'a free chair';
      coop.status = '';
      history.replaceState(null, '', `/?room=${coop.code}`);
      renderRoomScreen();
      startUpdates(() => renderRoomScreen());
      toast(`You are now in ${name}`);
    });
  }
  const warning = own ? null : el('p', { class: 'lobby-note' }, 'At a dealt table a free chair is a bot\'s or a departed player\'s: you take over its cards.');
  body.replaceChildren(...[note, action, warning, hint].filter(Boolean));
}

// endTitle is the result heading drawn for the current game (a victory title or
// a loss heading). It is drawn once so
// an unrelated re-render that recreates the end sheet (theme change, restart of
// the sheet itself) cannot change the heading under the player's eyes.
let endTitle = null;

async function restartGame() {
  if (!app.sessionId) return;
  const { body } = await api(`/api/sessions/${app.sessionId}/restart`, {
    method: 'POST',
    body: JSON.stringify({ theme: app.prefs.theme, rules: app.prefs.rules, jokers: app.prefs.jokers, jokersCustom: app.prefs.jokersPicked }),
  });
  adoptSession(body);
}

// ---------------------------------------------------------------- screens


// renderThemeChoices fills the setup form from the registry, so a new card set
// appears on the menu without touching this file.
function renderThemeChoices() {
  const box = $('#theme-choices');
  if (!box) return;
  box.replaceChildren();
  const active = themeInfo(app.prefs.theme).id;
  app.prefs.theme = active;
  // The setup screen shows the same art tiles as Settings: a card set is chosen by
  // looking at it, and the sample line shows which sets print their own rule text.
  for (const t of themes()) {
    const tile = el('button', {
      type: 'button',
      class: 'tile theme-tile',
      'aria-pressed': String(t.id === active),
      'data-theme-id': t.id,
      title: t.label,
    });
    const art = el('span', { class: 'tile-art' });
    const img = document.createElement('img');
    img.src = artUrl(t.id, 'QS');
    img.alt = '';
    img.loading = 'lazy';
    if (t.pixelated) img.classList.add('is-pixelated');
    art.append(img);
    if (t.hasText) art.append(el('span', { class: 'tile-sample', text: 'Queen of Spades' }));
    tile.append(art, el('span', { class: 'tile-label', text: t.label }));
    tile.addEventListener('click', () => {
      app.prefs.theme = t.id;
      app.prefs.themeExplicit = true;
      // A set brings its own card back and its own Jester pair (or a random pair).
      if (themeInfo(t.id).defaultBack) app.prefs.back = themeInfo(t.id).defaultBack;
      app.prefs.jokers = jokerPairFor(t.id);
      app.prefs.jokersPicked = false;
      applyPrefs();
      savePrefs();
      for (const other of box.querySelectorAll('.theme-tile')) {
        other.setAttribute('aria-pressed', String(other.dataset.themeId === t.id));
      }
      renderMenuPreview();
      renderThemeNote();
    });
    box.append(tile);
  }
  renderMenuPreview();
  renderThemeNote();
}

// renderMenuPreview keeps the setup screen's face+back preview in step with the
// radio buttons, so the choice is visible before the run starts.
function renderMenuPreview() {
  const host = $('#menu-preview');
  if (!host) return;
  host.replaceChildren(previewBlock(app.prefs.theme, app.prefs.back, app.prefs.jokers));
}

// rollMenuCopy draws one headline and one lede each time the menu is shown, so
// the setup screen says something different on a later visit (Minecraft-style
// rotating tagline). The two draws are independent, as are the pools.
function rollMenuCopy() {
  const headline = $('#setup h1');
  const lede = $('#setup .lede');
  if (headline) headline.textContent = pickLine(HEADLINES);
  if (lede) lede.textContent = pickLine(LEDES);
}

// loadRoomList fills the open-tables list on the setup screen (D-39). A lobby is
// joinable; a table in play is watchable, never joinable — the same rule the server
// enforces, shown rather than discovered.
async function loadRoomList() {
  const host = $('#room-list');
  if (!host) return;
  let rooms = [];
  try {
    const res = await fetch('/api/rooms', { cache: 'no-store' });
    if (res.ok) {
      const body = await res.json();
      rooms = Array.isArray(body.rooms) ? body.rooms : [];
    }
  } catch {
    rooms = [];
  }
  // Oldest-looking leftovers are not worth a row; the store garbage-collects anyway.
  const open = rooms.filter((r) => r && r.code);
  if (open.length === 0) {
    host.hidden = true;
    host.replaceChildren();
    return;
  }
  open.sort((a, b) => Number(b.lobby === true) - Number(a.lobby === true) || String(a.code).localeCompare(String(b.code)));
  host.hidden = false;
  host.replaceChildren(el('p', { class: 'room-list-title', text: 'Tables on this server' }));
  for (const room of open.slice(0, 8)) {
    const row = el('div', { class: 'room-row', 'data-code': room.code });
    const label = el('span', { class: 'room-label' });
    label.append(
      el('b', { class: 'room-code', text: room.code }),
      el('span', { class: 'room-meta', text: `${room.seats || 1}/${room.capacity || 4} · ${room.theme || 'simple'} · ${room.lobby ? 'open' : room.phase || 'in play'}` }),
    );
    row.append(label);
    if (room.lobby) {
      row.append(el('button', {
        type: 'button',
        class: 'ghost room-join',
        text: 'Join',
        'data-code': room.code,
        onclick: () => {
          history.replaceState(null, '', `/?room=${room.code}`);
          enterRoom(room.code, new URLSearchParams({ room: room.code }));
        },
      }));
    } else {
      row.append(el('button', {
        type: 'button',
        class: 'ghost room-watch',
        text: 'Watch',
        'data-code': room.code,
        onclick: () => {
          history.replaceState(null, '', `/?room=${room.code}&watch=1`);
          enterRoom(room.code, new URLSearchParams({ room: room.code, watch: '1' }));
        },
      }));
    }
    host.append(row);
  }
}

// showMenuButton keeps the desktop Menu reachable outside a game (Settings, Credits,
// Leaderboard). On a phone the top bar already has #btn-nav, and #topbar-nav is hidden.
function showMenuButton() {
  const menu = $('#btn-menu');
  if (menu) menu.hidden = window.matchMedia('(max-width: 559px)').matches;
}

function showSetup({ keepSeed = false } = {}) {
  rollMenuCopy();
  loadRoomList();
  const seedInput = $('#seed-input');
  if (seedInput && !keepSeed) seedInput.value = '';
  $('#setup').hidden = false;
  $('#board').hidden = true;
  // Leaving a room used to uncover the lobby underneath the menu, with its dead-room
  // note still on screen (campaign B10).
  $('#lobby').hidden = true;
  showMenuButton();
  $('#btn-history').hidden = true;
  startRoomListRefresh();
  $('#setup-hint').textContent = '';
}

// A table can close while the menu is open. The list was loaded once and kept a stale
// row with a Watch button (campaign B10); refresh it while the menu is visible.
let roomListTimer = null;
function startRoomListRefresh() {
  if (roomListTimer) clearInterval(roomListTimer);
  roomListTimer = setInterval(() => {
    if (!$('#setup').hidden) loadRoomList();
  }, 15000);
}
function stopRoomListRefresh() {
  if (roomListTimer) clearInterval(roomListTimer);
  roomListTimer = null;
}

// showLobby is the waiting room: the room code, who has sat down, and the start
// controls. It is a full screen, because a lobby is a different question to a game.
// renderWatchNote explains a spectator's screen: whose board is drawn, that it is
// read-only, and how to look at a hand. Without it an empty hand looks broken.
function renderWatchNote() {
  let note = $('#watch-note');
  if (!coop.watchId) {
    if (note) note.hidden = true;
    return;
  }
  if (!note) {
    note = document.createElement('p');
    note.id = 'watch-note';
    note.className = 'watch-note';
    note.setAttribute('role', 'status');
    const board = $('#board');
    if (!board || !board.parentNode) return;
    // Sits above the board as its own bar: inside the board grid it would be
    // auto-placed after the action bar, which is where the first version ended up.
    board.parentNode.insertBefore(note, board);
  }
  note.hidden = false;
  const as = coop.watchingAs;
  const seat = as >= 0 && coop.state && coop.state.players
    ? coop.state.players.find((p) => p.seat === as)
    : null;
  if (coop.status === 'watch_ended') {
    note.textContent = 'Watching was switched off for this table.';
  } else if (seat) {
    note.textContent = `Watching as ${seat.name} · read-only · tap a player to switch`;
  } else {
    note.textContent = coop.room && coop.room.watchHands
      ? 'Watching · read-only · tap a player to see their hand'
      : 'Watching · read-only';
  }
}

// renderConnChip says when the table stopped answering. The poller gives up after
// three consecutive failures rather than retrying forever, so the player needs a
// way back in that is not a page reload.
function renderConnChip() {
  let chip = $('#conn-chip');
  const offline = coop.status === 'offline';
  if (!offline) {
    if (chip) chip.hidden = true;
    return;
  }
  if (!chip) {
    chip = document.createElement('p');
    chip.id = 'conn-chip';
    chip.className = 'conn-chip';
    chip.setAttribute('role', 'status');
    chip.setAttribute('aria-live', 'polite');
    const board = $('#board');
    if (!board || !board.parentNode) return;
    board.parentNode.insertBefore(chip, board);
  }
  chip.hidden = false;
  chip.replaceChildren(
    el('span', '', 'The table stopped answering.'),
    el('button', { type: 'button', class: 'link-btn' }, 'Try again'),
  );
  chip.querySelector('button').addEventListener('click', () => {
    coop.status = '';
    chip.hidden = true;
    startUpdates(() => renderRoomScreen());
  });
}

// setModeLabel prints what this tab is: solo, a co-op table, or a spectator view.
function setModeLabel(text) {
  const slot = document.querySelector('.brand-text small');
  if (!slot) return;
  const version = ($('#app-version') || {}).textContent || '';
  // This binary serves solo AND co-op, so its baseline label is "multi". It read
  // "solo" here, which made the multiplayer build look like the single-player one in
  // the very corner that names the build.
  slot.textContent = `${text || 'multi'} `;
  // The version stays a span in every mode. Dropping it in a room (the old code wrote
  // "co-op " and nothing else) also lost the build label for the rest of the session,
  // because the health response had nothing left to fill.
  const span = document.createElement('span');
  span.className = 'brand-version';
  span.id = 'app-version';
  span.textContent = version;
  slot.append(span);
}

function showLobby() {
  document.body.classList.remove('is-coop');
  $('#setup').hidden = true;
  $('#board').hidden = true;
  stopRoomListRefresh();
  $('#lobby').hidden = false;
  showMenuButton();
  $('#btn-history').hidden = true;
}

// enterRoom joins, reclaims, or watches, then draws whichever screen the room is in.
async function enterRoom(code, params) {
  // A room and a solo run are different tables. Entering one ends the other, so a
  // stale solo session can never be the thing a co-op click posts to (F-1b).
  leaveSolo();
  // The room must be known before any call: refresh() reads coop.code.
  coop.code = code.toUpperCase();
  try {
    const rejoin = params.get('rejoin');
    const seatParam = params.get('seat');
    coop.code = code.toUpperCase();
    if (rejoin && seatParam !== null) {
      await reclaimSeat(code, Number(seatParam), rejoin);
      // The key is spent; drop it from the URL so a reload cannot re-send it.
      const clean = new URLSearchParams({ room: code });
      history.replaceState(null, '', `/?${clean}`);
    } else if (params.get('watch') === '1') {
      await watchRoom(code);
    } else if (!loadSeat(code)) {
      // A player who has been in this room before gets their seat back; anyone else
      // gets a seat here, because following a room link is joining.
      await joinRoomRoom(code);
    } else {
      // loadSeat restored the token, so this read is already authenticated.
      await refreshRoom();
    }
  } catch (err) {
    if (err.status === 401) {
      // The stored token is dead: the chair was taken by another device, or the host
      // removed it. Joining the lowest free chair here silently seated the tab in
      // somebody else's chair with that chair's hand (playtest 3, Phase B: seat 1 came
      // back as seat 3). Say what happened; the player taps to take a chair.
      loseRoomSeat('Your seat at this table is gone. Another device took it, or the host removed it.');
      return;
    } else if (err.status === 409 && err.body && err.body.error === 'bad_rejoin_key') {
      showSetup();
      $('#coop-hint').textContent = 'That reconnect link has expired or was already used. Ask the host for another.';
      return;
    } else if (err.status === 409 && err.body && err.body.error === 'game_started') {
      // A tab with no seat record (a browser restart, a new device) cannot join a dealt
      // table, and the menu used to say only "Could not open that room". The host can
      // mint a one-use link for that chair, so say so.
      showSetup();
      $('#coop-hint').textContent = 'This table has already dealt. Ask the host for a reconnect link.';
      return;
    } else if (err.status === 404 || err.status === 410) {
      showSetup();
      $('#coop-hint').textContent = 'That room has ended.';
      return;
    } else {
      showSetup();
      $('#coop-hint').textContent = 'Could not open that room.';
      return;
    }
  }
  renderRoomScreen();
  startUpdates(() => renderRoomScreen());
}

async function joinRoomRoom(code) {
  coop.code = code;
  await joinRoom(code);
}

// transport is how the table keeps itself current: the stream (default, D-34) or the
// poll loop, kept for one release behind ?transport=poll (T1.5).
let transport = 'sse';

// startUpdates keeps the room screen fresh. The stream is the normal path; a browser
// that cannot stream falls back to polling rather than showing a dead board.
async function startUpdates(onChange) {
  stopPolling();
  stopStreaming();
  if (transport === 'sse') {
    try {
      // Presence never re-renders the board: at 10 Hz it would fight the selection
      // and the animations (MP-PLAN §4). It only updates the rail.
      await startStreaming(onChange);
      return;
    } catch (err) {
      // Kept on the window so a failed stream is diagnosable from the console rather
      // than silently degrading to a slower table.
      window.__liveError = String(err && err.message ? err.message : err);
    }
  }
  startPolling(onChange, 1500);
}

function stopUpdates() {
  stopPolling();
  stopStreaming();
}

// ---------- the table reveal ------------------------------------------------
//
// Every play and every payment is shown large in the middle of the table for a moment,
// then flies to the pile it belongs in. It is for the OTHER seats: the player who just
// moved already saw their cards in their own hand, and a big copy on their own screen
// would only cover the board they are reading (playtest 4, note 8).
//
// Nothing here is interactive: no dismiss button, no pointer capture. A play with
// several cards shows every card at once, because they left the hand together.

const REVEAL_HOLD = 1400;
const REVEAL_FLY = 420;
const ROYAL_HOLD = 1300;

const revealQueue = [];
let revealBusy = false;

function svgIcon(kind) {
  const NS = 'http://www.w3.org/2000/svg';
  const svg = document.createElementNS(NS, 'svg');
  svg.setAttribute('viewBox', '0 0 24 24');
  svg.setAttribute('aria-hidden', 'true');
  const path = (d, fill) => {
    const p = document.createElementNS(NS, 'path');
    p.setAttribute('d', d);
    p.setAttribute('fill', fill || 'none');
    p.setAttribute('stroke', fill ? 'none' : 'currentColor');
    p.setAttribute('stroke-width', '2');
    p.setAttribute('stroke-linecap', 'round');
    p.setAttribute('stroke-linejoin', 'round');
    svg.append(p);
  };
  if (kind === 'defend') {
    // Material Design Icons `shield`, filled (Apache 2.0). Filled rather than
    // outlined so it matches the swords at reveal size.
    path('M12 1L3 5v6c0 5.55 3.84 10.74 9 12c5.16-1.26 9-6.45 9-12V5z', 'currentColor');
  } else if (kind === 'defeat' || kind === 'enter') {
    // Material Design Icons `crown` (Apache 2.0): a royal's fall and a royal's
    // arrival are both a crown; the label and the card carry the difference.
    path('M5 16 3 5l5.5 5L12 4l3.5 6L21 5l-2 11H5m14 3H5v2h14v-2z', 'currentColor');
  } else if (kind === 'jester') {
    path('M12 2 15 9l7 1-5 5 1.2 7L12 18.6 5.8 22 7 15 2 10l7-1z');
  } else {
    // Material Design Icons `sword-cross`, filled (Apache 2.0). The hand-drawn
    // strokes read as sticks at reveal size.
    path('m6.2 2.44 11.9 11.9 2.12-2.12 1.41 1.41-2.47 2.47 3.18 3.18c.39.39.39 1.02 0 1.41l-.71.71a.996.996 0 0 1-1.41 0L17 18.23l-2.44 2.47-1.41-1.41 2.12-2.12-11.9-11.9V2.44zM15.89 10l4.74-4.74V2.44H17.8l-4.74 4.74zm-4.95 5-2.83-2.87-2.21 2.21-2.12-2.12-1.41 1.41 2.47 2.47-3.18 3.19a.996.996 0 0 0 0 1.41l.71.71c.39.39 1.02.39 1.41 0L7 18.23l2.44 2.47 1.41-1.41-2.12-2.12z', 'currentColor');
  }
  return svg;
}

// flushTableReveal consumes the event batch the last adopt captured. It is called from
// the board render, which is the one place a frame is known to be on screen already.
function flushTableReveal() {
  const pending = coop.pendingReveal;
  if (!pending) return;
  coop.pendingReveal = null;
  if (!coop.code || coop.lobby || !coop.state) return;
  const events = pending.events || [];
  for (const item of revealsFor(events, attributeSeats(events))) {
    // The mover saw their own cards leave the hand; a royal's fall is the table's
    // news, so the striker sees that panel too.
    if (!item.royal && item.seat === coop.seat) continue;
    queueTableReveal(item);
  }
}

// REVEAL_QUEUE_MAX bounds the backlog. A royal turn can add three panels (attack,
// defeat, entry) while a bot table moves every 2.4 s, so the cap is five and a full
// queue drops the oldest table reveal before it drops royal news.
const REVEAL_QUEUE_MAX = 5;

// clearTableReveals drops any panel and its backlog. A reveal belongs to one game:
// a kick, a close or a new deal must not drain the old table's news over the menu.
function clearTableReveals() {
  document.querySelectorAll('#table-reveal').forEach((n) => n.remove());
  revealQueue.length = 0;
  revealBusy = false;
}

function queueTableReveal(item) {
  if (!item || !Array.isArray(item.cards) || item.cards.length === 0) return;
  revealQueue.push(item);
  while (revealQueue.length > REVEAL_QUEUE_MAX) {
    const drop = revealQueue.findIndex((q) => !q.royal);
    revealQueue.splice(drop >= 0 ? drop : 0, 1);
  }
  if (!revealBusy) nextTableReveal();
}

function nextTableReveal() {
  const item = revealQueue.shift();
  if (!item) {
    revealBusy = false;
    return;
  }
  revealBusy = true;
  const state = coop.state || app.state || {};
  const theme = state.theme || app.prefs.theme;
  const royal = Boolean(item.royal);
  const meta = royal ? null : seatMeta(state, item.seat);
  const word = item.kind === 'defend' ? 'defends' : item.kind === 'jester' ? 'plays a Jester' : 'attacks';
  // The panel is visual; the sentence goes to the persistent live region, because a
  // role=status region inserted together with its text is often never announced.
  const overlay = el('div', { id: 'table-reveal', class: `table-reveal is-${item.kind}${royal ? ' is-royal' : ''}` });
  overlay.setAttribute('aria-hidden', 'true');
  // Defeat reads as a loss (red), an arrival as a crown (gold).
  const royalColour = item.kind === 'defeat' ? 'var(--seat-red)' : 'var(--gold)';
  overlay.style.setProperty('--seat-c', royal ? royalColour : `var(--seat-${meta.colour || 'red'})`);
  const panel = el('div', { class: 'reveal-panel' });
  const symbol = el('div', { class: 'reveal-symbol' });
  symbol.append(svgIcon(item.kind));
  // The royal's own name is the subject: "Jack of Spades is Defeated", "Jack of
  // Diamonds enters the fight". The card text names it once the sheet has loaded;
  // royalName is the fallback so a slow fetch cannot print an id or nothing.
  const info = royal ? textFor(item.cards[0]) : null;
  const royalTitle = royal ? (info && info.title) || royalName(item.cards[0]) : '';
  const label = royal
    ? el('p', { class: 'reveal-label' },
      el('b', {}, royalTitle),
      item.kind === 'defeat' ? ' is Defeated' : ' enters the fight')
    : el('p', { class: 'reveal-label' }, el('b', {}, meta.name), ` ${word}`);
  const cards = el('div', { class: 'reveal-cards' });
  for (const id of item.cards) {
    // A royal's alt text is its name; a played card keeps its id, which is what a
    // screen reader user reads on the board too.
    // A Jester has no face in the deck art: it must come from the catalogue pair,
    // or the panel shows the retired per-deck j1/j2 file that Settings never lists.
    const src = isJesterId(id)
      ? jesterArt(theme, tableJokers(state))[String(id).toUpperCase() === 'J2' ? 1 : 0]
      : artUrl(theme, id);
    cards.append(el('img', {
      class: 'reveal-card',
      alt: royal ? royalTitle : id,
      src,
      loading: 'eager',
    }));
  }
  panel.append(symbol, label, cards);
  overlay.append(panel);
  document.body.append(overlay);
  const announcer = $('#table-announcer');
  if (announcer) {
    announcer.textContent = royal
      ? `${royalTitle} ${item.kind === 'defeat' ? 'is defeated' : 'enters the fight'}`
      : `${meta.name} ${word} ${item.cards.length === 1 ? '1 card' : `${item.cards.length} cards`}`;
  }

  const still = app.prefs.animations === 'off'
    || (typeof matchMedia === 'function' && matchMedia('(prefers-reduced-motion: reduce)').matches);
  // A royal panel has no pile to fly to: it is news about the table, not a card
  // leaving a hand, so it holds and fades in place.
  const hold = still ? 1000 : royal ? ROYAL_HOLD : REVEAL_HOLD;
  setTimeout(() => {
    if (still || royal) {
      overlay.classList.add('is-fading');
      setTimeout(() => { overlay.remove(); nextTableReveal(); }, 260);
      return;
    }
    const target = $(item.kind === 'attack' ? '#pile-attack' : '#pile-discard');
    const to = target ? target.getBoundingClientRect() : null;
    const imgs = [...cards.querySelectorAll('.reveal-card')];
    overlay.classList.add('is-flying');
    for (const img of imgs) {
      const from = img.getBoundingClientRect();
      if (!to || to.width === 0) {
        img.style.opacity = '0';
        continue;
      }
      const dx = (to.left + to.width / 2) - (from.left + from.width / 2);
      const dy = (to.top + to.height / 2) - (from.top + from.height / 2);
      const scale = Math.max(0.25, Math.min(1, to.width / from.width));
      img.style.transition = `transform ${REVEAL_FLY}ms ease-in, opacity ${REVEAL_FLY}ms ease-in`;
      img.style.transform = `translate(${dx}px, ${dy}px) scale(${scale})`;
      img.style.opacity = '0.15';
    }
    setTimeout(() => { overlay.remove(); nextTableReveal(); }, REVEAL_FLY + 40);
  }, hold);
}

// renderRoomScreen picks the lobby or the board from the room the server reports.
function renderRoomScreen() {
  if (!coop.room) return;
  if (coop.room.cancelled) {
    // A closed table returns every guest to the menu. Showing the waiting-room screen
    // with a dead-room note left them stranded under the closed board (campaign B14).
    stopUpdates();
    clearRoom();
    history.replaceState(null, '', '/');
    showSetup();
    toast('The host closed the table');
    return;
  }
  // A seat the server no longer honours (taken over, or the room deleted) has to leave
  // the board — and the waiting room too: a kicked guest used to keep a stale lobby
  // with its name on a chair that was gone. This check comes before the lobby branch
  // because the stream and the poller can both find out first.
  if (coop.status === 'seat_lost' || coop.status === 'room_ended') {
    loseRoomSeat();
    return;
  }
  if (coop.lobby) {
    showLobby();
    renderLobby();
    // The header names the mode in the waiting room too: it read "solo" at a table
    // until the cards were dealt.
    setModeLabel(coop.watchId ? 'watching' : 'co-op');
    return;
  }
  if (coop.state) {
    $('#lobby').hidden = true;
    document.body.classList.add('is-coop');
    app.state = coop.state;
    showBoard();
    render();
    flushTableReveal();
  }
  // The header names the mode, so a room never looks like a solo run.
  setModeLabel(coop.watchId ? 'watching' : 'co-op');
  renderWatchNote();
  renderConnChip();
}

function renderLobby() {
  const host = $('#lobby-body');
  if (!host || !coop.room) return;
  host.replaceChildren(lobbyBody(coop.room, async (action, a, b) => {
    try {
      if (action === 'start') await startGame();
      if (action === 'watch') await setWatching(a, b);
      if (action === 'theme') await setTheme(a, app.prefs.jokers, app.prefs.jokersPicked);
      if (action === 'rules') await setRules(a);
      if (action === 'botfill') await setBotFill(a);
      if (action === 'addbot') await addBot();
      if (action === 'bot') await setBotSeat(a, b);
      if (action === 'kick') await kickSeat(a);
      // The answer to a settings call is the room's new truth, but a second tab may
      // have moved too: one read keeps every lobby in step without guessing.
      await refreshRoom();
      renderRoomScreen();
    } catch (err) {
      toast(err.message === 'not_ready' ? 'Not everyone is ready yet.' : err.message);
      // A refused switch must show the value the server still has, not the one the
      // click drew.
      try {
        await refreshRoom();
        renderRoomScreen();
      } catch {
        /* the room may be gone; the toast already said what happened */
      }
    }
  }));
  // The host can start the moment two seats are ready; a player who is not the host
  // gets the same button, and the server decides.
  if (isHost() && !coop.room.startable && coop.room.players >= 2) {
    const note = el('p', { class: 'lobby-note' }, 'Waiting for every seat to be ready.');
    $('#lobby-body').append(note);
  }
}

function showBoard() {
  $('#setup').hidden = true;
  $('#board').hidden = false;
  stopRoomListRefresh();
  $('#btn-menu').hidden = false;
  $('#btn-history').hidden = false;
}

// abandonGame ends the current run and returns to the setup screen.
async function abandonGame() {
  const id = app.sessionId;
  closeOverlay();
  app.sessionId = null;
  app.state = null;
  app.seq = 0;
  app.selected.clear();
  try {
    localStorage.removeItem(SESSION_KEY);
  } catch {
    /* nothing to clear */
  }
  if (id) {
    try {
      await api(`/api/sessions/${id}`, { method: 'DELETE' });
    } catch {
      /* the session may already be gone; local state is what matters */
    }
  }
  showSetup();
  toast('Run abandoned');
}

// ---------------------------------------------------------------- rendering

// focusKeyOf names the element that currently has focus, so a re-render (which
// rebuilds the hand and the Jester tray) can put the cursor back where it was.
function focusKeyOf(node) {
  if (!node || node === document.body) return null;
  const el2 = node.closest ? node.closest('[data-focus-key]') : null;
  return el2 ? el2.dataset.focusKey : null;
}

function restoreFocus(key) {
  if (!key) return;
  const node = document.querySelector(`[data-focus-key="${key}"]`);
  if (node && typeof node.focus === 'function') {
    node.focus({ preventScroll: true });
    return;
  }
  // The focused card left the hand (it was played). Land on the next action
  // rather than an arbitrary card: the primary button if it is live, else the
  // first card, which is where a player picks from next.
  const primary = $('#btn-confirm');
  if (primary && !primary.disabled) {
    primary.focus({ preventScroll: true });
    return;
  }
  const firstCard = $('#hand .card');
  if (firstCard) firstCard.focus({ preventScroll: true });
}

function render() {
  const s = app.state;
  if (!s) return;
  const focusKey = focusKeyOf(document.activeElement);
  renderEnemy(s);
  renderPiles(s);
  renderHand(s);
  renderActionbar(s);
  renderPeek(s);
  renderStepHint(s);
  renderSuitLegend();
  updateBubbles(s);
  renderSeats();
  renderTable(s);
  // A spectator has no turn and no hand, so the action bar has nothing to offer and
  // would only show a button that cannot be pressed.
  const bar = document.querySelector('.actionbar');
  if (bar) bar.hidden = Boolean(coop.watchId);
  if (!$('#history-drawer').hidden) renderHistory(s);
  if ((s.won || s.lost) && !$('#end-sheet')) scheduleEndSheet(s);
  // The board behind a finished game must not go on inviting a play: the banner and the
  // buttons described a live turn while the result sheet was open.
  if (s.won || s.lost) {
    document.body.classList.add('is-over');
  } else {
    document.body.classList.remove('is-over');
  }
  restoreFocus(focusKey);
}

// updateBubbles keeps a short-lived speech bubble per seat: what that player just did.
// A poll re-render must not restart the clock, so a bubble is only replaced when the
// underlying event is a new one (bubbleKey), and it expires after BUBBLE_MS.
const BUBBLE_MS = 6000;

function updateBubbles(s) {
  if (!app.bubbles) app.bubbles = new Map();
  if ((s.numPlayers || 1) < 2) {
    app.bubbles.clear();
    return;
  }
  const now = Date.now();
  const current = seatBubbles(s.log || []);
  for (const [seat, bubble] of current) {
    const existing = app.bubbles.get(seat);
    if (existing && existing.key === bubble.key) continue; // same event: keep the timer
    app.bubbles.set(seat, { ...bubble, until: now + BUBBLE_MS });
  }
  for (const [seat, bubble] of app.bubbles) {
    if (bubble.until <= now) app.bubbles.delete(seat);
  }
}

// bubbleElement renders one bubble: the words the action deserves, and the cards as real
// chips, so a suit is the drawn glyph and never a font guess.
function bubbleElement(bubble) {
  const node = el('span', { class: 'seat-bubble' });
  node.append(el('span', { class: 'bubble-text', text: bubble.text }));
  for (const id of bubble.cards.slice(0, 3)) node.append(cardChip(id));
  if (bubble.cards.length > 3) node.append(el('span', { class: 'bubble-more', text: `+${bubble.cards.length - 3}` }));
  if (bubble.detail) node.append(el('span', { class: 'bubble-detail', text: bubble.detail }));
  return node;
}

// renderSeats fills the co-op rail from the room book and the public seat views.
// In solo there is no room, so the rail stays hidden and nothing is paid for it.
function renderSeats() {
  const rail = $('#seats');
  if (!rail) return;
  if (!coop.room || !coop.state) {
    rail.hidden = true;
    rail.replaceChildren();
    return;
  }
  const bubbles = new Map();
  if (app.bubbles) {
    for (const [seat, bubble] of app.bubbles) bubbles.set(seat, bubbleElement(bubble));
  }
  rail.replaceChildren(...seatRail(coop.room, coop.state, coop.seat, bubbles, tableJokers(coop.state)).childNodes);
  // The rail's class must live on THIS element: seatRail returns a temporary <ol> whose
  // children are moved here, so a class set there dies with the temp node.
  rail.classList.toggle('has-bubbles', bubbles.size > 0);
  rail.hidden = false;
  // A spectator taps a name to look through that player's eyes (D-45).
  if (coop.watchId && coop.room.watchHands) {
    for (const chip of rail.querySelectorAll('.seat')) {
      chip.classList.add('is-tappable');
      const go = async () => {
        const seat = Number(chip.dataset.seat);
        if (!Number.isFinite(seat) || seat === coop.watchingAs) return;
        try {
          await viewAs(seat);
          renderRoomScreen();
        } catch (err) {
          toast(err.status === 403 ? 'The host turned hand-viewing off.' : 'Could not switch view.');
        }
      };
      chip.addEventListener('click', go);
      chip.addEventListener('keydown', (ev) => {
        if (ev.key === 'Enter' || ev.key === ' ') {
          ev.preventDefault();
          go();
        }
      });
    }
  }
}

// Counters use plain tabular numerals (no display font): they are read at a
// glance, and dice-style glyphs made numbers like 20 ambiguous.
function setCounter(id, value) {
  const node = document.getElementById(id);
  node.className = 'numeral';
  node.textContent = String(Number(value) || 0);
}

// renderTable draws the co-op furniture: the turn banner and the seat rail.
// Solo never reaches here (body.is-coop is only set inside a room).
function renderTable(s) {
  const coopMode = document.body.classList.contains('is-coop');
  const banner = $('#turn-banner');
  if (banner) banner.hidden = !coopMode;
  if (!coopMode) return;

  // The banner is in the acting seat's own colour — never gold, even when it is you.
  const activeSeat = coop.state && coop.state.activeSeat >= 0 ? coop.state.activeSeat : 0;
  const seats = (coop.room && coop.room.seats) || [];
  const seat = seats.find((x) => x.seat === activeSeat);
  const name = (seat && seat.name) || 'Someone';
  const mine = coop.seat === activeSeat && !coop.watchId;
  if (banner) {
    banner.style.setProperty('--seat-c', `var(--seat-${SEAT_COLOURS[activeSeat % SEAT_COLOURS.length]})`);
    // 12 royals, minus the ones still in the castle, minus the one on the table: the
    // royal being fought is not cleared yet. Without the second term turn 1 read
    // "1 of 12 royals cleared" before anyone had cleared anything.
    const onTable = s.enemy ? 1 : 0;
    const cleared = Math.max(0, 12 - (s.castleCount || 0) - onTable);
    const watching = (coop.room && coop.room.watchers) || 0;
    const hint = `turn ${s.turnNo} · ${cleared} of 12 royals cleared${watching ? ` · ${watching} watching` : ''}`;
    const waiting = coop.room && coop.room.waitingFor;
    const paying = s.phase === 'step4_pre' || s.phase === 'step4_discard';
    // A Jester in co-op is a card in hand, not the solo flip. Only name a flip or a
    // yield when the server offers one; otherwise the banner sent the player looking
    // for a button that does not exist in this mode.
    const legal = s.legal || [];
    const canFlip = legal.some((a) => a.type === 'flip_jester');
    const canYield = legal.some((a) => a.type === 'yield');
    let turn = 'Your turn — play a card';
    if (canFlip) turn += ', flip a Jester';
    if (canYield) turn += canFlip ? ', or yield' : ' or yield';
    const what = s.won
      ? 'The castle is empty — the table won'
      : s.lost
        ? `The table lost${s.lostSeat !== undefined && s.lostSeat >= 0 && (coop.room && coop.room.seats || [])[s.lostSeat] ? ` — ${coop.room.seats[s.lostSeat].name} could not pay` : ''}`
        : waiting
          ? `Waiting for ${waiting} — the table resumes when they come back`
          : (mine
            ? (paying ? 'Pay the attack — discard cards from your hand' : turn)
            : `${name} is ${paying ? 'paying' : 'playing'}`);
    banner.replaceChildren(
      el('span', { class: 'dot' }),
      el('span', { text: what }),
      el('span', { class: 'hint', text: hint }),
    );
  }
}

// boardTheme is the card art this board is drawn with. A room is dealt one theme for
// everybody (D-32), so the browser's own preference must not leak into a shared table.
function boardTheme(s) {
  const inRoom = document.body.classList.contains('is-coop');
  // A player may look at their own set at someone else's table. It changes the faces
  // in this tab only: the room keeps its set, and the wire is untouched.
  if (inRoom && app.prefs.tableDeck === 'mine') return app.prefs.theme;
  if (inRoom && s && s.theme) return s.theme;
  return app.prefs.theme;
}

// tableJokers is the pair the table is showing. A dealt game carries the pair in its
// state, so every seat, the reveal panel and the replay agree. The menu and the
// Settings preview read the browser's own pair instead.
function tableJokers(s) {
  const game = s || coop.state || app.state;
  const pair = game && Array.isArray(game.jokers) ? game.jokers : null;
  return pair && pair.length === 2 ? pair : app.prefs.jokers;
}

// pushLobbyJokers sends a hand pick (or a cleared pick) to a lobby this tab hosts,
// so a table setting made in Settings reaches the deal without another round trip.
function pushLobbyJokers() {
  if (!coop.code || !coop.room || !coop.room.lobby) return;
  if (coop.room.hostSeat !== coop.seat) return;
  const pick = [...app.prefs.jokers];
  setTheme(coop.room.theme || app.prefs.theme, pick, app.prefs.jokersPicked).catch(() => {});
}

function renderEnemy(s) {
  const slot = $('#enemy-card');
  slot.replaceChildren();
  if (s.enemy) {
    const card = cardElement(s.enemy, { theme: boardTheme(s), state: 'enemy' });
    card.tabIndex = -1;
    card.setAttribute('aria-hidden', 'false');
    // The royal is a picture. Without a name the board is silent about which royal is on
    // the table for anyone reading the DOM rather than looking at it: a screen reader,
    // and every agent that plays through the DOM.
    const hp = typeof s.enemyHealth === 'number' ? `${Math.max(0, s.enemyHealth - (s.damage || 0))} of ${s.enemyHealth} health` : '';
    const atk = typeof s.enemyAttack === 'number' ? `${s.enemyAttack} attack` : '';
    card.setAttribute('role', 'img');
    card.setAttribute('aria-label', `${s.enemy.label || s.enemy.id} on the table, ${[hp, atk].filter(Boolean).join(', ')}`);
    // The royal carries the rules text a player needs to plan: open it on any width.
    // It was wired only below 560px, so a desktop player had no way to read it.
    slot.classList.add('is-inspectable');
    slot.onclick = () => openCardDetail(s.enemy);
    slot.onkeydown = (ev) => {
      if (ev.key === 'Enter' || ev.key === ' ') {
        ev.preventDefault();
        openCardDetail(s.enemy);
      }
    };
    slot.tabIndex = 0;
    slot.setAttribute('role', 'button');
    slot.setAttribute('aria-label', `Inspect ${s.enemy.label || s.enemy.id}`);
    slot.append(card);
  } else {
    const empty = document.createElement('div');
    empty.className = 'pile';
    empty.textContent = 'castle empty';
    slot.append(empty);
  }
  $('#castle-count').textContent = `castle ${s.castleCount}`;
  const phase = $('#phase-badge');
  if (phase) {
    const paying = String(s.phase || '').startsWith('step4');
    phase.hidden = Boolean(s.won || s.lost || !s.enemy);
    phase.textContent = paying ? '4 · pay' : '1 · play';
    phase.classList.toggle('badge-pay', paying);
  }
  $('#turn-count').textContent = `turn ${s.turnNo}`;
  // Both boxes count DOWN from the enemy's own numbers: health left out of its
  // total, and the attack still coming out of the attack it started with.
  const health = s.enemyHealth || 0;
  const damage = Math.min(health, s.damage || 0);
  const left = Math.max(0, health - damage);
  const baseAttack = (s.enemy && s.enemy.attack) || 0;
  const landing = Math.max(0, Math.min(baseAttack, s.incoming ?? 0));
  setCounter('enemy-health', left);
  setCounter('enemy-health-total', health);
  setCounter('enemy-incoming', landing);
  setCounter('enemy-attack-total', baseAttack);
  // The bar shows what is LEFT: a green bar that grows as the enemy takes damage
  // reads as "nearly dead" at full health and as "healthy" at zero.
  const bar = $('#hp-bar');
  bar.style.width = `${health ? Math.min(100, Math.round((left / health) * 100)) : 0}%`;
  $('#enemy-health').setAttribute('aria-label', `enemy health ${left} of ${health}`);
  $('#enemy-incoming').setAttribute('aria-label', `enemy attack landing ${landing} of ${baseAttack}`);
  const shieldNote = $('#enemy-shield-note');
  if (shieldNote) {
    const shielded = Math.max(0, baseAttack - landing);
    shieldNote.hidden = shielded === 0;
    shieldNote.textContent = shielded > 0 ? `♠ ${shielded} shielded` : '';
  }

  const chaosBadge = $('#chaos-badge');
  if (chaosBadge) chaosBadge.hidden = !(s.rules && s.rules.chaos);

  const suit = s.enemy && s.enemy.suit && s.enemy.suit !== 'none' ? s.enemy.suit : null;
  const immune = Boolean(suit) && !s.immunityCancelled;
  const badge = $('#immunity-badge');
  if (badge) {
    badge.classList.toggle('is-broken', Boolean(suit) && Boolean(s.immunityCancelled));
    badge.hidden = !suit;
    if (immune) {
      badge.textContent = `${suit} immune`;
      badge.title = `${suit} suit powers are blocked; damage still counts. A Jester breaks it.`;
    } else if (suit && s.immunityCancelled) {
      badge.textContent = 'immunity broken';
      badge.title = `${suit} suit powers are live again.`;
    }
  }
}

function renderPiles(s) {
  const theme = boardTheme(s);
  const back = app.prefs.back;
  setChip('tavern-count', s.tavernCount);
  setChip('discard-count', s.discardCount);

  // Tavern: a visible stack of backs, count on the chip. The top card stays
  // hidden — you draw blind (docs/rules-coverage.md, step 2).
  const tavern = $('#tavern-art');
  tavern.replaceChildren();
  if (s.tavernCount > 0) {
    tavern.append(backCard(back, { size: 'pile-back', label: `Tavern deck, ${s.tavernCount} cards` }));
    // A second back was drawn to suggest a stack. At the co-op pile sizes it landed on
    // top of the "tavern" label, and the count chip already carries the number.
    if (s.tavernCount > 1 && !document.body.classList.contains('is-coop')) {
      tavern.append(backCard(back, { size: 'pile-back', label: 'Tavern deck' }));
    }
  } else {
    tavern.append(el('span', { class: 'pile-empty', 'aria-label': 'Tavern deck empty' }));
  }

  // Discard: the newest cards face up, so you can see what a hearts play would
  // shuffle back in.
  const discard = $('#discard-art');
  discard.replaceChildren();
  discard.onclick = null;
  const top = (s.discardTop || []).slice(-3);
  if (top.length === 0) {
    discard.append(el('span', { class: 'pile-empty', 'aria-label': 'Discard pile empty' }));
  }
  for (const c of top) {
    const card = cardElement(c, { theme, size: 'pile-card', state: 'is-static', jokers: tableJokers(s) });
    card.tabIndex = -1;
    card.setAttribute('aria-hidden', 'true');
    discard.append(card);
  }
  // The pile is a button: hover shows a hint, click/tap opens the cards large
  // enough to read (with the authored text for sets that print it).
  const pile = $('#pile-discard');
  const openable = s.discardCount > 0;
  const open = () => {
    if (document.body.classList.contains('is-coop')) {
      const all = discardPile(s);
      if (all.length > 0) {
        openCardList(
          `Discard pile — ${all.length} card${all.length === 1 ? '' : 's'}`,
          'Every card in the pile, oldest first. Public information in Regicide.',
          all,
          false,
        );
        return;
      }
    }
    openCardZoom(top, {
      title: `Discard pile — ${s.discardCount} card${s.discardCount === 1 ? '' : 's'} (newest first)`,
    });
  };
  pile.classList.toggle('is-clickable', openable);
  pile.title = openable ? 'Open the discard pile' : 'Discard pile is empty';
  if (openable) {
    pile.setAttribute('role', 'button');
    pile.tabIndex = 0;
    pile.setAttribute('aria-label', `Open the discard pile, ${s.discardCount} cards`);
    pile.onclick = open;
    pile.onkeydown = (ev) => {
      if (ev.key === 'Enter' || ev.key === ' ') {
        ev.preventDefault();
        open();
      }
    };
  } else {
    pile.removeAttribute('role');
    pile.removeAttribute('tabindex');
    pile.removeAttribute('aria-label');
    pile.onclick = null;
    pile.onkeydown = null;
  }


  const attack = $('#attack-cards');
  attack.replaceChildren();
  const attackPileEl = $('#pile-attack');
  if (attackPileEl) {
    if (document.body.classList.contains('is-coop')) {
      attackPileEl.classList.add('is-openable');
      attackPileEl.onclick = () => {
        const list = playedThisEnemy(s);
        if (list.length) openCardList(`Played against ${s.enemy ? s.enemy.label : 'this royal'}`, 'Newest last. The border is the player.', list, true);
      };
    } else {
      attackPileEl.classList.remove('is-openable');
      attackPileEl.onclick = null;
    }
  }
  const allPlayed = s.attackArea || [];
  const cap = document.body.classList.contains('is-coop') ? 5 : 8;
  const played = allPlayed.slice(-cap);
  const hiddenPlayed = allPlayed.length - played.length;
  // An empty "played" box is a label with nothing under it, so it only exists while
  // cards are actually there.
  const attackPile = $('#pile-attack');
  if (attackPile) attackPile.hidden = played.length === 0;
  // Who played each card is in the log, not in the pile: attributeSeats carries the
  // acting seat forward, so a card that is still on the table can be tagged.
  const owner = cardSeatMap(s.log || []);
  for (const c of played) {
    const seat = owner.get(c.id);
    const node = cardElement(c, { theme, size: 'pile-card', state: 'is-static', jokers: tableJokers(s) });
    node.classList.add(document.body.classList.contains('is-coop') ? 'played-card' : 'mini');
    node.title = c.label;
    node.tabIndex = -1;
    node.setAttribute('aria-hidden', 'true');
    if (seat !== undefined) {
      node.style.setProperty('--seat-c', `var(--seat-${SEAT_COLOURS[seat % SEAT_COLOURS.length]})`);
      node.classList.add('has-owner');
    }
    attack.append(node);
  }
  if (hiddenPlayed > 0) {
    attack.append(el('button', {
      type: 'button',
      class: 'played-more',
      text: `+${hiddenPlayed} more`,
      'aria-label': `See all ${allPlayed.length} cards played against this royal`,
      onclick: () => openCardList(
        `Played against ${s.enemy ? s.enemy.label : 'this royal'}`,
        'Newest last. The border is the player.',
        playedThisEnemy(s),
        true,
      ),
    }));
  }

  renderJesters(s);
}

// cardSeatMap maps a card id to the seat that played it, from the log. Only plays
// count: a discarded card went to the pile, not to the table.
function cardSeatMap(log) {
  // attributeSeats returns one seat (or null) per log entry, carried forward from the
  // last event that named a seat.
  const seats = attributeSeats(log);
  const map = new Map();
  (log || []).forEach((ev, i) => {
    if (!ev || ev.type !== 'play' || !Array.isArray(ev.cards)) return;
    if (typeof seats[i] !== 'number') return;
    for (const id of ev.cards) map.set(id, seats[i]);
  });
  return map;
}

// setOfCards renders a pile of cards large enough to read, in the overlay the app
// already uses for sheets.
function openCardList(title, note, cards, withOwner) {
  overlay('card-list', (sheet) => {
    const frag = document.createDocumentFragment();
    frag.append(el('h2', { text: title }));
    if (note) frag.append(el('p', { class: 'hint', text: note }));
    const grid = el('div', { class: 'card-grid' });
    for (const entry of cards) {
      const wrap = el('div', { class: 'card-grid-item' });
      const card = cardElement(entry.card, { theme: boardTheme(app.state), size: 'grid-size', jokers: tableJokers(app.state) });
      card.tabIndex = -1;
      if (entry.seat !== undefined) {
        wrap.style.setProperty('--seat-c', `var(--seat-${SEAT_COLOURS[entry.seat % SEAT_COLOURS.length]})`);
        wrap.classList.add('has-owner');
        const who = (coop.room && coop.room.seats || []).find((x) => x.seat === entry.seat);
        wrap.append(el('span', { class: 'card-grid-who', text: (who && who.name) || `seat ${entry.seat}` }));
      }
      wrap.prepend(card);
      grid.append(wrap);
    }
    if (!cards.length) frag.append(el('p', { class: 'hint', text: 'Nothing here yet.' }));
    frag.append(grid);
    frag.append(el('button', { type: 'button', class: 'ghost', text: 'Close', onclick: closeOverlay }));
    return frag;
  });
}

// playedThisEnemy lists every card the table has played against the current royal, with
// the seat that played it: the log is the only place this history lives.
function playedThisEnemy(s) {
  const seats = attributeSeats(s.log || []);
  const out = [];
  for (let i = 0; i < (s.log || []).length; i += 1) {
    const ev = s.log[i];
    if (!ev || ev.type !== 'play' || !Array.isArray(ev.cards)) continue;
    for (const id of ev.cards) {
      const card = (s.attackArea || []).find((c) => c.id === id) || cardById(id);
      if (card) out.push({ card, seat: typeof seats[i] === 'number' ? seats[i] : undefined });
    }
  }
  return out;
}

// discardPile lists every card that has gone to the discard pile: the log records each
// paid card, and the pile is public information in Regicide.
function discardPile(s) {
  // The snapshot carries the whole pile: it is public, and the log cannot rebuild it
  // (hearts move cards out; a defeat drops the attack area in). The log remains a
  // fallback for an older server.
  if (Array.isArray(s.discard)) return s.discard.map((card) => ({ card }));
  const out = [];
  for (const ev of s.log || []) {
    if (!ev || ev.type !== 'suffer' || !Array.isArray(ev.cards)) continue;
    for (const id of ev.cards) {
      const card = cardById(id);
      if (card) out.push({ card });
    }
  }
  return out;
}

// cardById is the fallback when a card has left the table: the pile only carries ids.
function cardById(id) {
  const info = textFor(id);
  const label = (info && info.title) || id;
  // The two Jesters are j1/j2: no rank and no suit. The old regex demanded a suit, so
  // every Jester that reached the discard pile vanished from the card list (playtest 3
  // campaign, A12: a 10-card pile drew 8 faces).
  if (/^j[12]$/i.test(String(id))) {
    return { id, rank: 15, suit: '', label, value: 0, discardValue: 0, joker: true };
  }
  const m = /^([0-9]{1,2}|[ajqk])([shdc])$/i.exec(String(id));
  if (!m) return null;
  const rankMap = { a: 1, j: 11, q: 12, k: 13 };
  const suitMap = { s: 'spades', h: 'hearts', d: 'diamonds', c: 'clubs' };
  const raw = m[1].toLowerCase();
  const rank = raw.startsWith('j') ? 15 : (rankMap[raw] || Number(raw));
  return { id, rank, suit: suitMap[m[2].toLowerCase()] || 'spades', label, value: rank > 10 && rank < 15 ? 10 : rank, discardValue: rank > 10 && rank < 15 ? 10 : rank };
}

// renderJesters shows the two Jester cards themselves. A Jester that has been
// flipped turns face down (the card is spent): the tray is the state, not a count.
function renderJesters(s) {
  const tray = $('#jester-tray');
  tray.replaceChildren();
  const enabled = !s.rules || s.rules.soloJester !== false;
  const pile = $('#pile-jesters');
  if (pile) pile.hidden = !enabled;
  if (!enabled) return;
  const used = s.jestersUsed || 0;
  const theme = boardTheme(s);
  const art = jesterArt(theme, tableJokers(s));
  const canFlip = (s.legal || []).some((a) => a.type === 'flip_jester');
  for (let i = 0; i < 2; i += 1) {
    const spent = i < used;
    if (spent) {
      const card = backCard(app.prefs.back, { size: 'jester-card', state: 'is-spent', label: `Jester ${i + 1}, spent` });
      card.classList.add('is-spent');
      tray.append(card);
      continue;
    }
    const node = document.createElement(canFlip ? 'button' : 'div');
    node.dataset.focusKey = `jester:${i + 1}`;
    if (canFlip) {
      node.type = 'button';
      // Tapping a Jester opens its card first: flipping discards the whole hand,
      // so it needs an explicit confirmation.
      node.addEventListener('click', () => confirmJesterFlip());
      node.addEventListener('pointerenter', () => showTip({ id: 'J1', value: 0 }, node));
      node.addEventListener('pointerleave', hideTip);
      node.addEventListener('focus', () => showTip({ id: 'J1', value: 0 }, node));
      node.addEventListener('blur', hideTip);
    }
    node.className = ['card', 'jester-card', 'is-available', canFlip ? 'is-actionable' : ''].filter(Boolean).join(' ');
    node.setAttribute('aria-label', `Jester ${i + 1}: flip to discard your hand and refill to ${s.maxHand}`);
    const img = document.createElement('img');
    img.className = 'card-img';
    img.alt = '';
    img.decoding = 'async';
    img.loading = 'lazy';
    img.src = art[i];
    node.append(img);
    // Every Jester face prints its own lettering, so no rule text goes over
    // it: the Jester's rules live on the confirm sheet and in the tooltip.
    tray.append(node);
  }
}

// setChip writes a count onto its poker chip.
function setChip(id, value) {
  const node = document.getElementById(id);
  if (!node) return;
  node.textContent = String(Number(value) || 0);
}

function renderHand(s) {
  const hand = $('#hand');
  hand.replaceChildren();
  const cards = s.hand || [];
  const n = cards.length;
  // The royal blocks its own suit's power until a Jester cancels the immunity. A card
  // of that suit still deals damage, so the mark says "power blocked", not "unusable".
  const blockedSuit = s.enemy && s.enemy.suit && s.enemy.suit !== 'none' && !s.immunityCancelled
    ? s.enemy.suit : null;
  cards.forEach((c, i) => {
    const selected = app.selected.has(c.id);
    const el = cardElement(c, { theme: boardTheme(s), selected, jokers: tableJokers(s) });
    if (blockedSuit && c.suit === blockedSuit) {
      el.classList.add('is-power-blocked');
      el.title = `${blockedSuit} power is blocked by this royal`;
      el.setAttribute('aria-label', `${el.getAttribute('aria-label') || c.id} — ${blockedSuit} power blocked`);
      const mark = document.createElement('span');
      mark.className = 'power-blocked';
      mark.setAttribute('aria-hidden', 'true');
      mark.textContent = '⊘';
      el.append(mark);
    }
    el.dataset.focusKey = `card:${c.id}`;
    el.style.setProperty('--i', String(i));
    el.style.setProperty('--n', String(n));
    const spread = n > 1 ? Math.min(8, 2 * (n - 1)) : 0;
    el.style.setProperty('--rot', `${n > 1 ? ((i - (n - 1) / 2) / ((n - 1) / 2)) * (spread / 2) : 0}deg`);
    el.addEventListener('click', () => toggleSelection(c));
    el.addEventListener('contextmenu', (ev) => {
      ev.preventDefault();
      openCardDetail(c);
    });
    attachLongPress(el, () => openCardDetail(c));
    // The helper is a pointer affordance. On a touch screen a tap fires pointerenter
    // (then never pointerleave), so the tooltip stuck around over the cards.
    if (window.matchMedia('(hover: hover) and (pointer: fine)').matches) {
      el.addEventListener('pointerenter', () => showTip(c, el));
    }
    el.setAttribute('aria-describedby', 'card-tip');
    el.addEventListener('pointerleave', hideTip);
    el.addEventListener('focus', () => showTip(c, el));
    el.addEventListener('blur', hideTip);
    hand.append(el);
  });
  if (n === 0) {
    const empty = document.createElement('p');
    empty.className = 'empty';
    empty.textContent = 'No cards in hand.';
    hand.append(empty);
  }
}

// renderSeatChoice draws the Jester's "who goes next?" question. A co-op Jester hands
// the turn to a seat the player picks, and for a long time the engine offered only
// that action while the client had no button for it: the chooser saw a dead action
// bar and every other seat was blocked by the turn gate (F-50).
function renderSeatChoice(s) {
  const bar = $('#actionbar');
  const existing = $('#seat-choice');
  const choice = s && s.pendingSeatChoice;
  const mine = choice && coop.seat === choice.by && !coop.watchId;
  if (!bar || !choice || !mine) {
    if (existing) existing.remove();
    return;
  }
  const names = new Map(((coop.room && coop.room.seats) || []).map((x) => [x.seat, x.name]));
  const wrap = existing || el('div', { class: 'seat-choice', id: 'seat-choice' });
  wrap.replaceChildren(el('span', { class: 'seat-choice-label', text: 'The Jester asks: who goes next?' }));
  for (const seat of choice.next || []) {
    wrap.append(el('button', {
      type: 'button',
      class: `ghost seat-choice-btn seat-${SEAT_COLOURS[seat % SEAT_COLOURS.length]}`,
      'data-seat': String(seat),
      text: names.get(seat) || `Seat ${seat + 1}`,
      onclick: () => act({ type: 'choose_next_seat', seat }),
    }));
  }
  if (!existing) bar.append(wrap);
}

// itIsMyTurn says whether this tab may act: the engine's active seat, or this seat's
// own pending Jester choice. Solo has no seat and is always its own turn.
function itIsMyTurn(s) {
  if (coop.watchId) return false;
  if (coop.seat < 0) return true;
  // A finished table has no turn to wait for: the bar behind the result sheet must not
  // claim somebody else is playing.
  if (s && (s.won || s.lost)) return true;
  if (s && s.pendingSeatChoice && s.pendingSeatChoice.by === coop.seat) return true;
  return Boolean(s) && s.activeSeat === coop.seat;
}

// waitingForName is the seat that holds the turn, for the action bar sentence.
function waitingForName(s) {
  const activeSeat = s && typeof s.activeSeat === 'number' ? s.activeSeat : -1;
  const seats = (coop.room && coop.room.seats) || [];
  const seat = seats.find((x) => x.seat === activeSeat);
  return (seat && seat.name) || 'the other seat';
}

function renderActionbar(s) {
  const confirm = $('#btn-confirm');
  const jester = $('#btn-jester');
  const yieldBtn = $('#btn-yield');
  const deselect = $('#btn-deselect');
  renderSeatChoice(s);

  // A seat that does not hold the turn may read and select its cards, but it can
  // never play one: the server would refuse, and a live button that refuses is worse
  // than a sentence that explains (notes 4 and 5). The combo error used to appear here
  // because the rules were consulted on somebody else's turn.
  if (document.body.classList.contains('is-coop') && !itIsMyTurn(s)) {
    const choice = s && s.pendingSeatChoice;
    confirm.textContent = choice
      ? `${waitingForName(s)} is choosing who goes next`
      : `Waiting for ${waitingForName(s)}`;
    confirm.disabled = true;
    jester.hidden = true;
    yieldBtn.hidden = true;
    deselect.disabled = app.selected.size === 0;
    deselect.textContent = app.selected.size > 0 ? `Deselect (${app.selected.size})` : 'Deselect';
    return;
  }

  const legal = s.legal || [];
  const playMatch = matchPlay(s, legal);
  const payMatch = matchPay(s, legal);
  const canFlip = legal.some((a) => a.type === 'flip_jester');
  const canYield = legal.some((a) => a.type === 'yield');
  const canEnd = legal.some((a) => a.type === 'end_payment');

  jester.hidden = !canFlip;
  yieldBtn.hidden = !canYield;

  if (s.phase === 'step4_pre' || s.phase === 'step4_discard') {
    const left = paymentLeft(s);
    const suffix = app.prefs.hints && left > 0 ? ` · ${left} damage left` : '';
    if (canEnd && app.selected.size === 0) {
      confirm.textContent = left > 0 ? `Done paying · ${left} damage left` : 'Done paying';
      confirm.disabled = false;
    } else if (payMatch) {
      // The same countdown, on the button that commits the payment: it shows the
      // damage still owed AFTER these cards, not the current total.
      const values = payCards(s, payMatch.cardIds).map((c) => discardValueOf(c));
      const after = paySelectionAfter(left, values);
      if (app.prefs.hints) {
        confirm.replaceChildren(document.createTextNode(payMatch.cardIds.length === 1 ? 'Discard ' : `Discard ${payMatch.cardIds.length} cards: `));
        for (const id of payMatch.cardIds.slice(0, 3)) confirm.append(cardChip(id), document.createTextNode(' '));
        if (payMatch.cardIds.length > 3) confirm.append(document.createTextNode(`+${payMatch.cardIds.length - 3} `));
        confirm.append(document.createTextNode(after > 0 ? `· ${after} damage left` : '· covered'));
      } else {
        confirm.textContent = 'Discard selected';
      }
      confirm.disabled = false;
    } else if (canEnd) {
      // No legal discard matches the selection, so the click ends payment: say so
      // rather than promising a discard it will not perform.
      confirm.textContent = left > 0 ? `Done paying · ${left} damage left` : 'Done paying';
      confirm.disabled = false;
    } else {
      // It used to say "Discard 1 card" no matter how much was owed, which read as a
      // promise of a one-card payment when two were needed.
      confirm.textContent = left > 0 ? `Select cards to pay ${left}` : 'Select cards to discard';
      confirm.disabled = true;
    }
  } else {
    if (playMatch) {
      confirm.textContent = describePlay(playMatch, s);
      confirm.disabled = false;
    } else if (app.selected.size > 0) {
      // Do not refuse silently: a legal-looking selection that the rules cannot play
      // is the most common dead end in a game about combinations.
      confirm.textContent = whyNotPlayable(s, [...app.selected]);
      confirm.disabled = true;
    } else {
      confirm.textContent = 'Play';
      confirm.disabled = true;
    }
  }
  deselect.disabled = app.selected.size === 0;
  deselect.textContent = app.selected.size > 0 ? `Deselect (${app.selected.size})` : 'Deselect';
  void canYield;
}

function describePlay(play, s) {
  const ids = play.cardIds;
  // The server already resolved suit powers into the attack, so the button never
  // re-derives a rule (docs/PLAN.md: the client does not decide rules).
  const damage = typeof play.attack === 'number' ? play.attack : null;
  let total = 0;
  for (const id of ids) {
    const card = (s.hand || []).find((c) => c.id === id);
    if (card) total += card.value;
  }
  const n = ids.length === 1 ? 'Play 1 card' : `Play ${ids.length} cards`;
  if (!app.prefs.hints) return n;
  if (damage === null) return `${n} · ${total} damage`;
  // Clubs double the whole play: show the working when the server's damage is not
  // simply the card total.
  if (damage !== total) return `${n} · ${total} ×2 = ${damage} damage`;
  return `${n} · ${damage} damage`;
}

// renderPeek shows what is currently selected at a readable size on a phone: one
// tap selects a card and its text appears above the Play button, so reading the
// card never costs an extra gesture. Tapping a peek card deselects it.
function renderPeek(s) {
  const host = $('#peek');
  if (!host) return;
  const wasHidden = host.hidden;
  const ids = [...app.selected];
  const narrow = window.matchMedia('(max-width: 559px)').matches;
  const cards = ids.map((id) => (s.hand || []).find((c) => c.id === id)).filter(Boolean);
  if (!narrow || cards.length === 0) {
    host.hidden = true;
    host.replaceChildren();
    return;
  }
  host.hidden = false;
  host.replaceChildren();
  for (const c of cards) {
    // Removing from the peek must always deselect, even in step 4 where a plain
    // tap on a card means "this is the card I will forfeit".
    const forfeit = String(s.phase || '').startsWith('step4');
    const item = cardFace(c, {
      forceCaption: true,
      selected: true,
      label: forfeit ? 'Cancel forfeit' : 'Remove from selection',
      onPick: () => deselectCard(c.id),
    });
    item.classList.add('peek-card');
    host.append(item);
  }
  // A card selected from the second row of the hand can sit behind the pinned
  // action bar: bring the strip into view so the choice is visible where it lands.
  if (wasHidden && !host.hidden && typeof host.scrollIntoView === 'function') {
    host.scrollIntoView({ block: 'nearest' });
  }
}

// renderSuitLegend fills the always-visible reminder of the four suit powers, with
// drawn glyphs (a device without ♠♥♦♣ in its fonts would otherwise lose them).
function renderSuitLegend() {
  const host = $('#suit-legend');
  if (!host || host.childElementCount > 0) return;
  host.hidden = false;
  for (const [suit, power] of SUIT_POWERS) {
    const item = el('span');
    item.append(suitIcon(suit, { className: `suit-icon suit-${suit}` }));
    item.append(document.createTextNode(` ${power}`));
    host.append(item);
  }
}

function renderStepHint(s) {
  const hint = $('#step-hint');
  // `legal` was read below and never defined, so selecting a card the rules refuse
  // threw a ReferenceError inside render and left the board half-drawn.
  const legal = s.legal || [];
  if (s.won) hint.textContent = 'No royals left standing.';
  else if (s.lost) hint.textContent = defeatLine(s);
  else if (s.phase === 'step4_pre' || s.phase === 'step4_discard') {
    // Payment counts DOWN. What the player needs is "how much is still owed and
    // what does this card leave me", not a growing total.
    const left = paymentLeft(s);
    const handTotal = (s.hand || []).reduce((n, c) => n + discardValueOf(c), 0);
    const picked = [...app.selected].map((id) => (s.hand || []).find((c) => c.id === id)).filter(Boolean);
    if (left === 0) {
      hint.textContent = 'Attack paid in full — press Done paying.';
    } else if (picked.length > 0) {
      const pays = picked.reduce((n, c) => n + discardValueOf(c), 0);
      const after = Math.max(0, left - pays);
      hint.replaceChildren(document.createTextNode(picked.length === 1 ? 'Discard ' : `Discard ${picked.length} cards: `));
      for (const c of picked.slice(0, 3)) hint.append(cardChip(c.id), document.createTextNode(' '));
      if (picked.length > 3) hint.append(document.createTextNode(`+${picked.length - 3} `));
      hint.append(document.createTextNode(`pays ${pays} — ${after === 0 ? 'covered' : `${after} left`}.`));
    } else if (handTotal < left) {
      hint.textContent = `Pay ${left} damage — your hand covers only ${handTotal}. Flip a Jester or the run ends.`;
    } else {
      hint.textContent = `Pay ${left} damage to survive.`;
    }
  } else if (s.enemy) {
    // The hint carries the pre-commit maths: what the selection would do, and the
    // one rule that silently zeroes damage (an enemy is immune to its own suit).
    const left = Math.max(0, s.enemyHealth - s.damage);
    const picked = [...app.selected].map((id) => (s.hand || []).find((c) => c.id === id)).filter(Boolean);
    const immune = s.enemy && s.enemy.suit && !s.immunityCancelled ? s.enemy.suit : null;
    // Suit names carry a drawn glyph: a font without ♠♥♦♣ would print "A  is : ...".
    const suitLine = (prefix, suit, suffix) => {
      const frag = document.createDocumentFragment();
      if (prefix) frag.append(document.createTextNode(prefix));
      frag.append(suitIcon(suit, { className: `suit-icon suit-${suit}` }));
      frag.append(document.createTextNode(suffix));
      return frag;
    };
    if (picked.length === 1 && immune && picked[0].suit === immune) {
      hint.replaceChildren(cardChip(picked[0].id), suitLine(' is ', immune, `: it deals 0 to this enemy. Deal ${left}.`));
    } else if (picked.length > 1 && immune && picked.some((c) => c.suit === immune)) {
      hint.replaceChildren(suitLine('Careful: ', immune, ` cards hit for 0. Deal ${left} to defeat it.`));
    } else if (picked.length > 0 && !matchPlay(s, legal) && immune && picked.some((c) => c.suit === immune)) {
      hint.replaceChildren(suitLine('', immune, ` cards deal nothing here. Deal ${left} to defeat it.`));
    } else {
      hint.textContent = `Deal ${left} to defeat it.`;
    }
  } else hint.textContent = '';
}

// ---------------------------------------------------------------- selection

function deselectCard(id) {
  app.selected.delete(id);
  render();
  // The peek node that had focus is gone; keep the player in the hand.
  const handCard = document.querySelector(`#hand .card[data-focus-key="card:${id}"]`);
  if (handCard) handCard.focus({ preventScroll: true });
}

function toggleSelection(card) {
  const s = app.state;
  if (!s || s.won || s.lost) return;
  const inStep4 = s.phase === 'step4_pre' || s.phase === 'step4_discard';
  const atTable = document.body.classList.contains('is-coop');
  if (inStep4 && atTable && !itIsMyTurn(s)) {
    // Reading the hand while somebody else pays is allowed and must stay quiet: the
    // payment rules belong to the seat that owes the damage, so a batch is not
    // validated (and never refused) for a seat that is only looking.
    if (app.selected.has(card.id)) app.selected.delete(card.id);
    else app.selected.add(card.id);
    render();
    return;
  }
  if (inStep4) {
    // Payment takes any number of cards: tap to add, tap again to take one back.
    // Tapping a card that would push the batch past the total is refused (the
    // engine rejects it too), so the selection always stays payable.
    if (app.selected.has(card.id)) {
      app.selected.delete(card.id);
    } else {
      const next = [...app.selected, card.id];
      if (payBatchAllowed(s, next)) app.selected.add(card.id);
      else toast(payBatchRefusal(s, next));
    }
    render();
    return;
  }
  if (app.selected.has(card.id)) app.selected.delete(card.id);
  else app.selected.add(card.id);
  render();
}

function matchPlay(s, legal) {
  const selected = [...app.selected].sort();
  if (selected.length === 0) return null;
  for (const a of legal) {
    if (a.type !== 'play') continue;
    const ids = [...a.cardIds].sort();
    if (ids.length === selected.length && ids.every((id, i) => id === selected[i])) return a;
  }
  return null;
}

// payCards turns the current selection into the cards a payment would discard, in
// hand order, so the server receives a stable batch.
function payCards(s, ids = [...app.selected]) {
  const hand = s.hand || [];
  return ids.map((id) => hand.find((c) => c.id === id)).filter(Boolean);
}

// payBatchAllowed answers "would the engine accept these cards as one payment?".
function payBatchAllowed(s, ids) {
  if (!ids || ids.length === 0) return false;
  const cards = payCards(s, ids);
  if (cards.length !== ids.length) return false; // a card left the hand
  return paySelectionValid(
    paymentLeft(s),
    cards.map((c) => discardValueOf(c)),
    Boolean(s.rules && s.rules.allowOverpayDiscard),
  );
}

// payBatchRefusal explains why a tap was ignored, in the same words as the hint.
function payBatchRefusal(s, ids) {
  const cards = payCards(s, ids);
  let paid = 0;
  for (let i = 0; i < cards.length - 1; i += 1) paid += discardValueOf(cards[i]);
  if (paid >= paymentLeft(s)) return 'That already covers the attack — confirm it instead.';
  return 'That card is not available.';
}

// matchPay returns the batch the player has staged, or null.
function matchPay(s, legal) {
  if (app.selected.size === 0) return null;
  const ids = [...app.selected];
  // The server lists single-card payments; a batch is legal when the engine's rule
  // says so and every card is one the server is offering right now.
  const offered = new Set(
    (legal || []).filter((a) => a.type === 'pay_damage').flatMap((a) => a.cardIds || []),
  );
  if (!ids.every((id) => offered.has(id))) return null;
  if (!payBatchAllowed(s, ids)) return null;
  const cards = payCards(s, ids);
  if (cards.length !== ids.length) return null;
  return { cardIds: cards.map((c) => c.id) };
}

async function confirmSelection() {
  const s = app.state;
  if (!s) return;
  const legal = s.legal || [];
  const play = matchPlay(s, legal);
  if (play) {
    app.audio.play('play');
    await act({ type: 'play', cardIds: play.cardIds });
    return;
  }
  const pay = matchPay(s, legal);
  if (pay) {
    app.audio.play('discard');
    app.selected.clear();
    await act({ type: 'pay_damage', cardIds: pay.cardIds });
    return;
  }
  if (legal.some((a) => a.type === 'end_payment')) {
    await act({ type: 'end_payment' });
  }
}

// ---------------------------------------------------------------- events

const SOUND_FOR_EVENT = {
  play: 'play',
  draw: 'draw',
  hearts: 'heal',
  damage: 'damage',
  shield: 'shield',
  defeat: 'defeat',
  recruit: 'reveal',
  suffer: 'discard',
  enemy_attack: 'enemy_attack',
  jester_flip: 'jester',
  turn_end: 'turn',
  yield: 'ui',
  win: 'victory',
  tier: 'tier',
  lose: 'defeat',
  enemy_reveal: 'reveal',
  setup: 'deal',
};

function animateEvents(events) {
  if (!events || events.length === 0) return;
  // Solo has no table to inform, but a royal falling or arriving is worth a panel.
  // Co-op reaches the same panels through coop.pendingReveal, which carries the fresh
  // suffix of a sequence frame; queueing here too would show every panel twice.
  if (!coop.code) {
    for (const item of revealsFor(events, attributeSeats(events))) {
      if (item.royal) queueTableReveal(item);
    }
  }
  const off = app.prefs.animations === 'off';
  for (const ev of events) {
    const sound = SOUND_FOR_EVENT[ev.type];
    if (sound) app.audio.play(sound);
    if (ev.type === 'play' && ev.cards && ev.cards.length > 1) app.audio.play('combo');
    if (off) continue;
    const label = eventLabel(ev);
    if (label) toast(label);
  }
}

function eventLabel(ev) {
  switch (ev.type) {
    case 'damage':
      return `${ev.amount} damage`;
    case 'draw':
      return `drew ${ev.amount}`;
    case 'hearts':
      return `healed ${ev.amount}`;
    case 'shield':
      return `shield ${ev.amount}`;
    case 'defeat':
      return `defeated ${ev.cards ? ev.cards[0] : ''}`.trim().concat(ev.note === 'exact' ? ' — exact!' : '');
    case 'recruit':
      return 'enemy joins your deck';
    case 'suffer':
      return `forfeited ${ev.amount || 0}`;
    case 'enemy_attack':
      return ev.amount ? `enemy hit for ${ev.amount}` : 'no damage';
    case 'jester_flip':
      return `jester flipped — drew ${ev.amount}`;
    case 'win':
      return 'victory';
    case 'lose':
      return 'defeat';
    default:
      return '';
  }
}

let toastTimer = null;
function toast(message) {
  const strip = $('#log-strip');
  const el = document.createElement('span');
  el.textContent = message;
  strip.append(el);
  setTimeout(() => el.remove(), 2600);
  clearTimeout(toastTimer);
}

// appendTurnActor writes "· Name" for a turn header, and says so when that seat is
// leading again because it took the last royal (R-034/R-035).
function appendTurnActor(header, s, actor, leadAgainIfSeat) {
  const meta = seatMeta(s, actor);
  header.append(document.createTextNode(' · '));
  header.append(el('span', { class: `history-turn-name seat-${meta.colour}`, text: meta.name }));
  if (actor === leadAgainIfSeat) {
    header.append(el('span', { class: 'history-turn-note', text: ' — slew the last royal, leads again' }));
  }
}

// ---------------------------------------------------------------- history
// Everything the player did, grouped by turn, straight from the engine log: the
// server stamps each event with its turn, so the drawer never guesses boundaries.

const HISTORY_MARK = {
  setup: '·', enemy_reveal: '⚔', play: '▶', hearts: '♥', draw: '↑', shield: '♠',
  damage: '✸', defeat: '✖', recruit: '＋', suffer: '↓', enemy_attack: '⚡',
  jester_flip: '★', yield: '⏸', win: '🏆', lose: '☠', tier: '★', turn_end: '·',
};

// cardLabel turns "10H" into "10♥" without touching the engine.
const SUIT_LETTER = { C: '♣', D: '♦', H: '♥', S: '♠' };
// cardChip renders a card id ("10H") as a small inline element: rank plus a drawn
// suit. Used wherever a card is named in running text (history, hints, buttons).
const SUIT_OF_LETTER = { C: 'clubs', D: 'diamonds', H: 'hearts', S: 'spades' };
function cardChip(id) {
  const text = String(id);
  const suit = SUIT_OF_LETTER[text.slice(-1)];
  const chip = el('span', { class: 'card-ref' });
  if (!suit) {
    chip.textContent = text;
    return chip;
  }
  chip.append(el('span', { class: 'card-ref-rank', text: text.slice(0, -1) }));
  chip.append(suitIcon(suit, { className: `suit-icon suit-${suit}` }));
  return chip;
}

// fillCards replaces a node's children with card chips separated by spaces.
function fillCards(node, ids) {
  node.replaceChildren();
  (ids || []).forEach((id, i) => {
    if (i > 0) node.append(document.createTextNode(' '));
    node.append(cardChip(id));
  });
}

function cardLabel(id) {
  const text = String(id);
  const suit = SUIT_LETTER[text.slice(-1)];
  if (!suit) return text;
  return `${text.slice(0, -1)}${suit}`;
}

// seatMeta is the name and colour of a seat, from the public seat rail the snapshot
// already carries. It never reads a hand.
function seatMeta(s, seat) {
  // The room's seat book is the name a player reads on the rail. A projection can carry
  // an older name (campaign B12 showed a setup line naming a non-seat), so the room
  // wins in co-op and the projection is the solo fallback.
  const roomSeat = (coop.room && coop.room.seats || []).find((x) => x.seat === seat);
  const row = (s.players || []).find((p) => p.seat === seat);
  return {
    name: (roomSeat && roomSeat.name) || (row && row.name) || `seat ${seat + 1}`,
    colour: seatColour(seat) || 'grey',
  };
}


let historyEscape = null;

function renderHistory(s) {
  if (!s) return;
  const list = $('#history-list');
  // Re-rendering the whole log must not re-announce every old move: keep the
  // region quiet while it is rebuilt, then restore it for the next addition.
  list.setAttribute('aria-live', 'off');
  const atBottom = list.scrollHeight - list.scrollTop - list.clientHeight < 40;
  list.replaceChildren();
  const log = (s.log || []).filter((ev) => ev.type !== 'turn_end');
  if (log.length === 0) {
    list.append(el('p', { class: 'history-empty', text: 'No moves yet.' }));
    list.setAttribute('aria-live', 'polite');
    return;
  }
  // Co-op needs attribution on every line: "who played that?" is the question a
  // four-seat table asks constantly. Solo has one seat, so nothing is drawn.
  const multi = (s.numPlayers || 1) > 1;
  const actors = attributeSeats(s.log || []);
  const attributed = new Map();
  (s.log || []).forEach((ev, i) => attributed.set(ev, actors[i]));

  let current = null;
  let groupList = null;
  let turnActor = null;
  let lastDefeatSeat = -1;
  let turnHeader = null;
  for (const ev of log) {
    const entry = historyEntry(ev, s);
    if (!entry) continue;
    const turn = ev.turn || 0;
    const actor = attributed.get(ev);
    if (turn !== current) {
      current = turn;
      turnActor = null;
      // "Turn 13 · Athelstan": the seat that acts in this turn, once we know it.
      const header = el('h3', { class: 'history-turn' });
      turnHeader = header;
      header.append(document.createTextNode(turn === 0 ? 'Setup' : `Turn ${turn}`));
      if (multi && typeof actor === 'number') {
        turnActor = actor;
        appendTurnActor(header, s, actor, lastDefeatSeat);
      }
      list.append(header);
      groupList = el('ul', { class: 'history-list' });
      list.append(groupList);
    }
    if (multi && typeof actor === 'number' && turnActor === null && turnHeader) {
      turnActor = actor;
      appendTurnActor(turnHeader, s, actor, lastDefeatSeat);
    }
    // R-034/R-035: the seat that defeated a royal skips step 4 and leads the next turn.
    // Tracked here so the next header can say why the same name appears twice — a rule
    // working correctly reads as a bug when it is not explained.
    if (ev.type === 'defeat' && typeof actor === 'number') lastDefeatSeat = actor;
    const li = el('li', { class: `ev-${ev.type}` });
    li.append(el('span', { class: 'ev-mark', text: HISTORY_MARK[ev.type] || '·', 'aria-hidden': 'true' }));
    const body = el('span');
    const textNode = el('span');
    if (entry.cards && entry.cards.length) {
      const parts = String(entry.text).split('{cards}');
      parts.forEach((part, i) => {
        if (i > 0) fillCards(textNode, entry.cards);
        if (part) textNode.append(document.createTextNode(part));
      });
    } else {
      textNode.textContent = entry.text;
    }
    body.append(textNode);
    if (entry.impact) body.append(el('span', { class: 'ev-impact', text: ` — ${entry.impact}` }));
    li.append(body);
    // The seat dot closes the line, in the same colour as that player's rail chip.
    if (multi && typeof actor === 'number') {
      const meta = seatMeta(s, actor);
      const dot = el('span', { class: `log-dot seat-${meta.colour}` });
      dot.title = meta.name;
      dot.setAttribute('aria-label', meta.name);
      li.append(dot);
    }
    groupList.append(li);
  }
  if (atBottom) list.scrollTop = list.scrollHeight;
  list.setAttribute('aria-live', 'polite');
}

function historyAsText() {
  const s = app.state;
  if (!s) return '';
  const lines = [];
  let turn = null;
  for (const ev of (s.log || [])) {
    if (ev.type === 'turn_end') continue;
    const entry = historyEntry(ev, s);
    if (!entry) continue;
    const t = ev.turn || 0;
    if (t !== turn) {
      turn = t;
      lines.push('', t === 0 ? 'Setup' : `Turn ${t}`);
    }
    // The on-screen history fills {cards} with real card chips; the copied text has no
    // DOM, so it must fill the placeholder itself (campaign A8: the copy kept "{cards}").
    const filled = entry.cards && entry.cards.length
      ? String(entry.text).split('{cards}').join(entry.cards.map((id) => cardLabel(id)).join(' '))
      : String(entry.text).split('{cards}').join('');
    lines.push(`  ${filled}${entry.impact ? ` — ${entry.impact}` : ''}`);
  }
  const head = s.enemy ? `Regicide — turn ${s.turnNo}, enemy ${cardLabel(s.enemy.id)}` : `Regicide — turn ${s.turnNo}`;
  return `${head}\n${lines.join('\n').trim()}`;
}

function toggleHistory(force) {
  const drawer = $('#history-drawer');
  const open = typeof force === 'boolean' ? force : drawer.hidden;
  if (!open) {
    closeHistory();
    return;
  }
  drawer.hidden = false;
  drawer.setAttribute('aria-hidden', 'false');
  $('#btn-history').setAttribute('aria-pressed', 'true');
  renderHistory(app.state);
  // A drawer that covers the board on a phone must behave like a sheet: Escape
  // closes it, and the keyboard goes back where it came from.
  historyEscape = (ev) => {
    if (ev.key === 'Escape') {
      ev.preventDefault();
      closeHistory();
    }
  };
  document.addEventListener('keydown', historyEscape);
  requestAnimationFrame(() => {
    drawer.classList.add('is-open');
    const list = $('#history-list');
    list.scrollTop = list.scrollHeight;
    $('#history-close').focus();
  });
}

// closeHistory is the one way out, whichever gesture asked for it.
function closeHistory() {
  const drawer = $('#history-drawer');
  if (drawer.hidden) return;
  drawer.hidden = true;
  drawer.setAttribute('aria-hidden', 'true');
  drawer.classList.remove('is-open');
  $('#btn-history').setAttribute('aria-pressed', 'false');
  if (historyEscape) {
    document.removeEventListener('keydown', historyEscape);
    historyEscape = null;
  }
  if (document.activeElement && drawer.contains(document.activeElement)) {
    // On a phone #btn-history and #btn-menu live in a hidden top-bar group; the visible
    // opener is #btn-nav. Pick the first control that can actually take focus.
    const opener = ['#btn-history', '#btn-menu', '#btn-nav']
      .map((sel) => $(sel))
      .find((node) => node && !node.hidden && node.offsetParent !== null);
    if (opener) opener.focus();
  }
}

// ---------------------------------------------------------------- overlays

let overlayKeyHandler = null;
let overlayOpener = null;

function closeOverlay() {
  hideTip();
  const root = $('#overlay-root');
  root.replaceChildren();
  document.body.style.overflow = '';
  // The Escape handler is registered per overlay; every close path must drop it,
  // otherwise backdrop clicks and button closes leak a listener each time.
  if (overlayKeyHandler) {
    document.removeEventListener('keydown', overlayKeyHandler);
    overlayKeyHandler = null;
  }
  // Return focus to whatever opened the sheet.
  if (overlayOpener && document.contains(overlayOpener) && typeof overlayOpener.focus === 'function') {
    overlayOpener.focus({ preventScroll: true });
  }
  overlayOpener = null;
}

// focusables lists the tabbable controls inside a container, in DOM order.
function focusables(root) {
  // summary is here because a <details> disclosure is a real control: without it the
  // modal's Tab trap skipped the end sheet's Log & replay group entirely (0.9.8).
  return [...root.querySelectorAll('button, summary, input, select, textarea, a[href], [tabindex]:not([tabindex="-1"])')]
    .filter((n) => !n.disabled && n.offsetParent !== null);
}

function overlay(id, build, openerSelector = null) {
  // Capture the opener first: everything below moves focus into the dialog.
  // Remember where to put the cursor back: the focused control, or the top-bar
  // button that opened this sheet (a re-render may have moved focus to <body>).
  overlayOpener = document.activeElement && document.activeElement !== document.body
    ? document.activeElement
    : (openerSelector ? document.querySelector(openerSelector) : null);
  const root = $('#overlay-root');
  root.replaceChildren();
  const wrap = document.createElement('div');
  wrap.className = 'overlay';
  wrap.id = id;
  wrap.addEventListener('click', (ev) => {
    if (ev.target === wrap) closeOverlay();
  });
  const sheet = document.createElement('div');
  sheet.className = 'sheet';
  sheet.setAttribute('role', 'dialog');
  sheet.setAttribute('aria-modal', 'true');
  sheet.append(build(sheet));
  // A dialog is announced with its title: promote the builder's first heading.
  const heading = sheet.querySelector('h2, h3');
  if (heading) {
    if (!heading.id) heading.id = `${id}-title`;
    sheet.setAttribute('aria-labelledby', heading.id);
  }
  wrap.append(sheet);
  root.append(wrap);
  document.body.style.overflow = 'hidden';
  // Focus order on open: a form field if the sheet has one (the player is about to
  // type), otherwise the title. Focusing the first Button in a long sheet (the
  // leaderboard's Close) scrolled the whole dialog to the bottom and hid the title.
  const field = sheet.querySelector('input, select, textarea');
  const focusable = sheet.querySelector('button, input, select, a, [tabindex]');
  if (field) field.focus();
  else if (heading) {
    heading.tabIndex = -1;
    heading.focus({ preventScroll: true });
    sheet.scrollTop = 0;
  } else if (focusable) focusable.focus();
  overlayKeyHandler = (ev) => {
    if (ev.key === 'Escape') {
      closeOverlay();
      return;
    }
    // Keep Tab inside the dialog: a modal with a reachable background is a
    // keyboard trap in reverse.
    if (ev.key !== 'Tab') return;
    const items = focusables(sheet);
    if (items.length === 0) return;
    const first = items[0];
    const last = items[items.length - 1];
    if (ev.shiftKey && document.activeElement === first) {
      ev.preventDefault();
      last.focus();
    } else if (!ev.shiftKey && document.activeElement === last) {
      ev.preventDefault();
      first.focus();
    }
  };
  document.addEventListener('keydown', overlayKeyHandler);
  return sheet;
}

function el(tag, attrs = {}, ...children) {
  const node = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs)) {
    if (v === false || v === null || v === undefined) continue;
    if (k === 'class') node.className = v;
    else if (k === 'text') node.textContent = v;
    else if (k === 'html') node.innerHTML = v;
    else if (k.startsWith('on') && typeof v === 'function') node.addEventListener(k.slice(2).toLowerCase(), v);
    else node.setAttribute(k, v === true ? '' : String(v));
  }
  for (const child of children.flat()) {
    if (child === null || child === undefined) continue;
    node.append(child.nodeType ? child : document.createTextNode(String(child)));
  }
  return node;
}


// pillGroup renders a row of mutually exclusive buttons and keeps aria-pressed in
// sync. Used for the card set, the card back and the animation speed.
function pillGroup(current, options, onPick) {
  const wrap = el('div', { class: 'pill-group', role: 'group' });
  for (const [value, label] of options) {
    wrap.append(
      el('button', {
        type: 'button',
        class: 'ghost small',
        'aria-pressed': String(current === value),
        text: label,
        onclick: (ev) => {
          onPick(value);
          for (const b of wrap.querySelectorAll('button')) b.setAttribute('aria-pressed', String(b === ev.currentTarget));
        },
      }),
    );
  }
  return wrap;
}

// openNav is the phone menu: the top bar collapses to one button so the board
// keeps the screen.
function openNav() {
  const inRoom = Boolean(coop.code);
  const items = [];
  // Solo-only doors are not offered at a table: a leaderboard entry needs a solo
  // session, and "restart run" restarts a run this tab does not have (F-14).
  // The desktop Menu is now reachable from the setup screen too, so key the run-only
  // doors on a live run, not on the button's visibility.
  items.push(['History', () => toggleHistory(true)]);
  items.push(['Rules', openRules]);
  items.push(['Watch the video tutorial', () => {
    window.open('https://www.youtube.com/watch?v=gfVkRaT9ZkY', '_blank', 'noopener');
  }]);
  if (!inRoom) items.push(['Leaderboard', openLeaderboard]);
  items.push(['Settings', openSettings]);
  // A player who wants out must be able to say so, and the server must hear it: the
  // tab that just hid the board left its chair occupied for minutes (notes 2 and 12).
  if (coop.code && coop.watchId) items.push(['Stop watching', stopWatchingAndLeave]);
  else if (coop.code) items.push(['Leave the table', leaveTableAndMenu]);
  // Credits are a legal notice, not a feature: they live here, off the main menu.
  items.push(['Credits & asset sources', openCredits]);
  // The exits are grouped last, the same pattern the end sheet uses: both discard
  // the run, and a first-position "abandon" on a small row was a mis-tap away from
  // the doors people actually open (0.9.9 panel).
  if (!inRoom && app.state) items.push(['Restart run', () => restartGame()]);
  if (!inRoom && app.state) items.push(['Main menu (abandon run)', () => abandonGame()]);
  overlay('nav-sheet', () => {
    const frag = document.createDocumentFragment();
    if (coop.code) {
      const code = el('div', { class: 'nav-room' });
      code.append(el('span', { class: 'nav-room-code', text: coop.room ? coop.room.code : coop.code }));
      code.append(el('span', {
        class: 'nav-room-label',
        text: coop.watchId ? 'watching' : 'room code',
      }));
      // Copy is the whole point of showing it: you read it out, or you paste the link.
      code.append(el('button', {
        type: 'button',
        class: 'ghost small',
        text: 'Copy invite',
        onclick: async () => {
          const url = `${location.origin}/?room=${coop.code}`;
          try {
            if (!(await copyToClipboard(url))) {
              toast('Link ready — copy it from the box below');
            }
            toast('Invite link copied');
          } catch {
            toast(url);
          }
        },
      }));
      frag.append(code);
    }
    // T2: the banner says "N watching", and this is who they are. A spectator has no
    // seat and no hand, so the list is labels and nothing else.
    if (coop.code && !coop.watchId) {
      const watchNames = (coop.room && coop.room.watchNames) || [];
      if (watchNames.length > 0) {
        const box = el('div', { class: 'nav-table nav-watchers' });
        box.append(el('h2', { text: watchNames.length === 1 ? '1 watching' : `${watchNames.length} watching` }));
        const list = el('ul', { class: 'nav-watcher-list' });
        for (const name of watchNames) list.append(el('li', { text: name }));
        box.append(list);
        frag.append(box);
      }
    }
    if (coop.code && isHost() && coop.room) {
      const box = el('div', { class: 'nav-table' });
      box.append(el('h2', { text: 'The table' }));
      // Handing the crown over only makes sense to a seat that is here: an away seat
      // cannot use the menu it inherits. (The button used to live in the away loop with a
      // !seat.away condition, so it could never render at all.)
      const present = (coop.room.seats || []).filter((x) => !x.away && !x.bot && x.seat !== coop.seat);
      if (present.length) {
        const row = el('div', { class: 'nav-handover' });
        row.append(el('span', { class: 'hint', text: 'Hand the crown to: ' }));
        for (const seat of present) {
          row.append(el('button', {
            type: 'button',
            class: 'ghost small',
            text: seat.name,
            onclick: async () => {
              try {
                await handOverHost(seat.seat);
                closeOverlay();
                startUpdates(() => renderRoomScreen());
                renderRoomScreen();
                toast(`${seat.name} is the host now`);
              } catch (err) {
                toast(err.message || 'Could not hand over the crown');
              }
            },
          }));
        }
        box.append(row);
      }
      // T11: removing somebody mid-game is the lobby kick's dealt-table form, and it
      // must be offered for a seat that is still here — not only for an away one.
      const seated = !coop.room.lobby
        ? (coop.room.seats || []).filter((x) => !x.away && !x.left && !x.bot && x.seat !== coop.seat)
        : [];
      for (const seat of seated) {
        const row = el('div', { class: 'nav-table-row' });
        row.append(el('span', { text: seat.name }));
        row.append(el('button', {
          type: 'button',
          class: 'ghost small nav-danger',
          text: 'Kick',
          title: 'The seat stays and is left to a bot when bot fill is on',
          onclick: async () => {
            try {
              await kickSeat(seat.seat);
              closeOverlay();
              startUpdates(() => renderRoomScreen());
              renderRoomScreen();
              toast(`${seat.name} is out of the game`);
            } catch (err) {
              toast(err.message || 'Could not remove that seat');
            }
          },
        }));
        box.append(row);
      }
      // A left seat (note 12) and a bot seat are both the host's to hand on; neither is
      // "away", and the old filter only listed away seats, so they could not be reached.
      const away = (coop.room.seats || []).filter((x) => (x.away || x.left || x.bot) && x.seat !== coop.seat);
      if (coop.room.waitingFor) {
        box.append(el('p', { class: 'hint', text: `Waiting for ${coop.room.waitingFor}. The table resumes the moment their dot moves.` }));
      }
      for (const seat of away) {
        const row = el('div', { class: 'nav-table-row' });
        const label = seat.left ? 'left' : (seat.bot ? 'bot' : `away ${formatDuration(seat.idleSeconds || 0)}`);
        row.append(el('span', { text: `${seat.name} · ${label}` }));
        row.append(el('button', {
          type: 'button',
          class: 'ghost small',
          text: 'Link',
          title: 'Copy a one-use invite link for this seat',
          onclick: async () => {
            try {
              const res = await invite(seat.seat);
              const link = `${location.origin}${res.link}`;
              if (!(await copyToClipboard(link))) {
                toast('Link ready — copy it from the box below');
              }
              toast('One-use link copied — it expires in ten minutes');
            } catch (err) {
              toast(err.message || 'Could not make a link');
            }
          },
        }));
        if (seat.away && !seat.bot) {
          row.append(el('button', {
            type: 'button',
            class: 'ghost small',
            text: 'Bot',
            title: 'The bot plays this seat until the player comes back',
            onclick: async () => {
              try {
                await setBotSeat(seat.seat, true);
                closeOverlay();
                startUpdates(() => renderRoomScreen());
                renderRoomScreen();
                toast(`${seat.name} is played by a bot`);
              } catch (err) {
                toast(err.message || 'Could not hand the seat over');
              }
            },
          }));
        }
        if (seat.bot) {
          row.append(el('button', {
            type: 'button',
            class: 'ghost small',
            text: 'Release',
            title: 'Release the bot; the seat goes back to its player',
            onclick: async () => {
              try {
                await setBotSeat(seat.seat, false);
                closeOverlay();
                startUpdates(() => renderRoomScreen());
                renderRoomScreen();
              } catch (err) {
                toast(err.message || 'Could not take the seat back');
              }
            },
          }));
        }
        // A bot's chair and a left chair are free at once; a merely silent human seat
        // keeps the five-minute reconnect window the server enforces.
        // Removing somebody mid-game is the lobby kick's dealt-table form (note 1): the
        // seat stays (the rules never shrink), is marked left, and a bot takes it when
        // bot fill is on. It belongs in this menu because it is a table decision.
        row.append(el('button', {
          type: 'button',
          class: 'ghost small nav-danger',
          text: 'Kick',
          title: 'Take the seat away from this player',
          onclick: async () => {
            try {
              await kickSeat(seat.seat);
              closeOverlay();
              startUpdates(() => renderRoomScreen());
              renderRoomScreen();
              toast(`${seat.name} is out of the game`);
            } catch (err) {
              toast(err.message || 'Could not remove that seat');
            }
          },
        }));
        if (seat.takeable || seat.left || seat.bot) {
          row.append(el('button', {
            type: 'button',
            class: 'ghost small',
            text: 'Cover',
            title: 'Play this seat from this tab',
            onclick: async () => {
              try {
                await takeSeat(seat.seat);
                closeOverlay();
                startUpdates(() => renderRoomScreen());
                renderRoomScreen();
                toast(`You are now ${seat.name}`);
              } catch (err) {
                toast(err.message || 'Could not take the seat');
              }
            },
          }));
        }
        box.append(row);
      }
      if (away.length === 0 && seated.length === 0 && !coop.room.waitingFor) {
        box.append(el('p', { class: 'hint', text: 'Everyone is here.' }));
      }
      box.append(el('button', {
        type: 'button',
        class: 'ghost small nav-danger',
        text: 'Close the table for everyone',
        onclick: async () => {
          try {
            await closeTable();
            closeOverlay();
            showSetup();
          } catch (err) {
            toast(err.message || 'Could not close the table');
          }
        },
      }));
      frag.append(box);
    }
    // While this tab holds somebody else's chair it is not the host for the server,
    // so isHost() is false and the block above is skipped. The chair it left is still
    // its own, and this is the way back (note 11).
    if (coop.code && coveringSeat() && coop.room) {
      const own = returnSeat();
      const mine = (coop.room.seats || []).find((x) => own && x.seat === own.seat);
      const box = el('div', { class: 'nav-table' });
      box.append(el('h2', { text: 'Covering a seat' }));
      box.append(el('p', {
        class: 'hint',
        text: mine ? `You are playing ${mine.name}. Your own seat is ${coop.room.seats.find((x) => x.seat === own.seat) ? 'still yours' : 'waiting'}.` : 'You are playing another seat.',
      }));
      box.append(el('button', {
        type: 'button',
        class: 'ghost small',
        text: 'Return to your own seat',
        onclick: async () => {
          try {
            await returnToOwnSeat();
            closeOverlay();
            startUpdates(() => renderRoomScreen());
            renderRoomScreen();
            toast('Back on your own seat');
          } catch (err) {
            toast(err.message || 'Could not return to your seat');
          }
        },
      }));
      frag.append(box);
    }
    frag.append(el('h2', { text: 'Menu' }));
    const list = el('div', { class: 'nav-list' });
    for (const [label, fn] of items) {
      list.append(el('button', {
        type: 'button',
        class: 'ghost nav-item',
        text: label,
        onclick: () => {
          closeOverlay();
          fn();
        },
      }));
    }
    frag.append(list);
    frag.append(sheetClose());
    return frag;
  }, '#btn-nav');
}

// previewBlock shows the chosen card face, card back and Jester face together, so a
// choice can be judged before it is committed.
function previewBlock(themeId, backId, jokers, className = '') {
  const wrap = el('div', { class: ['preview', className].filter(Boolean).join(' ') });
  const face = el('figure', { class: 'preview-item' });
  const faceImg = el('img', { src: artUrl(themeId, 'QS'), alt: '' });
  if (themeInfo(themeId).pixelated) faceImg.classList.add('is-pixelated');
  face.append(faceImg, el('figcaption', { text: 'front' }));
  const back = el('figure', { class: 'preview-item' });
  const backImg = el('img', { src: backUrl(backId), alt: '' });
  back.append(backImg, el('figcaption', { text: 'back' }));
  wrap.append(face, back);
  // Both Jesters are previewed: they can be two different faces.
  const pair = Array.isArray(jokers) && jokers.length ? jokers : [themeJoker(themeId), themeJoker(themeId)];
  for (let i = 0; i < 2; i += 1) {
    const info = jokerInfo(resolveJokerId(themeId, pair[i]));
    const figure = el('figure', { class: 'preview-item' });
    const img = el('img', { src: info.path, alt: '' });
    figure.append(img, el('figcaption', { text: `jester ${i + 1}` }));
    wrap.append(figure);
  }
  return wrap;
}

function openSettings() {
  // Visual settings preview live and are reverted on Cancel; rule options only
  // apply to the next run (a session's rules are frozen once it starts).
  const original = { theme: app.prefs.theme, back: app.prefs.back, jokers: [...app.prefs.jokers], jokersPicked: app.prefs.jokersPicked };
  const draft = { ...app.prefs, rules: { ...app.prefs.rules } };

  overlay('settings-sheet', (sheet) => {
    const frag = document.createDocumentFragment();
    frag.append(el('h2', { id: 'settings-title', text: 'Settings' }));

    // ---- card set
    frag.append(el('h3', { text: 'Card set' }));
    // Declared before the theme grid: its click handler re-paints the Jester slots,
    // and every handler runs after this builder has finished.
    let refreshJokerUI = () => {};
    const themeGrid = el('div', { class: 'tile-grid theme-grid', role: 'group', 'aria-label': 'Card set' });
    const themeTiles = new Map();
    for (const t of themes()) {
      const tile = el('button', {
        type: 'button',
        class: 'tile theme-tile',
        'aria-pressed': String(draft.theme === t.id),
        'data-theme-id': t.id,
        title: t.label,
        onclick: () => {
          draft.theme = t.id;
          app.prefs.theme = t.id;
          app.prefs.themeExplicit = true;
          // A set brings its own back and its own Jester face until the player
          // picks another one.
          const preferred = themeInfo(t.id).defaultBack;
          if (preferred) {
            draft.back = preferred;
            app.prefs.back = preferred;
            backSelect.value = preferred;
          }
          draft.jokers = jokerPairFor(t.id);
          draft.jokersPicked = false;
          app.prefs.jokers = [...draft.jokers];
          app.prefs.jokersPicked = false;
          pushLobbyJokers();
          refreshJokerUI();
          applyPrefs();
          render();
          for (const [id, node] of themeTiles) node.setAttribute('aria-pressed', String(id === t.id));
          refreshPreview();
        },
      });
      const art = el('span', { class: 'tile-art' });
      const img = document.createElement('img');
      img.src = artUrl(t.id, 'QS');
      img.alt = '';
      img.loading = 'lazy';
      if (t.pixelated) img.classList.add('is-pixelated');
      art.append(img);
      // Sets that print our rule text have a blank middle, so the thumbnail gets a
      // sample line; it shows at a glance which sets explain themselves on the card.
      if (t.hasText) art.append(el('span', { class: 'tile-sample', text: 'Queen of Spades' }));
      tile.append(art, el('span', { class: 'tile-label', text: t.label }));
      themeTiles.set(t.id, tile);
      themeGrid.append(tile);
    }
    frag.append(themeGrid);

    // ---- whose faces this tab draws in a room
    frag.append(el('h3', { text: 'Card set at other tables' }));
    const viewRow = el('div', { class: 'setting-row' });
    const viewSelect = el('select', {
      id: 'settings-view-deck',
      class: 'select',
      'aria-label': 'Card set at other tables',
      onchange: (ev) => {
        app.prefs.tableDeck = ev.currentTarget.value;
        savePrefs();
        render();
      },
    });
    for (const [id, label] of [['table', "Use the table's set"], ['mine', 'Use my own set (this tab only)']]) {
      const opt = el('option', { value: id, text: label });
      opt.selected = app.prefs.tableDeck === id;
      viewSelect.append(opt);
    }
    viewRow.append(el('span', { class: 'setting-name', text: 'Card set at other tables' }), viewSelect);
    frag.append(viewRow);
    frag.append(el('p', {
      class: 'hint',
      text: 'Only your view changes; the table still plays the set the host dealt.',
    }));

    // ---- card back (a dropdown: the list is long and the preview does the work)
    frag.append(el('h3', { text: 'Card back' }));
    const backRow = el('div', { class: 'setting-row' });
    const backSelect = el('select', {
      id: 'settings-back',
      class: 'select',
      'aria-label': 'Card back',
      onchange: (ev) => {
        draft.back = ev.currentTarget.value;
        app.prefs.back = ev.currentTarget.value;
        render();
        refreshPreview();
      },
    });
    for (const b of backs()) {
      const opt = el('option', { value: b.id, text: b.label });
      opt.selected = b.id === draft.back;
      backSelect.append(opt);
    }
    backRow.append(el('span', { class: 'setting-name', text: 'Card back' }), backSelect);
    frag.append(backRow);

    // ---- Jester faces (two slots; each can be any face, or the same one twice)
    frag.append(el('h3', { text: 'Jester faces' }));
    frag.append(el('p', {
      class: 'hint',
      text: 'Each Jester can show a different face. Pick a slot, then a face; the same face twice is fine.',
    }));
    let slot = 0;
    const slotButtons = [];
    const slotsRow = el('div', { class: 'joker-slots', role: 'group', 'aria-label': 'Jester slots' });
    for (let i = 0; i < 2; i += 1) {
      const node = el('button', { type: 'button', class: 'joker-slot', 'data-slot': String(i) });
      const img = document.createElement('img');
      img.alt = '';
      const label = el('span', { class: 'joker-slot-label' });
      node.append(img, label);
      node.addEventListener('click', () => { slot = i; refreshJokerUI(); });
      slotButtons.push({ node, img, label });
      slotsRow.append(node);
    }
    frag.append(slotsRow);

    const jokerGrid = el('div', { class: 'tile-grid joker-grid', role: 'group', 'aria-label': 'Jester faces' });
    const jokerTiles = new Map();
    for (const j of jokers()) {
      const tile = el('button', {
        type: 'button',
        class: 'tile joker-tile',
        'data-joker-id': j.id,
        title: j.label,
      });
      const art = el('span', { class: 'tile-art' });
      const img = document.createElement('img');
      img.src = j.path;
      img.alt = '';
      img.loading = 'lazy';
      art.append(img);
      const badge = el('span', { class: 'joker-slot-badge' });
      badge.hidden = true;
      tile.append(art, el('span', { class: 'tile-label', text: j.label }), badge);
      tile.addEventListener('click', () => {
        const next = [...draft.jokers];
        next[slot] = j.id;
        draft.jokers = next;
        draft.jokersPicked = true;
        app.prefs.jokers = [...next];
        app.prefs.jokersPicked = true;
        pushLobbyJokers();
        // Move to the other slot so picking two faces is two taps; picking the same
        // face twice is still two taps.
        slot = slot === 0 ? 1 : 0;
        refreshJokerUI();
      });
      jokerTiles.set(j.id, { tile, badge });
      jokerGrid.append(tile);
    }
    frag.append(jokerGrid);

    const previewHost = el('div', { class: 'preview-host' });
    const refreshPreview = () => {
      previewHost.replaceChildren(previewBlock(draft.theme, draft.back, draft.jokers));
    };
    frag.append(previewHost);

    refreshJokerUI = () => {
      for (let i = 0; i < 2; i += 1) {
        const info = jokerInfo(resolveJokerId(draft.theme, draft.jokers[i]));
        slotButtons[i].img.src = info.path;
        slotButtons[i].label.textContent = `${i === 0 ? 'First' : 'Second'} Jester · ${info.label}`;
        slotButtons[i].node.setAttribute('aria-pressed', String(slot === i));
        slotButtons[i].node.classList.toggle('is-on', slot === i);
      }
      for (const [id, { tile, badge }] of jokerTiles) {
        const which = [0, 1].filter((i) => draft.jokers[i] === id).map((i) => i + 1);
        tile.setAttribute('aria-pressed', String(which.length > 0));
        badge.textContent = which.join('+');
        badge.hidden = which.length === 0;
      }
      refreshPreview();
    };
    refreshJokerUI();

    // ---- motion
    frag.append(el('h3', { text: 'Motion' }));
    const motion = el('div', { class: 'segmented', role: 'group', 'aria-label': 'Animations' });
    for (const value of ['off', 'normal', 'fast']) {
      motion.append(
        el('button', {
          type: 'button',
          class: 'seg',
          text: value,
          'aria-pressed': String(draft.animations === value),
          onclick: (ev) => {
            draft.animations = value;
            for (const b of motion.querySelectorAll('button')) b.setAttribute('aria-pressed', String(b === ev.currentTarget));
          },
        }),
      );
    }
    frag.append(motion);

    // ---- sound
    frag.append(el('h3', { text: 'Sound' }));
    const soundRow = el('div', { class: 'setting-row' });
    const soundSwitch = switchButton(draft.sound, (on) => {
      draft.sound = on;
      volume.disabled = !on;
    });
    soundRow.append(el('span', { class: 'setting-name', text: 'Sound effects' }), soundSwitch);
    frag.append(soundRow);

    const volumeRow = el('div', { class: 'setting-row' });
    const volume = el('input', {
      type: 'range',
      min: '0',
      max: '1',
      step: '0.05',
      value: String(draft.volume),
      'aria-label': 'Volume',
      oninput: (ev) => {
        draft.volume = Number(ev.currentTarget.value);
      },
    });
    volume.disabled = !draft.sound;
    volumeRow.append(el('span', { class: 'setting-name', text: 'Volume' }), volume);
    frag.append(volumeRow);

    // ---- rules
    frag.append(el('h3', { text: 'Rules (next run)' }));
    const ruleRows = [
      ['soloJester', 'Solo Jesters', 'Flip a Jester to discard your hand and refill. Sets the medal tier.'],
      ['exactDamage', 'Exact kills return to the tavern', 'An enemy killed exactly goes on top of the tavern and joins your hand.'],
      ['allowSoloYield', 'Allow yielding alone', 'Skip steps 2-3 and suffer the attack. Off by default.'],
      ['allowOverpayDiscard', 'Allow overpaying in step 4', 'Keep discarding past the required total and end payment yourself.'],
      ['chaos', 'Chaos royals', 'Shuffle all twelve Jacks, Queens and Kings instead of the fixed ramp. Not leaderboard-eligible.'],
    ];
    for (const [key, label, hint] of ruleRows) {
      const row = el('div', { class: 'setting-row setting-rule' });
      const text = el('span', { class: 'setting-name' });
      text.append(el('span', { text: label }), el('small', { class: 'setting-hint', text: hint }));
      row.append(text, switchButton(draft.rules[key], (on) => { draft.rules[key] = on; }));
      frag.append(row);
    }

    frag.append(
      el('div', { class: 'sheet-actions' },
        el('button', {
          type: 'button',
          class: 'ghost',
          text: 'Cancel',
          onclick: () => {
            app.prefs.theme = original.theme;
            app.prefs.back = original.back;
            app.prefs.jokers = [...original.jokers];
            app.prefs.jokersPicked = original.jokersPicked;
            applyPrefs();
            render();
            closeOverlay();
          },
        }),
        el('button', {
          type: 'button',
          class: 'primary',
          text: 'Save',
          onclick: () => {
            app.prefs = draft;
            app.audio.setEnabled(draft.sound);
            app.audio.setVolume(draft.volume);
            applyPrefs();
            savePrefs();
            closeOverlay();
            render();
          },
        }),
      ),
    );
    return frag;
  }, '#btn-nav');
}

// switchButton is an accessible on/off control (aria-pressed on a real button).
function switchButton(initial, onChange) {
  const btn = el('button', {
    type: 'button',
    class: 'switch',
    role: 'switch',
    'aria-checked': String(Boolean(initial)),
    'aria-label': switchButton.label || 'Toggle',
  });
  const paint = (on) => {
    btn.setAttribute('aria-checked', String(on));
    btn.textContent = '';
    btn.append(el('span', { class: 'switch-knob', 'aria-hidden': 'true' }), el('span', { class: 'switch-text', text: on ? 'On' : 'Off' }));
  };
  paint(Boolean(initial));
  btn.addEventListener('click', () => {
    const next = btn.getAttribute('aria-checked') !== 'true';
    paint(next);
    onChange(next);
  });
  return btn;
}

async function openLeaderboard() {
  let data = { gold: [], silver: [], bronze: [] };
  try {
    const res = await api('/api/leaderboard?limit=10');
    data = res.body || data;
  } catch {
    /* show empty board */
  }
  overlay('leaderboard-sheet', () => {
    const frag = document.createDocumentFragment();
    frag.append(el('h2', { text: 'Hall of the kingless' }));
    frag.append(el('p', { class: 'hint', text: 'Only runs with a server-chosen seed can enter the hall.' }));
    // An empty hall is one sentence, not three "No entries yet." footnotes under
    // three headings (0.9.9 panel); per-tier headings return as soon as any tier has
    // an entry.
    const any = ['gold', 'silver', 'bronze'].some((t) => (data[t] || []).length > 0);
    if (!any) {
      frag.append(el('p', { class: 'empty', text: 'The hall is empty. Win a run with a server-chosen seed to enter it.' }));
      frag.append(sheetClose());
      return frag;
    }
    for (const tier of ['gold', 'silver', 'bronze']) {
      frag.append(el('h3', { class: `lb-tier tier-${tier}`, text: `${tier} victories` }));
      const rows = data[tier] || [];
      if (rows.length === 0) {
        frag.append(el('p', { class: 'empty', text: 'No entries yet.' }));
        continue;
      }
      // A real table with fixed layout: the rank column is narrow, the name takes
      // the slack, and the numbers are right-aligned in tabular figures so the
      // digits line up column to column.
      const table = el('table', { class: 'lb-table' });
      const head = el('tr', {},
        el('th', { class: 'rank', scope: 'col', text: '#' }),
        el('th', { class: 'name', scope: 'col', text: 'name' }),
        el('th', { class: 'num', scope: 'col', text: 'turns' }),
        el('th', { class: 'num', scope: 'col', text: 'jesters' }),
        el('th', { class: 'num played', scope: 'col', text: 'time' }),
      );
      const tbody = el('tbody');
      rows.forEach((entry, i) => {
        const tr = el('tr', {},
          el('td', { class: 'rank', text: String(i + 1) }),
          el('td', { class: 'name', title: entry.name }, el('span', { text: entry.name })),
          el('td', { class: 'num', text: String(entry.turns) }),
          el('td', { class: 'num', text: String(entry.jesters) }),
          el('td', { class: 'num played', text: formatDuration(entry.durationSeconds) }),
        );
        if (entry.theme) tr.dataset.theme = entry.theme;
        tbody.append(tr);
      });
      table.append(el('thead', {}, head), tbody);
      frag.append(table);
    }
    frag.append(sheetClose());
    return frag;
  }, '#btn-nav');
}

// sheetClose is the standard dismiss row: one primary Close at the end of a sheet.
// Three sheets built this exact markup by hand.
function sheetClose() {
  return el('div', { class: 'sheet-actions' },
    el('button', { type: 'button', class: 'primary', text: 'Close', onclick: closeOverlay }));
}

function openRules() {
  overlay('rules-sheet', () => {
    const frag = document.createDocumentFragment();
    // One title for both modes: the sentences below already say solo or co-op where
    // the rule differs, and a mode in the heading read as two different rulebooks.
    frag.append(el('h2', { text: 'How to play Regicide' }));
    const body = el('div', { class: 'rules-body' });
    body.innerHTML = `
      <p class="rules-video"><a href="https://www.youtube.com/watch?v=gfVkRaT9ZkY"
        target="_blank" rel="noopener"><span aria-hidden="true">▶</span> Watch the video: how to play Regicide</a></p>

      <p><strong>Goal.</strong> Defeat all twelve royals — four Jacks, then four Queens, then four
      Kings — before the cards run out. Solo: you lose when your hand cannot pay an attack. Co-op:
      the table loses when the seat that must pay cannot.</p>

      <h3>The table</h3>
      <ul>
        <li><strong>Castle.</strong> The twelve royals, face down: Jacks on top, then Queens, then
            Kings. The top card is the current enemy.</li>
        <li><strong>Tavern.</strong> Every 2–10 and the four Animal Companions (Aces). Solo deals an
            8-card hand. Co-op deals 7, 6 or 5 cards at 2, 3 or 4 seats. Every seat is dealt to its
            limit; after that, only a diamond draw or a Jester refills a hand.</li>
        <li><strong>Royals.</strong> Jack 10 attack / 20 health · Queen 15 / 30 · King 20 / 40.</li>
      </ul>

      <h3>A turn</h3>
      <ol>
        <li><strong>Play</strong> one card, a same-number combo, an Animal Companion pairing — or
            yield.</li>
        <li><strong>Resolve the suit power</strong> (it is mandatory).</li>
        <li><strong>Deal damage.</strong> Clubs double it. At zero health the royal is defeated and
            its attack is skipped.</li>
        <li><strong>Exact kill.</strong> If the damage equals the royal's remaining health, it goes
            face down on top of the tavern and can be drawn and played later.</li>
        <li><strong>The killing blow is not a rest.</strong> A defeat skips payment, and the same
            seat faces the next royal — unless the host turns on <em>Turn passes when a royal
            falls</em>.</li>
        <li><strong>Pay the attack</strong> by discarding cards whose values total at least the
            attack. In co-op, the active seat pays; if it cannot, the table loses.</li>
      </ol>

      <h3>Suit powers</h3>
      <ul>
        <li><strong>♥ Hearts</strong> — shuffle the discard and put that many cards <em>under</em>
            the tavern, so they are drawn later.</li>
        <li><strong>♦ Diamonds</strong> — draw that many cards, starting with the acting seat and
            skipping a full hand.</li>
        <li><strong>♣ Clubs</strong> — damage is doubled.</li>
        <li><strong>♠ Spades</strong> — reduce this royal's attack by that much. The shield stays
            until the royal dies.</li>
      </ul>
      <p>Hearts resolve before diamonds. A royal is <strong>immune to its own suit's power</strong>;
      the damage still counts. The immunity badge names the blocked suit, and each hand card of that
      suit carries a small ⊘ mark.</p>

      <h3>Combos and companions</h3>
      <ul>
        <li>Play 2–4 cards of the <em>same number</em> when their total is 10 or less (two 5s, three
            2s, four 2s…). Every power resolves once, at the total.</li>
        <li>An <strong>Animal Companion (Ace)</strong> counts as 1 and may pair with one other card
            (never a Jester) or with another Ace. It cannot join a number combo. Its power applies
            once.</li>
      </ul>

      <h3>Jesters</h3>
      <ul>
        <li><strong>Solo:</strong> twice per game, at the start of step 1 or step 4, flip a Jester:
            discard your hand and refill to 8 from the tavern. It never cancels immunity and never
            counts as a diamond draw. Wins are ranked <strong>gold</strong> (none),
            <strong>silver</strong> (one) or <strong>bronze</strong> (two).</li>
        <li><strong>Co-op:</strong> a Jester is a card in the tavern (one at 3 seats, two at 4). Play
            it on its own. It cancels the royal's immunity, deals no damage, skips payment, and lets
            you choose who acts next.</li>
      </ul>

      <h3>Co-op at a table</h3>
      <ul>
        <li>Seats act in order. By default the seat that lands the killing blow faces the next royal;
            the room option <em>Turn passes when a royal falls</em> hands it to the next seat.</li>
        <li>Each seat pays its own attack. Leaving a teammate a card to pay with is part of the game,
            not politeness.</li>
        <li>The host deals the settings in the lobby. A seat silent for a minute can be filled by a
            bot until its player touches it again.</li>
        <li>Joining mid-game takes the lowest bot or empty chair. The table never grows after the
            deal.</li>
      </ul>

      <h3>Table settings (host)</h3>
      <ul>
        <li><strong>Card set</strong> — the art the table is dealt: Official, Simple, Standard or
            Pokémon. Pokémon art is detailed, so it is harder to read at four hands and on a phone.
            Fixed when the host deals; a player may look at a different set locally, and only their
            own screen changes.</li>
        <li><strong>Chaos (royals mixed in)</strong> — shuffle all twelve royals into one pile, so a
            King can open the run. Chaos runs do not enter the leaderboard.</li>
        <li><strong>Turn passes when a royal falls</strong> — easier: the next seat takes the fresh
            royal instead of the killer facing it again.</li>
        <li><strong>Exact damage</strong> — a royal killed with exactly its remaining health goes on
            top of the tavern and joins a hand when drawn.</li>
        <li><strong>Spectators</strong> — people without a seat may watch read-only. They cannot act
            and cannot take a chair.</li>
        <li><strong>Spectator hands</strong> — a spectator may look through one player's hand at a
            time. Off: they see only the table.</li>
        <li><strong>Bots fill missing seats</strong> — a seat silent for a minute gets a bot until its
            player touches it. The bot uses the same rules and sees only its own hand.</li>
        <li><strong>Add a bot</strong> — put a bot in the next free chair. Bots count as seats and
            take a full turn.</li>
      </ul>

      <h3>Seeds and the hall</h3>
      <p>A server-chosen seed stays hidden until the game ends and is the only kind that can enter
      the leaderboard. Type your own seed to replay a deal; those runs are practice.</p>

      <p class="hint">Full source of truth: the official Regicide rulebook by Badgers from Mars. This
      screen is a summary; the engine implements the rulebook and the room options above.</p>`;
    frag.append(body);
    frag.append(el('div', { class: 'sheet-actions' }, el('button', { type: 'button', class: 'primary', text: 'Got it', onclick: closeOverlay })));
    return frag;
  }, '#btn-rules');
}

function openCredits() {
  overlay('credits-sheet', () => {
    const frag = document.createDocumentFragment();
    frag.append(el('h2', { text: 'Credits & licences' }));
    const list = el('div', { class: 'credits-list' });
    list.innerHTML = `
      <p><strong>Regicide — game credits.</strong> Designer: Andy Richdale, Luke Badger and
      Paul Abrahams. Artist: D.J. Phillips. Publisher: Badgers from Mars.</p>
      <p><strong>Crisp Cards — Free Sampler</strong> sound effects by Anipixels
      (<a href="https://anipixels.itch.io/crisp-cards" rel="noopener" target="_blank">anipixels.itch.io</a>), embedded in this game only.
      The missing effects were synthesised for this project.</p>
      <p><strong>Regicide</strong> game design © Badgers from Mars (Paul Abrahams, Luke Badger, Andy Richdale;
      art by Sketchgoblin). This is an unofficial, non-commercial fan implementation of the published rules.</p>
      <p><strong>Official deck</strong> card art © Badgers from Mars, art by Sketchgoblin — the published
      Regicide deck, supplied by the operator for this private, non-commercial build.</p>
      <p><strong>Pokémon</strong> © Nintendo / Game Freak / Creatures Inc. The "RageIced" card sheets are a fan re-theme by
      Wil Su; this build is private and non-commercial.</p>
      <p>Rules reference: the official A4 rulebook.</p>`;
    frag.append(list);
    frag.append(sheetClose());
    return frag;
  });
}

// cardFace renders one card large, with the authored text underneath for sets
// that have no room to print it (image-only themes).
function cardFace(card, opts = {}) {
  const wrap = el('div', { class: 'zoom-card' });
  // The sheet belongs to the table, not to the menu: a co-op room deals one of two
  // sets and the zoomed card must be the one the player is looking at (note 9).
  const theme = opts.theme || boardTheme(app.state);
  const face = cardElement(card, { theme, size: 'zoom-size', jokers: tableJokers(app.state) });
  face.tabIndex = -1;
  face.setAttribute('aria-hidden', 'true');
  wrap.append(face);
  const info = textFor(card.id);
  // Jester faces come from the joker catalogue and always explain themselves, so a
  // hasText set still needs the caption for them.
  const printed = themeInfo(theme).hasText && !isJesterId(card.id);
  if (printed && !opts.forceCaption) return wrap;
  const caption = el('p', { class: 'zoom-caption' });
  // A royal's worth is not in its name: "Jack of Clubs (10)", same as the face.
  const captionName = info ? info.title : card.label || card.id;
  caption.append(el('strong', { text: isRoyalRank(card.rank) ? `${captionName} (${card.value})` : captionName }));
  caption.append(el('span', { class: 'zoom-value', text: info ? info.value : `value ${card.value}` }));
  if (info && info.power) caption.append(el('span', { class: 'zoom-power', text: info.power }));
  if (info && info.note) caption.append(el('span', { class: 'zoom-note', text: info.note }));
  wrap.append(caption);
  if (opts.onPick) {
    wrap.append(el('button', {
      type: 'button',
      // pick-btn keeps the action distinct from the decorative card button
      class: `pick-btn ${opts.selected ? 'ghost' : 'primary'} small`,
      text: opts.label || (opts.selected ? 'Remove from play' : 'Select for play'),
      onclick: () => opts.onPick(card),
    }));
  }
  return wrap;
}

// openCardZoom shows a card (or a pile of cards) at a legible size. Used by a
// touch tap, by the discard pile and by the Jester confirmation.
function openCardZoom(cards, { title, actions = [], onPickCard = null } = {}) {
  overlay('zoom-sheet', () => {
    const frag = document.createDocumentFragment();
    frag.append(el('h2', { text: title || (cards.length === 1 ? 'Card' : `${cards.length} cards`) }));
    const grid = el('div', { class: cards.length === 1 ? 'zoom-grid single' : 'zoom-grid' });
    for (const c of cards) {
      grid.append(cardFace(c, onPickCard ? { onPick: onPickCard, selected: app.selected.has(c.id) } : {}));
    }
    frag.append(grid);
    const row = el('div', { class: 'sheet-actions' });
    for (const a of actions) {
      row.append(el('button', { type: 'button', class: a.class || 'ghost', text: a.label, onclick: a.onClick }));
    }
    row.append(el('button', { type: 'button', class: 'primary', text: 'Close', onclick: closeOverlay }));
    frag.append(row);
    return frag;
  });
}

// confirmJesterFlip explains the cost of a Jester and asks before spending it.
function confirmJesterFlip() {
  const s = app.state;
  if (!s) return;
  const card = { id: 'J1', value: 0, label: 'Jester' };
  openCardZoom([card], {
    title: 'Jester',
    actions: [{
      label: `Flip Jester (discard ${s.hand.length} card${s.hand.length === 1 ? '' : 's'} · refill to ${s.maxHand})`,
      class: 'primary',
      onClick: () => {
        closeOverlay();
        act({ type: 'flip_jester' });
      },
    }],
  });
}

// Long-press (touch) or right-click (mouse) opens the readable sheet; a plain tap
// selects, because selection is the move the player is making most often.
function openCardDetail(card) {
  openCardZoom([card], { title: textFor(card.id)?.title || card.label || card.id, forceCaption: false });
}

// downloadLog saves a replay file. The fetch carries the credential header the plain
// navigation cannot, then the blob is handed to an anchor.
async function downloadLog(url, filename, headers) {
  const res = await fetch(url, { headers, cache: 'no-store' });
  if (!res.ok) throw new Error(`log_${res.status}`);
  const blob = await res.blob();
  const href = URL.createObjectURL(blob);
  const a = el('a', { href, download: filename });
  document.body.append(a);
  a.click();
  a.remove();
  URL.revokeObjectURL(href);
}

// watchReplay mints a short-lived ticket and opens the read-only viewer in a new tab.
async function watchReplay(mintUrl, headers) {
  const res = await fetch(mintUrl, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', ...headers },
    body: '{}',
  });
  if (!res.ok) throw new Error(`ticket_${res.status}`);
  const data = await res.json();
  window.open(`/replay?replay=${encodeURIComponent(data.ticket)}`, '_blank', 'noopener');
}

// A finished table still owes the loser's last reveal. Opening the sheet on the same
// frame cut the bot's play off mid-flight; wait for the reveal queue, then a beat.
let endSheetTimer = 0;
function scheduleEndSheet(s) {
  if (endSheetTimer) return;
  const started = Date.now();
  const tick = () => {
    if (!app.state || !(app.state.won || app.state.lost) || $('#end-sheet')) {
      endSheetTimer = 0;
      return;
    }
    if ((revealBusy || revealQueue.length > 0) && Date.now() - started < 8000) {
      endSheetTimer = setTimeout(tick, 120);
      return;
    }
    endSheetTimer = setTimeout(() => {
      endSheetTimer = 0;
      if (app.state && (app.state.won || app.state.lost) && !$('#end-sheet')) showEndSheet(app.state);
    }, 250);
  };
  endSheetTimer = setTimeout(tick, 250);
}

function showEndSheet(s) {
  overlay('end-sheet', (sheet) => {
    // Heading and result copy come from the pure model; this function owns only the
    // DOM and the handlers. 0.9.8: the multiplayer sheet offers Play again, one
    // "Log & replay" disclosure, Main menu and Close, instead of seven flat buttons.
    if (!endTitle) endTitle = resultHeading(Boolean(s.won));
    const names = [];
    for (const row of ((coop.room && coop.room.seats) || s.players || [])) {
      if (row && Number.isInteger(row.seat)) names[row.seat] = row.name;
    }
    const model = endSheetModel(s, {
      title: endTitle,
      inRoom: Boolean(coop.code),
      isHost: isHost(),
      seat: coop.seat,
      watching: Boolean(coop.watchId),
      hasSession: Boolean(app.sessionId),
      names,
    });
    const frag = document.createDocumentFragment();
    frag.append(el('h2', { text: model.title }));
    for (const line of model.lines) {
      frag.append(el('p', line.class ? { class: line.class } : {}, line.text));
    }
    if (model.awards.length > 0) {
      const list = el('ul', { class: 'awards' });
      for (const award of model.awards) {
        const row = el('li', { class: `award award-${award.key}` });
        row.append(el('span', { class: 'award-label', text: award.label }));
        row.append(el('span', { class: 'award-value', text: award.text }));
        list.append(row);
      }
      frag.append(el('h3', { text: 'Awards' }), list);
    }

    if (model.seed.value) {
      frag.append(el('h3', { text: 'Seed' }));
      const row = el('div', { class: 'copy-row' });
      row.append(el('span', { class: 'result-seed', text: model.seed.value }));
      row.append(el('button', { type: 'button', class: 'ghost', text: 'Copy', onclick: () => copyText(model.seed.value, 'Seed copied') }));
      frag.append(row);
    } else {
      frag.append(el('p', { class: 'hint hint-note', text: model.seed.note }));
    }

    if (model.leaderboard && model.leaderboard.eligible) {
      frag.append(el('h3', { text: 'Enter the hall' }));
      const nameRow = el('div', { class: 'name-row' });
      const input = el('input', { type: 'text', maxlength: '16', placeholder: 'Your name', 'aria-label': 'Name for the leaderboard' });
      const submit = el('button', {
        type: 'button',
        class: 'primary',
        text: 'Submit',
        onclick: async () => {
          submit.disabled = true;
          try {
            const res = await api('/api/leaderboard', {
              method: 'POST',
              body: JSON.stringify({ sessionId: app.sessionId, name: input.value }),
            });
            const rank = res.body && res.body.rank;
            app.audio.play('leaderboard');
            // Keep the node: a DocumentFragment is emptied when the overlay appends
            // it, so querying it again here would throw after a successful submit.
            nameRow.replaceChildren(el('p', { text: rank ? `Recorded — rank #${rank} in your tier.` : 'Recorded.' }));
          } catch (err) {
            toast(err.message || 'Could not save the score');
            submit.disabled = false;
          }
        },
      });
      nameRow.append(input, submit);
      frag.append(nameRow);
    } else if (model.leaderboard && model.leaderboard.note) {
      frag.append(el('p', { class: 'hint hint-note', text: model.leaderboard.note }));
    }

    // One handler per action id. A control the model did not name cannot be rendered,
    // and an id with no handler here is a bug the gate's end-sheet check catches.
    const handlers = {
      newRun: async () => {
        closeOverlay();
        await restartGame();
      },
      playAgain: async () => {
        try {
          closeOverlay();
          await playAgain();
          startUpdates(() => renderRoomScreen());
          renderRoomScreen();
        } catch (err) {
          toast(err.message || 'Could not deal again');
        }
      },
      mainMenu: () => {
        // A room frees its chair through the server; a solo run is local. The room path
        // already treats a gone chair as gone instead of stranding the player. The sheet
        // is dismissed here as well: leaveTableAndMenu shows the setup screen but does
        // not own the overlay, so without this the sheet sat on top of the menu.
        closeOverlay();
        if (coop.code) leaveTableAndMenu();
        else abandonGame();
      },
      leaderboard: () => openLeaderboard(),
      close: () => closeOverlay(),
      history: () => {
        closeOverlay();
        toggleHistory(true);
      },
      copyLog: () => copyText(historyAsText(), 'Game log copied'),
      // A navigation cannot carry the seat header, so a plain location.href got a 401
      // page instead of the file. Fetch it, then save the blob.
      downloadLog: async () => {
        try {
          if (coop.code) {
            await downloadLog(`/api/rooms/${encodeURIComponent(coop.code)}/replay`, `regicide-${coop.code}.jsonl`, seatHeader());
          } else if (app.sessionId) {
            await downloadLog(`/api/sessions/${encodeURIComponent(app.sessionId)}/replay`, `regicide-${app.sessionId.slice(0, 8)}.jsonl`, {});
          }
        } catch (err) {
          toast(err.message || 'Could not download the log');
        }
      },
      // The viewer needs a ticket instead of the seat header: mint one, then open.
      watchReplay: async () => {
        try {
          if (coop.code) {
            await watchReplay(`/api/rooms/${encodeURIComponent(coop.code)}/replay/ticket`, seatHeader());
          } else if (app.sessionId) {
            await watchReplay(`/api/sessions/${encodeURIComponent(app.sessionId)}/replay/ticket`, {});
          }
        } catch (err) {
          toast(err.message || 'Could not open the replay');
        }
      },
    };

    const actions = el('div', { class: 'sheet-actions' });
    if (model.waiting) actions.append(el('p', { class: 'hint', text: model.waiting }));
    for (const spec of model.actions) {
      if (spec.children) {
        const details = el('details', { class: 'log-replay' });
        details.append(el('summary', { class: spec.kind === 'primary' ? 'primary' : 'ghost', text: spec.label }));
        const inner = el('div', { class: 'log-replay-actions' });
        for (const child of spec.children) {
          inner.append(el('button', {
            type: 'button',
            class: 'ghost',
            text: child.label,
            onclick: handlers[child.id],
          }));
        }
        details.append(inner);
        actions.append(details);
        continue;
      }
      actions.append(el('button', {
        type: 'button',
        class: spec.kind === 'primary' ? 'primary' : 'ghost',
        text: spec.label,
        onclick: handlers[spec.id],
      }));
    }
    frag.append(actions);
    return frag;
  });
}

// ---------------------------------------------------------------- card helper
// Cards that carry no printed rule text (the Pokémon faces, or any future theme)
// explain themselves on hover and on keyboard focus.
let tipEl = null;
let tipCard = null;

function showTip(card, anchor) {
  // The helper is a hover affordance. A touch tap focuses the card, which used to
  // raise this window on top of the peek/zoom that already shows the same text.
  if (typeof matchMedia === 'function'
    && !matchMedia('(hover: hover) and (pointer: fine)').matches) return;
  if (!tipEl) tipEl = document.getElementById('card-tip');
  const info = textFor(card.id);
  tipEl.replaceChildren();
  tipEl.append(
    el('span', { class: 'tip-title', text: info ? info.title : card.label || card.id }),
    el('span', { class: 'tip-value', text: info ? info.value : `value ${card.value}` }),
  );
  if (info && info.power) tipEl.append(el('span', { class: 'tip-power', text: info.power }));
  if (info && info.note) tipEl.append(el('span', { class: 'tip-note', text: info.note }));
  tipEl.classList.add('is-visible');
  tipEl.setAttribute('aria-hidden', 'false');
  tipCard = card;
  positionTip(anchor);
}

function positionTip(anchor) {
  if (!tipEl || !tipCard) return;
  const rect = anchor.getBoundingClientRect();
  const tipRect = tipEl.getBoundingClientRect();
  const gap = 10;
  let left = rect.left + rect.width / 2 - tipRect.width / 2;
  left = Math.max(8, Math.min(left, window.innerWidth - tipRect.width - 8));
  let top = rect.top - tipRect.height - gap;
  if (top < 8) top = rect.bottom + gap;
  tipEl.style.left = `${Math.round(left)}px`;
  tipEl.style.top = `${Math.round(top)}px`;
}

function hideTip() {
  if (!tipEl) return;
  tipEl.classList.remove('is-visible');
  tipEl.setAttribute('aria-hidden', 'true');
  tipCard = null;
}

// ---------------------------------------------------------------- misc UI

function setBusyUI(busy) {
  document.body.classList.toggle('busy', busy);
  if (busy) {
    $('#btn-confirm').disabled = true;
    return;
  }
  // The enabled/disabled decision belongs to renderActionbar. This used to force the
  // button back on after every action, leaving a highlighted dead "Select cards to pay"
  // button that did nothing when pressed (playtest 3 campaign, A16/A7).
  if (app.state) renderActionbar(app.state);
  else $('#btn-confirm').disabled = true;
}

function copyText(text, message) {
  const done = () => toast(message || 'Copied');
  if (navigator.clipboard && navigator.clipboard.writeText) {
    navigator.clipboard.writeText(text).then(done).catch(() => fallbackCopy(text, done));
  } else {
    fallbackCopy(text, done);
  }
}

function fallbackCopy(text, done) {
  const area = document.createElement('textarea');
  area.value = text;
  area.setAttribute('readonly', '');
  area.style.position = 'fixed';
  area.style.opacity = '0';
  document.body.append(area);
  area.select();
  try {
    if (!document.execCommand('copy')) {
      toast('Copy blocked — select the text and copy it');
      return;
    }
    done();
  } catch {
    toast('Copy failed — select the seed manually');
  } finally {
    area.remove();
  }
}

function attachLongPress(node, handler) {
  let timer = null;
  const start = () => {
    timer = setTimeout(() => {
      timer = null;
      handler();
    }, 500);
  };
  const cancel = () => {
    if (timer) clearTimeout(timer);
    timer = null;
  };
  node.addEventListener('pointerdown', start);
  node.addEventListener('pointerup', cancel);
  node.addEventListener('pointerleave', cancel);
  node.addEventListener('pointercancel', cancel);
}

// ---------------------------------------------------------------- wiring

function wire() {
  // Host a table: one button, then the waiting room. Joining is a five-character
  // code typed beside the seed control, so Join MP reads that field.
  const hostBtn = $('#btn-host');
  if (hostBtn) {
    hostBtn.addEventListener('click', async () => {
      try {
        // A table deals the host's chosen set when co-op offers it, so picking the
        // official deck on the menu and pressing Host does what it looks like.
        await hostRoom(COOP_SETS.has(app.prefs.theme) ? app.prefs.theme : 'official', app.prefs.jokers, app.prefs.jokersPicked);
        history.replaceState(null, '', `/?room=${coop.code}`);
        renderRoomScreen();
        // The host is a seat like any other: it uses whatever transport boot chose.
        startUpdates(() => renderRoomScreen());
      } catch (err) {
        $('#coop-hint').textContent = err.message || 'Could not open a table.';
      }
    });
  }
  const joinBtn = $('#btn-join');
  if (joinBtn) {
    const go = async () => {
      const input = $('#join-code');
      const code = (input.value || '').trim().toUpperCase();
      if (!code) {
        $('#coop-hint').textContent = 'Type the room code first — it is five characters.';
        input.focus();
        return;
      }
      if (code.length < 4) {
        $('#coop-hint').textContent = 'A room code is five characters.';
        input.focus();
        return;
      }
      history.replaceState(null, '', `/?room=${code}`);
      await enterRoom(code, new URLSearchParams({ room: code }));
    };
    joinBtn.addEventListener('click', go);
    // Enter must join the typed room, not submit the solo form the field lives in.
    $('#join-code').addEventListener('keydown', (ev) => {
      if (ev.key !== 'Enter') return;
      ev.preventDefault();
      go();
    });
  }
  const leaveBtn = $('#btn-lobby-leave');
  if (leaveBtn) {
    // Leaving the table must clear the mode, not just the screen: a stale coop.code
    // keeps routing act() at a room this tab is no longer in (F-31). It must also tell
    // the server, or the chair waits for its away timer (note 2).
    leaveBtn.addEventListener('click', () => { void leaveTableAndMenu(); });
  }

  $('#setup-form').addEventListener('submit', async (ev) => {
    ev.preventDefault();
    const seed = $('#seed-input').value.trim();
    const theme = themeInfo(app.prefs.theme).id;
    savePrefs();
    try {
      await createGame({ seed, theme });
    } catch (err) {
      $('#setup-hint').textContent = err.message || 'Could not start a game.';
    }
  });

  $('#btn-confirm').addEventListener('click', confirmSelection);
  $('#btn-deselect').addEventListener('click', () => {
    app.selected.clear();
    render();
  });
  $('#btn-history').addEventListener('click', () => toggleHistory());
  $('#history-close').addEventListener('click', () => closeHistory());
  $('#history-copy').addEventListener('click', () => copyText(historyAsText(), 'History copied'));
  $('#btn-jester').addEventListener('click', () => confirmJesterFlip());
  $('#btn-yield').addEventListener('click', () => act({ type: 'yield' }));
  // The top-bar Menu is a menu, not a destructive button: it opens the sheet that
  // now owns Restart and "main menu" (an accidental click can no longer end a run).
  $('#btn-menu').addEventListener('click', openNav);
  $('#btn-nav').addEventListener('click', openNav);
  $('#btn-rules').addEventListener('click', openRules);
  // Settings and Leaderboard live in the Menu sheet: the top bar keeps only the two
  // controls a player reaches for mid-run.

  document.addEventListener('keydown', (ev) => {
    // Enter on a button inside the peek strip or the action bar must activate that
    // button, not the pending move (the peek's controls sit next to Play).
    const insideControls = ev.target && ev.target.closest && ev.target.closest('#peek, #actionbar');
    if (ev.key === 'Enter' && !insideControls && !$('#overlay-root').firstChild && !$('#btn-confirm').disabled) confirmSelection();
    if (ev.key === 'Escape') app.selected.clear(), render();
  });
}

// ---------------------------------------------------------------- smoke test
// The UI-test seam (docs/DECISIONS.md ADR-008): drives the real handlers and
// reports machine-readable results for `chromium --dump-dom`.

function imageLoaded(img) {
  if (!img) return Promise.resolve(false);
  if (img.complete) return Promise.resolve(img.naturalWidth > 0);
  return new Promise((resolve) => {
    const done = () => resolve(img.naturalWidth > 0);
    img.addEventListener('load', done, { once: true });
    img.addEventListener('error', () => resolve(false), { once: true });
    setTimeout(done, 3000);
  });
}

async function runSmoke() {
  const checks = [];
  const check = (name, ok, detail) => checks.push({ name, ok: Boolean(ok), detail: detail ?? null });

  const startErrors = consoleErrors.slice();

  check('dom.title', document.title.length > 0, document.title);
  check('dom.stylesheet', getComputedStyle(document.body).backgroundColor !== 'rgba(0, 0, 0, 0)', null);

  // Playtest 4: the menu is one entry row with the two fields under the buttons they
  // belong to — the seed under Start Solo, the room code under Join MP — and the
  // placeholders are the labels.
  {
    const start = $('#btn-start');
    const host = $('#btn-host');
    const join = $('#btn-join');
    const seed = $('#seed-input');
    const code = $('#join-code');
    check(
      'ui.entryButtons',
      Boolean(start && host && join && seed && code)
        && start.textContent.trim() === 'Start Solo'
        && host.textContent.trim() === 'Host MP'
        && join.textContent.trim() === 'Join MP'
        && seed.placeholder === 'Seed (optional)'
        && code.placeholder === 'Room code'
        && !document.querySelector('.entry-code > span'),
      [start, host, join].map((b) => (b ? b.textContent.trim() : 'missing')).join(' / '),
    );
    if (start && seed && code) {
      const sb = start.getBoundingClientRect();
      const hb = host.getBoundingClientRect();
      const jb = join.getBoundingClientRect();
      const pb = seed.getBoundingClientRect();
      const cb = code.getBoundingClientRect();
      // Below 380 px the entry row becomes two rows, so the old centre alignment
      // cannot hold; the contract there is that nothing overlaps.
      const narrow = innerWidth <= 600;
      check(
        'ui.seedUnderStart',
        narrow || Math.abs((pb.x + pb.width / 2) - (sb.x + sb.width / 2)) <= 2,
        `start centre ${Math.round(sb.x + sb.width / 2)}, seed centre ${Math.round(pb.x + pb.width / 2)}`,
      );
      check(
        'ui.codeUnderJoin',
        narrow || Math.abs((cb.x + cb.width / 2) - (jb.x + jb.width / 2)) <= 2,
        `join centre ${Math.round(jb.x + jb.width / 2)}, code centre ${Math.round(cb.x + cb.width / 2)}`,
      );
      const hit = (a, b) => a.right > b.left + 0.5 && b.right > a.left + 0.5
        && a.bottom > b.top + 0.5 && b.bottom > a.top + 0.5;
      check(
        'ui.menuNoOverlap',
        !hit(sb, jb) && !hit(sb, hb) && !hit(jb, hb),
        `start ${Math.round(sb.right)}|${Math.round(sb.bottom)} host ${Math.round(jb.x)}|${Math.round(jb.y)} join ${Math.round(hb.x)}`,
      );
      check('ui.codeBesideSeed', cb.x >= pb.right - 1, `seed ends ${Math.round(pb.right)}, code starts ${Math.round(cb.x)}`);
      check('ui.noRoomCodeLabel', !document.body.innerText.includes('Room code\n'), 'label text present');
      check('ui.startAboveTheFold', start.getBoundingClientRect().bottom <= innerHeight, Math.round(start.getBoundingClientRect().bottom));
      const tops = [...document.querySelectorAll('#theme-choices .theme-tile')].map((t) => Math.round(t.getBoundingClientRect().top));
      check('ui.themeTilesOneRow', new Set(tops).size === 1, tops.join(','));
      // Enter joins the typed room; it must not submit the solo form.
      const sessionBefore = app.sessionId;
      code.value = '';
      const enter = new KeyboardEvent('keydown', { key: 'Enter', bubbles: true, cancelable: true });
      code.dispatchEvent(enter);
      const hint = ($('#coop-hint') || {}).textContent || '';
      check(
        'ui.entryEnterJoins',
        enter.defaultPrevented && app.sessionId === sessionBefore && /room code/i.test(hint),
        `prevented=${enter.defaultPrevented} hint="${hint}"`,
      );
      code.value = '';
      $('#coop-hint').textContent = '';
    }
  }

  try {
    const health = await api('/api/health');
    check('api.health', health.body && health.body.status === 'ok', health.body && health.body.version);
  } catch (err) {
    check('api.health', false, String(err));
  }

  try {
    const created = await createGame({ seed: 'SMOKE-SEED-1' });
    check('game.create', Boolean(created.sessionId) && created.state.hand.length === 8, `${created.state.hand.length} cards`);
    check('game.seedVisibleWhenChosen', created.state.seedVisible === true, created.state.seed);
    check('game.legalActions', (created.state.legal || []).length > 0, (created.state.legal || []).length);

    const single = (created.state.legal || []).find((a) => a.type === 'play' && (a.cardIds || []).length === 1);
    check('game.singlePlayOffered', Boolean(single), single && single.cardIds[0]);

    const handButtons = $$('#hand .card');
    check('ui.handRendered', handButtons.length === 8, handButtons.length);
    // Text is either printed on the face (hasText sets) or available to the
    // hover/zoom helper; either way every card must explain itself.
    const handCard = $('#hand .card');
    check(
      'ui.cardHasText',
      Boolean(handCard) && (themeInfo(app.prefs.theme).hasText
        ? Boolean(handCard.querySelector('.card-title'))
        : Boolean(textFor(handCard.dataset.card))),
      themeInfo(app.prefs.theme).id,
    );

    if (single && handButtons.length) {
      const before = app.state.damage;
      const btn = handButtons.find((b) => b.dataset.card === single.cardIds[0]);
      btn.click();
      const reselected = $(`#hand .card[data-card="${single.cardIds[0]}"]`);
      check(
        'ui.selectionMarksCard',
        Boolean(reselected) && reselected.getAttribute('aria-selected') === 'true',
        reselected ? reselected.getAttribute('aria-selected') : 'no card',
      );
      check('ui.confirmEnabled', $('#btn-confirm').disabled === false, $('#btn-confirm').textContent);
      $('#btn-confirm').click();
      await waitFor(() => app.state.damage !== before || app.state.phase !== 'step1', 4000);
      check('game.damageApplied', app.state.damage !== before || app.state.phase !== 'step1', `${before} -> ${app.state.damage}`);
      // Both boxes count down from the enemy's own total, not up from zero.
      const hpLeft = Math.max(0, app.state.enemyHealth - app.state.damage);
      check('ui.healthCountsDown', $('#enemy-health').textContent === String(hpLeft), `${$('#enemy-health').textContent} vs ${hpLeft}`);
      check('ui.healthShowsTotal', $('#enemy-health-total').textContent === String(app.state.enemyHealth), $('#enemy-health-total').textContent);
      check('ui.attackShowsBase', $('#enemy-attack-total').textContent === String(app.state.enemy.attack), $('#enemy-attack-total').textContent);
      check(
        'ui.attackCountsDown',
        Number($('#enemy-incoming').textContent) <= Number($('#enemy-attack-total').textContent),
        `${$('#enemy-incoming').textContent} / ${$('#enemy-attack-total').textContent}`,
      );
      // The busy release must not re-enable a button the renderer disabled: after a
      // play the table is in payment with nothing selected, and the old release left a
      // live "Select cards to pay N" button that did nothing (campaign finding).
      check(
        'ui.primaryNotLeftLive',
        $('#btn-confirm').disabled === true,
        `phase=${app.state.phase} disabled=${$('#btn-confirm').disabled} text="${$('#btn-confirm').textContent}"`,
      );
      setBusyUI(false);
      check(
        'ui.primaryNotRevivedByBusyRelease',
        $('#btn-confirm').disabled === true,
        `disabled=${$('#btn-confirm').disabled} text="${$('#btn-confirm').textContent}"`,
      );
    }

    // The solo board is a grid with named areas. The co-op wrappers exist in the same
    // DOM for the room layout, and if they ever become boxes in solo the areas stop
    // matching and the whole board collapses to shrink-to-fit — which is exactly what
    // happened once and shipped. These three checks would have caught it.
    {
      const cloth = getComputedStyle($('#cloth'));
      const piles = getComputedStyle($('.zone-piles'));
      const board = getComputedStyle($('#board'));
      check('solo.wrappersDissolve', cloth.display === 'contents', cloth.display);
      check('solo.pilesKeepTheirArea', piles.display !== 'contents' && piles.gridArea === 'piles', `${piles.display}/${piles.gridArea}`);
      // The D-52 collapse left the template string intact and broke the CHILDREN's
      // placement, so counting auto-placed children is the property that actually fails.
      const autoPlaced = [...$('#board').children].filter((el) => {
        const c = getComputedStyle(el);
        if (c.position === 'absolute' || c.position === 'fixed' || c.display === 'contents' || c.display === 'none') return false;
        // An empty child cannot distort the grid: only one with area is worth
        // counting.
        const r = el.getBoundingClientRect();
        if (r.width === 0 || r.height === 0) return false;
        return c.gridRowStart === 'auto' || c.gridColumnStart === 'auto';
      });
      check('solo.boardKeepsItsAreas', autoPlaced.length === 0, `${autoPlaced.length} auto-placed: ${autoPlaced.map((e) => e.id || e.className).join(',')}`);
      // At the smoke's first viewport the hand is 8 cards on a 390 px screen, so the
      // cards are ~44 px: the check is that they are plausible, not a fixed size.
      const cardWidth = $('#hand .card') ? $('#hand .card').getBoundingClientRect().width : 0;
      check('solo.handSized', cardWidth >= 36 && cardWidth <= 130, Math.round(cardWidth));
    }

    // Resume: a fresh GET must return the same hand.
    const resumed = await api(`/api/sessions/${app.sessionId}`);
    check('game.resume', resumed.body && resumed.body.state.hand.length === app.state.hand.length, null);

    // Every registered card set must render and its art must load.
    check('ui.themeRegistry', themes().length >= 4, themes().map((t) => t.id).join(','));
    check('ui.themeChoices', $$('#theme-choices .theme-tile').length === themes().length, $$('#theme-choices .theme-tile').length);
    check(
      'ui.themeTilesPick',
      (() => {
        const tiles = $$('#theme-choices .theme-tile');
        const target = tiles.find((t) => t.dataset.themeId !== app.prefs.theme) || tiles[0];
        target.click();
        const picked = document.querySelector(`#theme-choices .theme-tile[data-theme-id="${target.dataset.themeId}"]`);
        return picked.getAttribute('aria-pressed') === 'true' && app.prefs.theme === target.dataset.themeId;
      })(),
      app.prefs.theme,
    );
    for (const t of themes()) {
      app.prefs.theme = t.id;
      render();
      const card = $('#hand .card');
      const img = card && card.querySelector('img');
      const ok = Boolean(img) && img.src.includes(`/img/${t.id}/`) && (await imageLoaded(img));
      check(`ui.themeArt.${t.id}`, ok, img ? img.src : 'no card');
    }
    app.prefs.theme = 'official';
    render();

    // Piles: the tavern shows backs, the count sits on a chip, discards are visible.
    check('ui.tavernBack', Boolean($('#tavern-art .card-back img')), $('#tavern-art img')?.src || 'none');
    check('ui.chipCount', $('#tavern-count').textContent === String(app.state.tavernCount), $('#tavern-count').textContent);
    check('ui.discardPile', Boolean($('#discard-art')), $$('#discard-art .card').length);
    check('ui.jesterTray', $$('#jester-tray .card').length === 2, $$('#jester-tray .card').length);
    check('ui.cardBacks', backs().length >= 4, backs().map((b) => b.id).join(','));

    // Deselect button: enabled only while something is selected, and it deselects.
    const firstSel = $('#hand .card');
    if (firstSel) {
      firstSel.click();
      check('ui.deselectEnabled', $('#btn-deselect').disabled === false, $('#btn-deselect').textContent);
      $('#btn-deselect').click();
      check('ui.deselectClears', $('#btn-deselect').disabled === true && app.selected.size === 0, null);
    }

    // History drawer: opens over the board and lists the turn-by-turn log.
    $('#btn-history').click();
    const drawer = $('#history-drawer');
    check('ui.historyOpens', drawer.hidden === false, null);
    check('ui.historyHasTurns', /Turn 1/.test(drawer.textContent), drawer.textContent.slice(0, 60));
    check('ui.historyHasPlay', Boolean(drawer.querySelector('.ev-play')), drawer.querySelectorAll('.ev-play').length);
    check('ui.historyCopy', historyAsText().includes('Turn 1'), null);
    check(
      'ui.historyCopyFillsCards',
      !/\{cards\}/.test(historyAsText()),
      historyAsText().slice(0, 120),
    );
    const log = $('#history-list');
    check(
      'ui.historyLive',
      log.getAttribute('role') === 'log' && log.getAttribute('aria-live') === 'polite',
      `${log.getAttribute('role')}/${log.getAttribute('aria-live')}`,
    );
    $('#history-close').click();
    check('ui.historyCloses', $('#history-drawer').hidden === true, null);

    // Action ids must work outside secure contexts (plain-http LAN installs).
    const idA = newActionId();
    const idB = newActionId();
    check('game.actionIdOutsideSecureContext', Boolean(idA) && Boolean(idB) && idA !== idB, `${idA}`);

    // Real card art is used for the active set.
    const firstCard = $('#hand .card');
    const setDir = themeInfo(app.prefs.theme).dir;
    check(
      'ui.setArt',
      Boolean(firstCard) && Boolean(firstCard.querySelector('img')) &&
        firstCard.querySelector('img').src.includes(setDir),
      firstCard ? firstCard.querySelector('img')?.src : 'no card',
    );

    // In game on a phone the top bar is just the crown and a small Menu: the wordmark
    // is menu chrome, and the bar's height is board space.
    if (innerWidth <= 559) {
      const bar = document.querySelector('.topbar').getBoundingClientRect();
      const menuBtn = $('#btn-nav').getBoundingClientRect();
      check(
        'ui.topbarCompactMobile',
        bar.height <= 32 && menuBtn.height <= 30 && getComputedStyle($('.brand-text')).display === 'none',
        `bar ${Math.round(bar.height)}px, menu ${Math.round(menuBtn.height)}px`,
      );
    }

    // Settings: a card-back dropdown and a face+back preview.
    openSettings();
    check('ui.settingsBackSelect', Boolean($('#settings-back')) && $('#settings-back').options.length >= 4, $('#settings-back') ? $('#settings-back').options.length : 0);
    check('ui.settingsPreview', Boolean($('#settings-sheet .preview img')), $$('#settings-sheet .preview img').length);
    closeOverlay();
    check('ui.menuPreview', Boolean($('#menu-preview .preview img')), $$('#menu-preview .preview img').length);

    // Jester tap asks for confirmation instead of spending the card.
    const jesterBtn = $('#jester-tray button.is-available');
    if (jesterBtn) {
      const jesterBefore = app.state.jestersUsed;
      jesterBtn.click();
      check('ui.jesterConfirmShown', Boolean($('#zoom-sheet')) && $('#zoom-sheet').textContent.includes('Flip Jester'), null);
      check('ui.jesterNotSpentYet', app.state.jestersUsed === jesterBefore, app.state.jestersUsed);
      closeOverlay();
    }

    // A tap selects and must not open a sheet (the zoom is long-press/right-click).
    if (!$('#btn-deselect').disabled) $('#btn-deselect').click();
    const tapCard = $('#hand .card');
    if (tapCard) {
      tapCard.click();
      check('ui.tapSelectsNoSheet', $('#overlay-root').childElementCount === 0 && app.selected.size === 1, app.selected.size);
      $('#btn-deselect').click();
    }

    // Choosing a card set picks the back that suits it.
    openSettings();
    const pokeTile = $('#settings-sheet .theme-tile[data-theme-id="pokemon"]');
    if (pokeTile) {
      pokeTile.click();
      check('ui.themePicksBack', $('#settings-back').value === themeInfo('pokemon').defaultBack, $('#settings-back') ? $('#settings-back').value : 'missing');
    }
    closeOverlay();

    // The action-bar Jester asks before spending the card.
    if ($('#btn-jester') && !$('#btn-jester').hidden) {
      const used = app.state.jestersUsed;
      $('#btn-jester').click();
      check('ui.jesterBarConfirms', Boolean($('#zoom-sheet')) && app.state.jestersUsed === used, null);
      closeOverlay();
    }

    // We never print our own index on a face: every deck already prints one, and a
    // second one lands on top of it.
    check(
      'ui.noPrintedOverlays',
      $$('#hand .card').every((c) => !c.querySelector('.card-index')),
      $$('#hand .card').length,
    );
    // The header names the running build, and the top bar is deliberately lean:
    // History and Rules only.
    check('ui.version', /^v\d+\.\d+\.\d+$/.test(($('#app-version') || {}).textContent || ''), ($('#app-version') || {}).textContent);
    // The corner names the build, not just the mode: this is the multi build.
    const brand = document.querySelector('.brand-text small');
    const brandText = brand ? brand.textContent : '';
    check('ui.modeLabelMulti', /^multi\s/i.test(brandText) && /v\d+\.\d+\.\d+/.test(brandText), brandText);
    check(
      'ui.topbarLean',
      ['btn-history', 'btn-rules'].every((id) => Boolean($(`#${id}`)))
        && ['btn-settings', 'btn-leaderboard'].every((id) => !$(`#${id}`)),
      null,
    );

    // The Jester face is a catalogue, not a per-set file: pick one and both
    // Jesters on the board show it.
    {
      const board = app.state || coop.state;
      const before = board.jokers;
      board.jokers = ['shadow', 'shadow'];
      render();
      const faces = $$('#jester-tray .card img').map((i) => i.getAttribute('src'));
      check(
        'ui.jesterFacePicked',
        faces.length === 2 && faces.every((src) => src === '/img/jokers/shadow.webp'),
        faces.join(' '),
      );
      // Two different faces on the two Jesters.
      board.jokers = ['shadow', 'banner'];
      render();
      const pair = $$('#jester-tray .card img').map((i) => i.getAttribute('src'));
      check(
        'ui.jesterTwoFaces',
        pair.length === 2 && pair[0] === '/img/jokers/shadow.webp' && pair[1] === '/img/jokers/banner.webp',
        pair.join(' '),
      );
      board.jokers = before;
      render();
    }

    // Step 4 takes any number of cards: two small ones stage as one payment.
    {
      const s4 = app.state;
      const inPay = s4.phase === 'step4_pre' || s4.phase === 'step4_discard';
      if (inPay) {
        app.selected.clear();
        const owed = paymentLeft(s4);
        const small = [...(s4.hand || [])].sort((a, b) => a.value - b.value);
        const staged = [];
        let sum = 0;
        for (const c of small) {
          if (staged.length < 2 && sum + c.value < owed) {
            staged.push(c.id);
            sum += c.value;
          }
        }
        for (const id of staged) app.selected.add(id);
        const batch = matchPay(s4, s4.legal || []);
        check(
          'ui.payMultiple',
          staged.length === 2 && Boolean(batch) && batch.cardIds.length === 2,
          `${staged.length} cards, ${sum}/${owed}`,
        );
        // A card that already covers the attack ends the payment: adding another
        // after it is refused rather than silently ignored.
        const cover = [...(s4.hand || [])].find((c) => c.value >= owed);
        const extra = [...(s4.hand || [])].find((c) => c.id !== (cover && cover.id));
        if (cover && extra) {
          app.selected.clear();
          app.selected.add(cover.id);
          toggleSelection(extra);
          check('ui.payRefusesOvershoot', app.selected.size === 1 && app.selected.has(cover.id), app.selected.size);
        }
        app.selected.clear();
        render();
      }
    }

    // The zoomed Jester must be the same face as the tray's, and the rules belong
    // under the card, not printed over the art.
    {
      const board = app.state || coop.state;
      const before = board.jokers;
      board.jokers = ['shadow', 'shadow'];
      render();
      const tray = $('#jester-tray button.is-available');
      if (tray) {
        tray.click();
        const zoomed = $('#zoom-sheet .card img');
        const traySrc = $('#jester-tray .card img');
        check(
          'ui.jesterZoomMatches',
          Boolean(zoomed) && Boolean(traySrc) && zoomed.getAttribute('src') === traySrc.getAttribute('src'),
          zoomed && zoomed.getAttribute('src'),
        );
        check('ui.jesterZoomNoOverlay', !$('#zoom-sheet .card .card-body'), null);
        closeOverlay();
      }
      board.jokers = before;
      render();
    }

    // Settings offers the whole catalogue, with the current pick pressed.
    openSettings();
    const jokerTiles = $$('#settings-sheet .joker-tile');
    const jokerPressed = $$('#settings-sheet .joker-tile[aria-pressed="true"]');
    check('ui.jokerCatalogue', jokerTiles.length === jokers().length && jokerTiles.length >= 5, `${jokerTiles.length} faces`);
    check('ui.jokerPressed', jokerPressed.length >= 1 && jokerPressed.length <= 2, jokerPressed.length);
    if (jokerTiles.length >= 2) {
      // Pick a different face for each slot; the two slots must differ.
      jokerTiles[0].click();
      jokerTiles[1].click();
      const slots = $$('#settings-sheet .joker-slot-label').map((n) => n.textContent);
      check('ui.jokerTwoSlots', slots.length === 2 && slots[0] !== slots[1], slots.join(' / '));
      const previews = $$('#settings-sheet .preview .preview-item img').map((i) => i.getAttribute('src'));
      const jokerShots = previews.filter((src) => src && src.startsWith('/img/jokers/'));
      check('ui.jokerPreview', previews.length === 4 && jokerShots.length === 2, previews.join(' '));
    }
    closeOverlay();

    // A phone is warned when the chosen art is too detailed to read there.
    const themeBefore = app.prefs.theme;
    app.prefs.theme = 'pokemon';
    renderThemeNote();
    const phone = window.matchMedia('(max-width: 559px)').matches;
    check('ui.themeNote', $('#theme-note').hidden === !phone, phone ? 'shown' : 'hidden on a desk');
    app.prefs.theme = themeBefore;
    renderThemeNote();

    // On a phone the selected cards appear readable above the play button.
    if (window.matchMedia('(max-width: 559px)').matches) {
      if (!$('#btn-deselect').disabled) $('#btn-deselect').click();
      const first = $('#hand .card');
      if (first) first.click();
      const peeked = $$('#peek .peek-card').length;
      check('ui.peekStrip', $('#peek').hidden === false && peeked === app.selected.size, `${peeked}/${app.selected.size}`);
      const remove = $('#peek .pick-btn');
      if (remove) remove.click();
      check('ui.peekDeselects', app.selected.size === 0 && $('#peek').hidden === true, app.selected.size);
    }

    // Sheets: credits (attribution) and rules (how to play).
    openCredits();
    check(
      'ui.credits',
      Boolean($('#credits-sheet'))
        && $('#credits-sheet').textContent.includes('Badgers from Mars')
        && /Andy Richdale/.test($('#credits-sheet').textContent)
        && /D\.J\. Phillips/.test($('#credits-sheet').textContent),
      null,
    );
    closeOverlay();
    $('#btn-rules').click();
    check(
      'ui.rulesSheet',
      Boolean($('#rules-sheet')) && $('#rules-sheet').textContent.includes('Suit powers'),
      null,
    );
    const rulesVideo = $('#rules-sheet a[href*="youtube.com/watch?v=gfVkRaT9ZkY"]');
    check(
      'ui.rulesVideo',
      Boolean(rulesVideo) && rulesVideo.target === '_blank' && /noopener/.test(rulesVideo.rel),
      rulesVideo ? rulesVideo.href : 'missing video link',
    );
    // One heading for both modes: the body says solo/co-op where the rule differs.
    const rulesHeading = $('#rules-sheet h2');
    check(
      'ui.rulesHeading',
      Boolean(rulesHeading) && rulesHeading.textContent.trim() === 'How to play Regicide',
      rulesHeading ? rulesHeading.textContent.trim() : 'missing heading',
    );
    closeOverlay();

    // Leaderboard shape.
    const board = await api('/api/leaderboard');
    check('api.leaderboard', board.body && Array.isArray(board.body.gold), null);

    // Chaos mode: shuffled royals, still deterministic per seed.
    const chaosRun = await createGame({ seed: 'SMOKE-CHAOS-1', rules: { ...app.prefs.rules, chaos: true } });
    check('game.chaosFlag', chaosRun.state.rules.chaos === true, null);
    check('ui.chaosBadge', $('#chaos-badge').hidden === false, null);
    const firstEnemyRank = chaosRun.state.enemy ? chaosRun.state.enemy.rank : 0;
    check('game.chaosMixedCastle', [11, 12, 13].includes(firstEnemyRank), String(firstEnemyRank));
    const chaosAgain = await createGame({ seed: 'SMOKE-CHAOS-1', rules: { ...app.prefs.rules, chaos: true } });
    check('game.chaosReproducible', chaosAgain.state.enemy && chaosRun.state.enemy &&
      chaosAgain.state.enemy.id === chaosRun.state.enemy.id, chaosRun.state.enemy && chaosRun.state.enemy.id);

    // Jester tray: a flipped Jester turns face down and the hand refills.
    const jesterRun = await createGame({ seed: 'SMOKE-JESTER-1' });
    const before = jesterRun.state.hand.length;
    if ($('#btn-jester') && $('#btn-jester').hidden === false) {
      // The button opens the confirmation sheet; the sheet's action flips.
      $('#btn-jester').click();
      const flip = [...$$('#zoom-sheet button')].find((b) => b.textContent.startsWith('Flip Jester'));
      check('game.jesterConfirmBeforeSpend', Boolean(flip) && app.state.jestersUsed === 0, app.state.jestersUsed);
      if (flip) flip.click();
      await waitFor(() => app.state.jestersUsed === 1, 4000);
      check('game.jesterFlipped', app.state.jestersUsed === 1, app.state.jestersUsed);
      check('ui.jesterSpent', $$('#jester-tray .card.is-spent').length === 1, $$('#jester-tray .card.is-spent').length);
      check('game.jesterRefilled', app.state.hand.length >= Math.min(before, 1), `${before} -> ${app.state.hand.length}`);
    }

    // A royal defeat and the next royal's arrival are the table's news, solo
    // included. The panels are asserted synchronously: the harness runs under a
    // virtual clock, so an await can fast-forward through a whole reveal.
    await createGame({ seed: 'SMOKE-ROYAL-1' });
    animateEvents([{ type: 'defeat', cards: ['JS'], amount: 20, note: 'exact' }]);
    const defeatPanel = document.querySelector('#table-reveal.is-defeat');
    check(
      'solo.royalDefeatReveal',
      Boolean(defeatPanel) && /Jack of Spades is Defeated/.test(defeatPanel.textContent)
        && Boolean(defeatPanel.querySelector('img.reveal-card[src*="/js."]')),
      defeatPanel ? defeatPanel.textContent.trim() : 'no defeat panel',
    );
    // The panel is aria-hidden; a persistent live region carries the sentence.
    check(
      'solo.royalRevealAnnounced',
      /Jack of Spades is defeated/.test(($('#table-announcer') || {}).textContent || ''),
      ($('#table-announcer') || {}).textContent || 'no announcement',
    );
    await waitFor(() => !document.querySelector('#table-reveal'), 6000);
    animateEvents([{ type: 'enemy_reveal', cards: ['JH'], seat: 0 }]);
    const enterPanel = document.querySelector('#table-reveal.is-enter');
    check(
      'solo.royalEntersReveal',
      Boolean(enterPanel) && /Jack of Hearts enters the fight/.test(enterPanel.textContent)
        && Boolean(enterPanel.querySelector('img.reveal-card[src*="/jh."]')),
      enterPanel ? enterPanel.textContent.trim() : 'no enter panel',
    );
    await waitFor(() => !document.querySelector('#table-reveal'), 6000);
    check('solo.royalRevealClears', !document.querySelector('#table-reveal'), 'overlay gone');

    // Random-run seed must stay hidden.
    const randomRun = await createGame({ seed: '' });
    check('game.randomSeedHidden', randomRun.state.seedVisible === false && !randomRun.state.seed, null);
    // The board must not show the seed at all: it lives on the end sheet.
    check('ui.seedHiddenOnBoard', !document.querySelector('#seed-chip'), null);
  } catch (err) {
    check('smoke.exception', false, String(err && err.stack ? err.stack : err).slice(0, 400));
  }

  const external = performance
    .getEntriesByType('resource')
    .map((e) => e.name)
    .filter((name) => {
      try {
        return new URL(name, location.href).origin !== location.origin;
      } catch {
        return false;
      }
    });
  check('perf.noExternalRequests', external.length === 0, external.slice(0, 3).join(','));

  const newErrors = consoleErrors.slice(startErrors.length);
  check('console.noErrors', newErrors.length === 0, newErrors.slice(0, 3).join(' | '));

  return finishSmoke(checks);
}

// finishSmoke publishes one runner's result to #result and to the title. The solo,
// seat and co-op runners each carried a copy of this block; the co-op field is the
// one the gate reads (`"pass":true`).
function finishSmoke(checks) {
  const pass = checks.every((c) => c.ok);
  const result = { pass, checks, viewport: { w: innerWidth, h: innerHeight } };
  const out = $('#result');
  out.hidden = false;
  out.textContent = JSON.stringify(result);
  document.title = pass ? 'SMOKE-PASS' : 'SMOKE-FAIL';
  return result;
}

// waitTurns gives a network round trip a number of event-loop turns rather than a
// number of milliseconds: the gate runs the page under a virtual clock, where a
// millisecond budget can expire before a localhost response arrives.
async function waitTurns(predicate, turns = 200) {
  for (let i = 0; i < turns; i++) {
    if (predicate()) return true;
    await new Promise((resolve) => setTimeout(resolve, 0));
  }
  return Boolean(predicate());
}

function waitFor(predicate, timeoutMs) {
  return new Promise((resolve) => {
    const started = performance.now();
    const poll = () => {
      if (predicate() || performance.now() - started > timeoutMs) resolve(null);
      else setTimeout(poll, 60);
    };
    poll();
  });
}

const consoleErrors = [];
{
  const nativeError = console.error.bind(console);
  console.error = (...args) => {
    consoleErrors.push(args.map(String).join(' '));
    nativeError(...args);
  };
}

// Read-only debug seam for manual QA and the CDP playthrough script: it exposes
// exactly what the browser already holds (the sanitised snapshot), never more.
window.__regicide = {
  state: () => app.state,
  prefs: () => app.prefs,
  selected: () => [...app.selected],
  // The room's sequence, for the reveal-pace check: it is the one number that says
  // "the table moved", and the browser test must compare it with the panel on screen.
  roomSeq: () => coop.seq,
  // Renders the end sheet with a fabricated finished state, the same seam the co-op
  // smoke uses to exercise the win copy and the awards. It is for UI measurement
  // (screenshots, geometry): the state is the caller's, and the sheet is read-only.
  // The tab's result heading is cleared first: the memo belongs to the run, and a
  // measurement call in a live tab must not decide what the real sheet says.
  endSheet: (state) => { endTitle = null; showEndSheet(state); },
};

// ---------------------------------------------------------------- co-op smoke
// The co-op seam: a real room, a real second seat, and a card played by clicking the
// board the player clicks. It exists because the whole co-op client was once reported
// playable while `act()` still posted to a solo session nobody had (F-1) — the only
// test that catches that is one that plays a card.

async function rawApi(method, path, body, headers) {
  const res = await fetch(path, {
    method,
    headers: { 'Content-Type': 'application/json', ...(headers || {}) },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  const data = await res.json().catch(() => null);
  return { status: res.status, body: data };
}

async function runCoopSmoke() {
  const checks = [];
  const check = (name, ok, detail) => checks.push({ name, ok: Boolean(ok), detail: detail ?? null });
  const startErrors = consoleErrors.slice();

  // Every request this run makes, so "no solo post in room mode" is provable rather
  // than assumed.
  const calls = [];
  const realFetch = window.fetch.bind(window);
  window.fetch = (input, init) => {
    const url = typeof input === 'string' ? input : input.url;
    // The status is recorded too: "the stale post was refused with 409" is the fact
    // this smoke is about, and reading it off a timer was flaky in Gecko.
    const rec = { url, method: ((init && init.method) || 'GET').toUpperCase(), status: null };
    calls.push(rec);
    return realFetch(input, init).then((res) => {
      rec.status = res.status;
      return res;
    });
  };

  let code = '';
  let hostToken = '';
  let guest = null;
  try {
    // 1. A host, and a second real seat with its own token.
    const hosted = await hostRoom('simple');
    code = hosted.code;
    hostToken = coop.token;
    check('coop.roomCreated', Boolean(code) && coop.seat === 0, code);

    const joined = await rawApi('POST', `/api/rooms/${code}/join`);
    guest = joined.body;
    check('coop.guestJoined', joined.status === 200 && guest && guest.seat === 1 && Boolean(guest.token), guest && guest.seat);
    check('coop.namesAssigned', Boolean(hosted.room) && (hosted.room.seats || []).every((x) => Boolean(x.name)), (hosted.room.seats || []).map((x) => x.name).join(','));
    check('coop.hostCrowned', isHost() && Boolean(coop.room && coop.room.hostSeat === 0), coop.room && coop.room.hostSeat);

    // 1b. The lobby is the host's control room: the rule switches, add-a-bot, and a way
    // to free a chair before the deal (notes 1 and 8).
    {
      // The guest joined after the host's own answer, so the room on screen is one seat
      // old: read before drawing, or the kick control has nobody to name.
      await refreshRoom();
      renderRoomScreen();
      const settings = document.querySelector('#lobby-body .lobby-settings');
      const boxes = settings ? settings.querySelectorAll('input[type="checkbox"]').length : 0;
      check('coop.lobbySettings', Boolean(settings) && boxes >= 5, `toggles=${boxes}`);
      check('coop.lobbyAddBot', Boolean(document.querySelector('#lobby-body .lobby-bot-row button')), null);
      check('coop.lobbyKick', Boolean(document.querySelector('#lobby-body .lobby-remove')), null);
      // 0.9.8: the seating cluster (seats, empty chairs, Add a bot, Start) sits above
      // Table settings, so the host's add-and-deal flow never scrolls past the
      // switches. The free chairs are drawn, and the deal button is always present —
      // disabled with its reason until the table is startable.
      const body = document.querySelector('#lobby-body');
      const botRow = body.querySelector('.lobby-bot-row');
      const actions = body.querySelector('.lobby-actions');
      const follows = (a, b) => Boolean(a && b) && (a.compareDocumentPosition(b) & Node.DOCUMENT_POSITION_FOLLOWING) !== 0;
      check('coop.lobbySeatingFirst', follows(botRow, settings) && follows(actions, settings), 'seating cluster before settings');
      const occupied = (coop.room.seats || []).length;
      const empty = document.querySelectorAll('#lobby-body .lobby-seat.is-empty').length;
      check('coop.lobbyEmptyChairs', empty === (coop.room.capacity || 4) - occupied, `${empty} empty for capacity ${coop.room.capacity}`);
      const startBtn = [...document.querySelectorAll('#lobby button')].find((b) => /Start the game/.test(b.textContent || ''));
      check(
        'coop.lobbyStartPresent',
        Boolean(startBtn) && startBtn.disabled === !coop.room.startable,
        startBtn ? `disabled=${startBtn.disabled}, startable=${coop.room.startable}` : 'missing',
      );
    }

    // 2. Start, then draw the board the way a player sees it.
    await setReady(true);
    await startGame();
    renderRoomScreen();
    // Open the real transport, whatever it is: the stream by default, the poll loop
    // with ?transport=poll. This is what the operator's tab does.
    await startUpdates(() => renderRoomScreen());
    await waitFor(() => Boolean(coop.state), 3000);
    check('coop.boardShown', $('#board').hidden === false && $('#lobby').hidden === true, null);
    // A co-op hand is dealt from the table size: two seats get seven cards each.
    const dealt = (coop.state.hand || []).length;
    check('coop.handRendered', dealt >= 5 && $$('#hand .card').length === dealt, `${$$('#hand .card').length}/${dealt}`);
    {
      // The royal's own suit has its power blocked; every hand card of that suit says so
      // with a corner mark, while remaining a legal source of damage.
      const suit = coop.state.enemy && coop.state.enemy.suit !== 'none' ? coop.state.enemy.suit : null;
      const own = (coop.state.hand || []).filter((c) => suit && c.suit === suit).length;
      const marked = $$('#hand .card.is-power-blocked').length;
      check(
        'coop.blockedPowerMarked',
        !suit || coop.state.immunityCancelled || (own === marked && marked >= 0),
        suit ? `${marked}/${own} ${suit} cards marked` : 'no suit immunity',
      );
    }
    check('coop.railShowsTable', $$('#seats .seat').length === 2, $$('#seats .seat').length);

    const legal = (coop.state && coop.state.legal) || [];
    const single = legal.find((a) => a.type === 'play' && (a.cardIds || []).length === 1);
    check('coop.legalOffered', Boolean(single), legal.length);

    // A refused play must say why instead of leaving a dead button. Run here, before
    // the smoke plays anything, so the play button is the button in question.
    {
      // Animal Companions pair with any single card, so a mixed pair containing one is
      // legal at any total: they are excluded from the "obviously illegal" set.
      const hand = (coop.state && coop.state.hand) || [];
      const byRank = new Map();
      for (const c of hand) {
        if (c.rank === 1 || c.rank === 0) continue;
        if (!byRank.has(c.rank)) byRank.set(c.rank, c);
      }
      const distinct = [...byRank.values()];
      let checked = false;
      for (let i = 0; i < distinct.length && !checked; i++) {
        for (let j = i + 1; j < distinct.length && !checked; j++) {
          if (distinct[i].value + distinct[j].value <= 10) continue;
          app.selected = new Set([distinct[i].id, distinct[j].id]);
          render();
          const btn = $('#btn-confirm');
          check(
            'coop.refusalExplainsItself',
            btn.disabled === true && /10 or less/.test(btn.textContent),
            btn.textContent,
          );
          checked = true;
        }
      }
      if (!checked) check('coop.refusalExplainsItself', true, 'no mixed pair over 10 in this deal');
      app.selected.clear();
      render();
    }


    // 3. A stale sequence is repaired from the answer, not retried. Post the one legal
    // play through the real action path: a DOM-driven attempt measured the harness's
    // own click race (Gecko on a cold profile was the one flaky check in the suite).
    const truth = coop.seq;
    coop.seq = truth - 1;
    if (single) {
      await act({ type: 'play', cardIds: single.cardIds });
      await waitFor(() => coop.seq === truth, 4000);
    }
    const stale409 = calls.some((c) => c.status === 409 && /\/actions$/.test(c.url));
    check('coop.actStale409Adopts', stale409 && coop.seq === truth, `${coop.seq}/${truth} 409=${stale409}`);
    // The next block starts from a clean selection whether or not an attempt posted.
    app.selected.clear();

    // 4. The real thing: select a card on the board and play it.
    const seqBefore = coop.seq;
    for (let attempt = 0; attempt < 4 && coop.seq === seqBefore; attempt++) {
      if (attempt > 0) await refreshRoom().catch(() => {});
      const afterStale = coop.state && coop.state.legal ? coop.state.legal.find((a) => a.type === 'play' && (a.cardIds || []).length === 1) : null;
      if (!afterStale) break;
      app.selected.clear();
      const card = document.querySelector(`#hand .card[data-card="${afterStale.cardIds[0]}"]`);
      if (!card) break;
      card.click();
      await waitFor(() => !$('#btn-confirm').disabled, 3000);
      if (!$('#btn-confirm').disabled) $('#btn-confirm').click();
      await waitFor(() => coop.seq > seqBefore, 6000);
    }
    check('coop.actSubmits', coop.seq > seqBefore, `${seqBefore} -> ${coop.seq}`);
    check(
      'coop.noCrossGamePost',
      calls.every((c) => !/\/api\/sessions/.test(c.url)) && calls.some((c) => c.method === 'POST' && /\/api\/rooms\/[^/]+\/actions$/.test(c.url)),
      calls.filter((c) => c.method === 'POST').map((c) => c.url).join(' '),
    );

    // 2b. A seat that does not hold the turn can still select and read its cards, but
    // the call to action names the actor and stays disabled: a live Play button that
    // the server refuses, plus a combo complaint about a turn that is not yours, was
    // the pair of notes 4 and 5.
    {
      const mine = coop.seat;
      const active = coop.state ? coop.state.activeSeat : -1;
      const other = ((coop.room && coop.room.seats) || []).find((x) => x.seat !== active);
      if (other) {
        coop.seat = other.seat;
        // The preceding action can still be settling; a click during that window is a
        // harness race, not a selection bug.
        await waitFor(() => !app.busy, 2000);
        app.selected.clear();
        render();
        const card = document.querySelector('#hand .card');
        if (card) card.click();
        await waitFor(() => app.selected.size === 1, 2000);
        // A frame can arrive between the seat switch and the click; the waiting copy is
        // the thing under test, so let it settle before reading it.
        await waitFor(() => {
          const b = document.querySelector('#btn-confirm');
          return Boolean(b) && /^Waiting for \S/.test(b.textContent);
        }, 2000);
        const confirm = document.querySelector('#btn-confirm');
        const text = confirm ? confirm.textContent : '';
        check('coop.waitingPlayDisabled', Boolean(confirm) && confirm.disabled === true, text);
        check('coop.waitingNamesTheActor', /^Waiting for \S/.test(text), text);
        check('coop.waitingNoComboError', !/10 or less|cannot|no legal|combo/i.test(text), text);
        check('coop.waitingSelectionStillWorks', app.selected.size === 1, app.selected.size);
        app.selected.clear();
        coop.seat = mine;
        render();
      } else {
        check('coop.waitingPlayDisabled', true, 'one-seat table');
      }
    }

    // 2d. A zoom sheet draws the room's card set, not the menu's (note 9). The room is
    // dealt with 'simple' while the menu preference is 'official', so the two disagree
    // and the sheet must follow the table.
    {
      const card = (coop.state && coop.state.hand && coop.state.hand[0]) || null;
      const face = card ? cardFace(card) : null;
      const img = face ? face.querySelector('img') : null;
      const want = `/${themeInfo(coop.state && coop.state.theme).dir.replace(/^\/+/, '')}/`;
      check('coop.zoomUsesTheRoomTheme', Boolean(img) && img.src.includes(want), img ? img.src : 'no art');
    }


    // 4b. The four things a live game exposed at turn one (F-47..F-49, F-52): an empty
    // played pile must not draw its label, the rail count must read "n/m", the royals
    // counter must not count the royal on the table, and a refused selection must say
    // why instead of leaving a dead button.
    {
      // The bug was an element marked hidden that still drew (a co-op grid rule
      // outranked [hidden]), so the assertion is the invariant, not a snapshot of one
      // moment: hidden must mean not displayed.
      const pile = $('#pile-attack');
      const pileDisplay = pile ? getComputedStyle(pile).display : 'absent';
      check(
        'coop.pileFollowsItsHiddenFlag',
        !pile || Boolean(pile.hidden) === (pileDisplay === 'none'),
        `hidden=${pile && pile.hidden} display=${pileDisplay}`,
      );
      const count = document.querySelector('#seats .seat .seat-count');
      check(
        'coop.seatCountReads',
        Boolean(count) && /^\s*\d+\s*\/\s*\d+\s*$/.test(count.textContent),
        count && count.textContent,
      );
      // The poll transport can land between a state frame and its render, so wait for
      // the banner to name the count before reading it.
      let banner = '';
      for (let i = 0; i < 20; i++) {
        banner = $('#turn-banner') ? $('#turn-banner').textContent : '';
        if (/royals cleared/.test(banner)) break;
        await new Promise((r) => setTimeout(r, 50));
      }
      const match = /(\d+)\s+of\s+12\s+royals\s+cleared/.exec(banner);
      const cleared = match ? Number(match[1]) : -1;
      // Compare against the number the board can compute from the state: 12 royals,
      // minus the ones still in the castle, minus the one on the table. The old check
      // pinned turn 1 to zero, and ">= 0 && <= 12" passed the very value the bug made.
      const enemyOnTable = Boolean(coop.state && coop.state.enemy);
      const castleLeft = coop.state && typeof coop.state.castleCount === 'number' ? coop.state.castleCount : 0;
      const want = Math.max(0, 12 - castleLeft - (enemyOnTable ? 1 : 0));
      check('coop.royalsClearedCountsOnlyCleared', cleared === want, `cleared=${cleared}, want=${want}, enemy=${enemyOnTable}`);
    }

    // The public discard pile must agree with its own badge: the sheet lists this list.
    check(
      'coop.discardListMatchesCount',
      !Array.isArray(coop.state.discard) || coop.state.discard.length === coop.state.discardCount,
      `${(coop.state.discard || []).length} vs ${coop.state.discardCount}`,
    );

    // 4c. A bot seat is labelled (T7.5). The smoke cannot make a player idle for 90 s,
    // so it feeds the renderer the projection the server would send and checks the word
    // it draws: the seat state must say "bot", not the name of a person.
    {
      const savedSeats = JSON.parse(JSON.stringify((coop.room && coop.room.seats) || []));
      if (coop.room && savedSeats.length > 1) {
        coop.room.seats[1].bot = true;
        render();
        const seatEl = document.querySelectorAll('#seats .seat')[1];
        const state = seatEl && seatEl.querySelector('.seat-state');
        check(
          'coop.botSeatLabelled',
          Boolean(state) && state.textContent === 'bot' && seatEl.classList.contains('is-bot'),
          state && state.textContent,
        );
        coop.room.seats = savedSeats;
        render();
      } else {
        check('coop.botSeatLabelled', true, 'no second seat to label');
      }
    }

    // 4d. The cursor layer is gone for good (D-85): no markup, and no client code that
    // could post to the server's inert cursor route.
    check(
      'coop.cursorLayerGone',
      !$('#dots') && !$('#felt') && typeof pointerFrame === 'undefined' && typeof postCursor === 'undefined',
      'dots and felt absent, relay not defined',
    );

    // 4f. Card sheets. The royal opens at every width (it used to be phone-only), the
    // played pile lists its cards with owner colours, and opening either leaves no
    // uncaught exception behind (a stale `void sheet;` threw on every open).
    {
      const before = consoleErrors.length;
      const royal = $('#enemy-card');
      if (royal) royal.click();
      check(
        'coop.royalInspectable',
        Boolean($('#card-list') || document.querySelector('#overlay-root .sheet')),
        royal ? royal.getAttribute('role') : 'missing',
      );
      closeOverlay();
      const pile = $('#pile-attack');
      if (pile && pile.classList.contains('is-openable')) {
        pile.click();
        const listed = $$('#card-list .card-grid .card');
        check('coop.playedSheetListsCards', listed.length > 0, listed.length);
        check('coop.playedSheetOwners', $$('#card-list .card-grid-item.has-owner').length > 0, $$('#card-list .card-grid-item.has-owner').length);
        closeOverlay();
      } else {
        check('coop.playedSheetListsCards', false, 'played pile not openable');
      }
      check('coop.cardSheetsNoThrow', consoleErrors.length === before, consoleErrors.slice(before).join(' | '));
    }

    // A watcher has no action bar: the attribute was set but display:flex kept a greyed
    // "Waiting for X" strip on screen (campaign B9).
    {
      const savedWatch = coop.watchId;
      coop.watchId = 'smoke-watch';
      render();
      const bar = $('.actionbar');
      check(
        'coop.watchHidesActionbar',
        Boolean(bar) && bar.hidden === true && getComputedStyle(bar).display === 'none',
        bar ? `${bar.hidden}/${getComputedStyle(bar).display}` : 'missing',
      );
      coop.watchId = savedWatch;
      render();
    }

    // 5. The other seat really sees it: the same sequence, its own turn.
    const guestView = await rawApi('GET', `/api/rooms/${code}`, undefined, { 'X-Regicide-Seat': `${guest.seat}:${guest.token}` });
    check(
      'coop.guestSeesSeq',
      guestView.status === 200 && guestView.body && guestView.body.seq === coop.seq,
      guestView.body && `${guestView.body.seq} vs ${coop.seq}`,
    );
    // The guest's own read is its own projection: its seat, its hand, and the same
    // sequence the table is on. Never seat 0's hand (the whole point of the seats).
    const guestState = guestView.body && guestView.body.state;
    check(
      'coop.guestSeesOwnBoard',
      Boolean(guestState) && guestState.viewerSeat >= 0 && (guestState.hand || []).length >= 1,
      guestState && `viewer ${guestState.viewerSeat}, ${(guestState.hand || []).length} cards`,
    );

    // 5a2. Card sets at a table: Pokemon is offered, and a tab may look at its own set
    // without changing the room (D-86, D-87).
    check('coop.pokemonOffered', (coop.room.themes || []).includes('pokemon'), (coop.room.themes || []).join(','));
    {
      const savedDeck = app.prefs.tableDeck;
      const savedTheme = app.prefs.theme;
      app.prefs.tableDeck = 'mine';
      app.prefs.theme = 'pokemon';
      render();
      const src = ($('#enemy-card img') && $('#enemy-card img').getAttribute('src')) || '';
      check('coop.viewDeckMine', /\/img\/pokemon\//.test(src), src);
      app.prefs.tableDeck = savedDeck;
      app.prefs.theme = savedTheme;
      render();
      const back = ($('#enemy-card img') && $('#enemy-card img').getAttribute('src')) || '';
      check('coop.viewDeckTable', !/\/img\/pokemon\//.test(back), back);
    }

    // 5a3. The phone rail is one compact line per seat: no state word, no action bubble
    // (the reveal says what happened), and a bot/away tag on the name.
    {
      const seat = $('#seats .seat');
      const stateWord = seat && seat.querySelector('.seat-state');
      const bubble = $('#seats .seat-bubble');
      const phone = innerWidth <= 600;
      check(
        'coop.railCompactOnPhone',
        !phone || (seat && Math.round(seat.getBoundingClientRect().height) <= 34),
        seat ? `${Math.round(seat.getBoundingClientRect().height)}px at ${innerWidth}px` : 'no seat',
      );
      check(
        'coop.railNoStateWordOnPhone',
        !phone || !stateWord || getComputedStyle(stateWord).display === 'none',
        stateWord ? getComputedStyle(stateWord).display : 'none',
      );
      check(
        'coop.railNoBubbleOnPhone',
        !phone || !bubble || getComputedStyle(bubble).display === 'none',
        bubble ? getComputedStyle(bubble).display : 'no bubble',
      );
    }

    // 5b. The table reveal shows every card of a play at once and clears itself. It is
    // for the other seats, so the queue is exercised directly here.
    {
      // A real play from the round above may still be on screen; the queue would then
      // show that panel, not this one. Under Gecko the wait makes the check deterministic.
      await waitFor(() => !document.querySelector('#table-reveal'), 8000);
      queueTableReveal({ kind: 'attack', seat: 1, cards: ['7S', 'AH'] });
      const overlay = $('#table-reveal');
      const shown = overlay ? overlay.querySelectorAll('.reveal-card').length : 0;
      check(
        'coop.revealAllCards',
        Boolean(overlay) && shown === 2,
        overlay ? `${shown} cards, kind ${overlay.className}` : 'no overlay',
      );
      await waitFor(() => !document.querySelector('#table-reveal'), 6000);
      check('coop.revealClearsItself', !document.querySelector('#table-reveal'), 'overlay gone');
      // The actor's own move is not revealed, and the mirror case is: this is the
      // promise note 8 makes, and deleting the guard must fail here.
      const ownCard = ((coop.state && coop.state.hand) || [])[0];
      coop.pendingReveal = { events: [{ type: 'play', seat: coop.seat, cards: ownCard ? [ownCard.id] : ['5S'] }], seq: coop.seq + 1 };
      renderRoomScreen();
      check('coop.revealSkipsTheActor', !document.querySelector('#table-reveal'), 'no panel for the mover');
      coop.pendingReveal = { events: [{ type: 'play', seat: 1, cards: ownCard ? [ownCard.id] : ['5S'] }], seq: coop.seq + 1 };
      renderRoomScreen();
      check('coop.revealShowsTheOtherSeat', Boolean(document.querySelector('#table-reveal')), 'panel for the other seat');
      coop.pendingReveal = null;
      await waitFor(() => !document.querySelector('#table-reveal'), 6000);
      // A defence is a shield, not crossed swords: the payment is its own action and
      // its reveal must use the defend branch (the payment events now carry the payer).
      queueTableReveal({ kind: 'defend', seat: 1, cards: ['5S'] });
      await waitFor(() => Boolean(document.querySelector('#table-reveal')), 2000);
      const defence = document.querySelector('#table-reveal');
      const shieldPath = defence ? defence.querySelector('.reveal-symbol svg path') : null;
      check(
        'coop.defendRevealShield',
        Boolean(defence) && defence.classList.contains('is-defend')
          && Boolean(shieldPath) && String(shieldPath.getAttribute('d')).startsWith('M12 1L3 5'),
        defence ? defence.className : 'no overlay',
      );
      await waitFor(() => !document.querySelector('#table-reveal'), 6000);

      // A royal fall and the next royal's arrival reach the whole table, the striker
      // included. Asserted synchronously, for the virtual-clock reason above; the
      // order of a defeat batch (blow, fall, arrival) is pinned by the unit test.
      coop.pendingReveal = {
        events: [
          { type: 'play', seat: 1, cards: ['10C'], amount: 20 },
          { type: 'defeat', cards: ['JS'], amount: 20, note: 'exact' },
        ],
        seq: coop.seq + 1,
      };
      renderRoomScreen();
      const blow = document.querySelector('#table-reveal.is-attack');
      check('coop.royalBlowFirst', Boolean(blow), blow ? blow.className : 'no attack panel');
      await waitFor(() => !document.querySelector('#table-reveal'), 6000);
      coop.pendingReveal = { events: [{ type: 'defeat', cards: ['JS'], amount: 20 }], seq: coop.seq + 2 };
      renderRoomScreen();
      const fall = document.querySelector('#table-reveal.is-defeat');
      check(
        'coop.royalDefeatReveal',
        Boolean(fall) && /Jack of Spades is Defeated/.test(fall.textContent)
          && Boolean(fall.querySelector('img.reveal-card[src*="/js."]')),
        fall ? fall.textContent.trim() : 'no defeat panel',
      );
      await waitFor(() => !document.querySelector('#table-reveal'), 6000);
      coop.pendingReveal = { events: [{ type: 'enemy_reveal', cards: ['JH'], seat: 1 }], seq: coop.seq + 3 };
      renderRoomScreen();
      const arrives = document.querySelector('#table-reveal.is-enter');
      check(
        'coop.royalEntersReveal',
        Boolean(arrives) && /Jack of Hearts enters the fight/.test(arrives.textContent)
          && Boolean(arrives.querySelector('img.reveal-card[src*="/jh."]')),
        arrives ? arrives.textContent.trim() : 'no enter panel',
      );
      await waitFor(() => !document.querySelector('#table-reveal'), 6000);
      check('coop.royalRevealClears', !document.querySelector('#table-reveal'), 'overlay gone');
      // The striker sees the royal news even though their own play is never shown.
      check('coop.royalSeatIsTheActor', coop.seat === 0, coop.seat);
      coop.pendingReveal = { events: [{ type: 'defeat', cards: ['KS'], amount: 40 }], seq: coop.seq + 4 };
      renderRoomScreen();
      check(
        'coop.royalNewsReachesTheActor',
        Boolean(document.querySelector('#table-reveal.is-defeat')),
        'defeat panel for the mover',
      );
      coop.pendingReveal = null;
      await waitFor(() => !document.querySelector('#table-reveal'), 6000);
    }

    // 5c. T2: a spectator appears in the menu by label, and the list agrees with the
    // count the banner shows. A spectator has no hand, so nothing but a label may
    // appear in that list.
    {
      const joinedWatch = await rawApi('POST', `/api/rooms/${code}/watch`);
      const watchId = joinedWatch.body && joinedWatch.body.watchId;
      check('coop.watcherJoined', joinedWatch.status === 200 && Boolean(watchId), joinedWatch.status);
      await refreshRoom();
      renderRoomScreen();
      openNav();
      const list = $$('#nav-sheet .nav-watcher-list li');
      const names = (coop.room && coop.room.watchNames) || [];
      const count = (coop.room && coop.room.watchers) || 0;
      const noCards = list.every((li) => !li.querySelector('img, .card'));
      check(
        'coop.watcherList',
        count >= 1 && names.length === count && list.length === count && noCards && list.every((li) => li.textContent.trim().length > 0),
        `count=${count} names=${names.join('|')} list=${list.map((li) => li.textContent).join('|')}`,
      );
      closeOverlay();
      if (watchId) {
        await rawApi('DELETE', `/api/rooms/${code}/watch`, undefined, { 'X-Regicide-Watch': watchId });
      }
      await refreshRoom();
      renderRoomScreen();
    }

    // 5d. T11: a dealt host menu removes a present player. The seat stays (the rules
    // never shrink), is marked left, and bot fill puts a bot in it.
    {
      await refreshRoom();
      renderRoomScreen();
      const guestSeat = (coop.room.seats || []).find((x) => x.seat === 1);
      openNav();
      const removeButtons = $$('#nav-sheet .nav-table-row button').filter((b) => b.textContent === 'Kick');
      const offered = removeButtons.length === 1 && guestSeat
        ? removeButtons[0].closest('.nav-table-row').textContent.includes(guestSeat.name)
        : false;
      check('coop.kickDealtPresentOffered', offered, `${removeButtons.length} remove button(s)`);
      if (offered) removeButtons[0].click();
      await waitFor(() => {
        const row = ((coop.room && coop.room.seats) || []).find((x) => x.seat === 1);
        return Boolean(row && row.left && row.bot);
      }, 4000);
      let row = ((coop.room && coop.room.seats) || []).find((x) => x.seat === 1);
      if (!row || !row.left) {
        await refreshRoom();
        renderRoomScreen();
        row = ((coop.room && coop.room.seats) || []).find((x) => x.seat === 1);
      }
      check('coop.kickDealtPresent', Boolean(row && row.left && row.bot), row ? `left=${row.left} bot=${row.bot}` : 'no seat 1');
      closeOverlay();
    }

    // 6. The transport is live, and it is the one the URL asked for.
    if (transport === 'sse') {
      check('coop.streamOpen', Boolean(coop.live) && coop.live.stopped === false, typeof coop.live);
      // The stream is open and has parsed a frame. Presence *arrival* is measured in
      // real time by the two-browser check: this harness runs under a virtual clock,
      // where a chunk can be starved, and a clock is not the thing under test.
      await waitTurns(() => Boolean(coop.live && coop.live.lastSeq > 0));
      check(
        'coop.streamFrames',
        Boolean(coop.live) && coop.live.lastSeq > 0 && coop.frames.state >= 1,
        `seq=${coop.live && coop.live.lastSeq} frames=${JSON.stringify(coop.frames)}`,
      );
      check(
        'coop.presenceOutOfState',
        !Object.prototype.hasOwnProperty.call(coop.state || {}, 'presence'),
        'presence is a separate channel',
      );
    } else {
      check('coop.pollOpen', Boolean(coop.poll) && typeof coop.poll === 'object' && coop.poll.stopped === false, typeof coop.poll);
    }
    stopUpdates();
    // And when it gives up, the board says so instead of going quiet.
    coop.status = 'offline';
    renderConnChip();
    const chip = $('#conn-chip');
    check('coop.pollOfflineChip', Boolean(chip) && chip.hidden === false && /stopped answering/.test(chip.textContent), chip && chip.textContent);
    coop.status = '';
    renderConnChip();
    check('coop.pollChipClears', Boolean(chip) && chip.hidden === true, chip && chip.hidden);

    // 7. A dead seat returns to the lobby instead of a board that cannot act.
    coop.token = 'bogus';
    await act({ type: 'yield' });
    check('coop.act401ReturnsToLobby', coop.status === 'seat_lost' && coop.token === '' && $('#lobby').hidden === false, coop.status);
    coop.token = hostToken;
    coop.seat = 0;
    // The result sheet is the table's: no solo medal, Jester cards counted, and four
    // awards from the same log. A fake finished state exercises the copy and the list.
    {
      const fake = {
        won: true, lost: false, tier: 'gold', numPlayers: 2, turnNo: 45, jestersUsed: 0,
        hand: [], seedVisible: false, rules: {},
        players: ((coop.room && coop.room.seats) || []).map((x) => ({ seat: x.seat, name: x.name })),
        log: [
          { type: 'play', seat: 0, cards: ['10S'] }, { type: 'damage', amount: 10 },
          { type: 'play', seat: 1, cards: ['J1'] }, { type: 'defeat', cards: ['JS'] },
          { type: 'draw', seat: 0, amount: 3 },
        ],
      };
      showEndSheet(fake);
      const sheet = $('#end-sheet');
      const awards = sheet ? [...sheet.querySelectorAll('.award')] : [];
      const text = sheet ? sheet.innerText : '';
      check(
        'coop.endSheetAwards',
        Boolean(sheet) && awards.length === 4
          && !/gold victory/i.test(text)
          && /One Jester played/i.test(text)
          && awards.every((a) => /Most /.test(a.textContent)),
        text.replace(/\s+/g, ' ').slice(0, 160),
      );
      // 0.9.8: the table's sheet is four controls, not seven, and one of them is the
      // way back to the menu. The log and the replay live behind one disclosure.
      const top = sheet ? [...sheet.querySelectorAll('.sheet-actions > button, .sheet-actions > details > summary')] : [];
      const labels = top.map((n) => n.textContent.trim());
      const group = sheet ? sheet.querySelector('details.log-replay') : null;
      const inside = group ? [...group.querySelectorAll('button')].map((n) => n.textContent.trim()) : [];
      check(
        'coop.endSheetCompact',
        top.length === 4 && labels[0] === 'Play again' && labels.includes('Main menu')
          && labels.includes('Log & replay') && labels.includes('Close')
          && !/Back to the table/.test(text),
        labels.join(' | '),
      );
      check(
        'coop.endSheetLogGroup',
        Boolean(group) && !group.open && inside.includes('History') && inside.includes('Copy log')
          && inside.includes('Download the log') && inside.includes('Watch the replay'),
        inside.join(' | '),
      );
      // 0.9.9 P1: a closed disclosure must be hidden in layout too. This Chromium
      // kept the closed details' buttons laid out and hit-testable — an invisible
      // History overlapped Close, invisible actions floated over the backdrop.
      const hiddenWhenClosed = group
        ? getComputedStyle(group.querySelector('.log-replay-actions')).display === 'none'
        : false;
      check('coop.endSheetClosedIsHidden', hiddenWhenClosed, hiddenWhenClosed ? 'closed group display:none' : 'closed group still laid out');
      closeOverlay();
    }
    await rawApi('DELETE', `/api/rooms/${code}`, undefined, { 'X-Regicide-Seat': `0:${hostToken}` });
  } catch (err) {
    check('coop.smokeException', false, String(err && err.stack ? err.stack : err).slice(0, 400));
    if (code && hostToken) {
      await rawApi('DELETE', `/api/rooms/${code}`, undefined, { 'X-Regicide-Seat': `0:${hostToken}` }).catch(() => null);
    }
  }

  const newErrors = consoleErrors.slice(startErrors.length);
  check('console.noErrors', newErrors.length === 0, newErrors.slice(0, 3).join(' | '));

  return finishSmoke(checks);
}

// runSeatsSmoke exercises the seat lifecycle the second playtest asked for, through
// the same client functions the buttons call: kick in the lobby, add a bot, cover a
// bot seat as the host, return to your own seat, and leave the table.
//
// It is separate from the co-op smoke because it deliberately destroys its own table:
// mixing the two would make the board checks depend on a seat that is meant to vanish.
async function runSeatsSmoke() {
  const checks = [];
  const check = (name, ok, detail) => checks.push({ name, ok: Boolean(ok), detail: detail ?? null });
  const startErrors = consoleErrors.slice();
  let code = '';
  let hostToken = '';
  let guest = null;
  try {
    // 1. A host and a second seat, both real tokens.
    const hosted = await hostRoom('simple');
    code = hosted.code;
    hostToken = coop.token;
    const joined = await rawApi('POST', `/api/rooms/${code}/join`);
    guest = joined.body;
    check('seats.roomMade', coop.seat === 0 && guest && guest.seat === 1, `${coop.seat}/${guest && guest.seat}`);
    renderRoomScreen();
    const deckButtons = [...document.querySelectorAll('#lobby .seg, #lobby button')];
    const pokemonBtn = deckButtons.find((b) => /Pokémon/.test(b.textContent || ''));
    check(
      'seats.pokemonHint',
      Boolean(pokemonBtn) && /Detailed art/.test(pokemonBtn.title || ''),
      pokemonBtn ? pokemonBtn.title : 'no Pokemon button',
    );

    // 2. The host kicks the guest's chair: the chair is gone, not just marked.
    await kickSeat(1);
    await refreshRoom();
    renderRoomScreen();
    check('seats.kickFreesTheChair', (coop.room.seats || []).length === 1, (coop.room.seats || []).length);
    const dead = await rawApi('GET', `/api/rooms/${code}`, undefined, { 'X-Regicide-Seat': `1:${guest.token}` });
    check('seats.kickedTokenIsDead', dead.status === 401, dead.status);

    // 3. Add a bot, which is a seat like any other, then deal.
    const added = await addBot();
    check('seats.addBot', added.botSeat === 1 && (added.room.seats || []).some((x) => x.seat === 1 && x.bot), added.botSeat);
    await startGame();
    check('seats.dealt', Boolean(coop.state) && coop.state.numPlayers === 2, coop.state && coop.state.numPlayers);

    // 4. Covering a bot seat keeps the host's own identity, and the menu offers the way
    //    back even though the server no longer sees this tab as the host.
    await takeSeat(1);
    check('seats.covering', coop.seat === 1 && coveringSeat(), `${coop.seat}/${coveringSeat()}`);
    openNav();
    const returnBtn = [...document.querySelectorAll('#overlay-root .sheet button')].find((b) => /Return to your own seat/.test(b.textContent || ''));
    check('seats.returnOffered', Boolean(returnBtn), returnBtn ? returnBtn.textContent : 'missing');
    if (returnBtn) {
      returnBtn.click();
      await waitFor(() => coop.seat === 0, 4000);
    }
    closeOverlay();
    await returnToOwnSeat().catch(() => null);
    check('seats.returned', coop.seat === 0 && !coveringSeat() && (coop.state ? coop.state.viewerSeat === 0 : false), coop.seat);

    // 5. Leaving a dealt table marks the seat left and hands it to a bot.
    await leaveTable();
    check('seats.left', coop.seat === -1 && coop.token === '', `${coop.seat}/${coop.token}`);
    const taken = await rawApi('POST', `/api/rooms/${code}/join`);
    check('seats.joinReplacesTheLeftSeat', taken.status === 200 && taken.body.seat === 0, taken.body && taken.body.seat);
    const room = taken.body.room || {};
    const row = (room.seats || []).find((x) => x.seat === 0) || {};
    check('seats.leftSeatIsLabelled', row.left === undefined ? true : row.left === false, `left=${row.left} bot=${row.bot}`);

    // 6. A tab that covers a seat holds two credentials. "Leave the table" must give up
    //    both: dropping the return record while the own chair stayed held left it
    //    occupied by a token no browser had.
    {
      const second = await hostRoom('simple');
      const other = await rawApi('POST', `/api/rooms/${second.code}/join`);
      await addBot();
      await startGame();
      await takeSeat(2);
      await leaveTable();
      const watcher = { 'X-Regicide-Seat': `1:${other.body.token}` };
      const seen = await rawApi('GET', `/api/rooms/${second.code}`, undefined, watcher);
      const rows = (seen.body && seen.body.room && seen.body.room.seats) || [];
      const own = rows.find((x) => x.seat === 0) || {};
      const coveredSeat = rows.find((x) => x.seat === 2) || {};
      check('seats.leaveGivesUpBothChairs', own.left === true && Boolean(coveredSeat.left || coveredSeat.bot), JSON.stringify(rows.map((x) => ({ seat: x.seat, left: x.left, bot: x.bot }))));
    }

    // 7. The menu path and a host-closed room both end on a clean menu: the lobby used
    //    to stay visible underneath, carrying its dead-room note (campaign B10/B14).
    showSetup();
    check('seats.lobbyHiddenOnMenu', $('#lobby').hidden === true && $('#setup').hidden === false, `${$('#lobby').hidden}/${$('#setup').hidden}`);
    coop.room = { cancelled: true };
    renderRoomScreen();
    check(
      'coop.closedRoomReturnsToMenu',
      $('#setup').hidden === false && $('#lobby').hidden === true && coop.code === '',
      `setup=${$('#setup').hidden} lobby=${$('#lobby').hidden} code="${coop.code}"`,
    );

    // 8. A returning tab whose chair was taken must not be seated in another chair.
    //    Refreshing with a dead stored token fell through to join(), so the tab landed
    //    in the lowest free chair with that chair's hand and no word said (playtest 3,
    //    Phase B: seat 1 came back as seat 3). It must say what happened and offer the
    //    one tap that joins, and nothing may join before that tap.
    {
      const table = await hostRoom('simple');
      const tableCode = table.code;
      const tableToken = coop.token;
      const alice = await rawApi('POST', `/api/rooms/${tableCode}/join`);
      const bob = await rawApi('POST', `/api/rooms/${tableCode}/join`);
      // The API joins moved the table on; the host's sequence has to catch up before a
      // start, or the action is refused as stale.
      await refreshRoom();
      await startGame();
      // Alice leaves and a newcomer takes her chair, so her token is dead. Bob leaves,
      // so one free (bot) chair is left — the chair a silent join would take.
      await rawApi('POST', `/api/rooms/${tableCode}/leave`, undefined, { 'X-Regicide-Seat': `1:${alice.body.token}` });
      await rawApi('POST', `/api/rooms/${tableCode}/join`);
      await rawApi('POST', `/api/rooms/${tableCode}/leave`, undefined, { 'X-Regicide-Seat': `2:${bob.body.token}` });
      // Alice's tab still holds the seat it sat in. The record is per tab (T10):
      // write this tab's key, not the old room-scoped one.
      const tab = sessionStorage.getItem('regicide.tab') || 'smoke';
      localStorage.setItem(`regicide.room.${tableCode}.${tab}`, JSON.stringify({ seat: 1, token: alice.body.token }));
      coop.code = tableCode;
      coop.seat = 1;
      coop.token = alice.body.token;
      await enterRoom(tableCode, new URLSearchParams({ room: tableCode }));
      stopUpdates();
      const lost = $('#lobby-body');
      check(
        'seats.lostSeatIsNotASilentJoin',
        coop.status === 'seat_lost' && coop.seat === -1 && coop.token === '' && coop.state === null,
        `${coop.status}/${coop.seat}/token=${coop.token}/state=${coop.state ? 'kept' : 'null'}`,
      );
      check(
        'seats.lostSeatLeavesNoSeatInTheURL',
        location.search === '',
        JSON.stringify(location.search),
      );
      check(
        'seats.lostSeatOffersAChair',
        Boolean(lost && lost.querySelector('#btn-take-chair')),
        lost ? lost.innerText.replace(/\s+/g, ' ').slice(0, 140) : 'no lobby body',
      );
      const asHost = await rawApi('GET', `/api/rooms/${tableCode}`, undefined, { 'X-Regicide-Seat': `0:${tableToken}` });
      const rows = (asHost.body && asHost.body.room && asHost.body.room.seats) || [];
      check(
        'seats.lostSeatJoinedNothing',
        rows.length === 3 && rows[2].bot === true,
        rows.map((r) => `${r.seat}:${r.bot ? 'bot' : 'human'}`).join(','),
      );
      const chair = lost && lost.querySelector('#btn-take-chair');
      if (chair) {
        chair.click();
        await waitFor(() => coop.seat === 2, 4000);
        stopUpdates();
      }
      check('seats.takeChairJoinsOnceAsked', coop.seat === 2 && Boolean(coop.token), `${coop.seat}/${Boolean(coop.token)}`);

      // A tab that was covering another chair still owns the one in its return record.
      // Losing the covered chair must offer that chair back, not drop both credentials.
      localStorage.setItem(`regicide.room.${tableCode}.return.${tab}`, JSON.stringify({ seat: 0, token: tableToken }));
      loseRoomSeat('simulated takeover of the covered chair');
      check(
        'seats.lostSeatKeepsYourOwnChair',
        Boolean($('#btn-return-seat')),
        $('#lobby-body') ? $('#lobby-body').innerText.replace(/\s+/g, ' ').slice(0, 140) : 'no lobby body',
      );
      const back = $('#btn-return-seat');
      if (back) {
        back.click();
        await waitFor(() => coop.seat === 0, 4000);
        stopUpdates();
      }
      check('seats.returnAfterLoss', coop.seat === 0 && Boolean(coop.token), `${coop.seat}/${Boolean(coop.token)}`);
      await rawApi('DELETE', `/api/rooms/${tableCode}`, undefined, { 'X-Regicide-Seat': `0:${tableToken}` }).catch(() => null);
    }
  } catch (err) {
    check('seats.exception', false, String(err && err.stack ? err.stack : err).slice(0, 400));
  }
  if (code && hostToken) {
    await rawApi('DELETE', `/api/rooms/${code}`, undefined, { 'X-Regicide-Seat': `0:${hostToken}` }).catch(() => null);
  }
  const newErrors = consoleErrors.slice(startErrors.length);
  check('console.noErrors', newErrors.length === 0, newErrors.slice(0, 3).join(' | '));
  return finishSmoke(checks);
}

// ---------------------------------------------------------------- boot

async function boot() {
  loadPrefs();
  // The theme registry decides which card sets and backs exist; load it before the
  // first paint so the setup screen and the board agree on the art.
  try {
    await loadThemes();
  } catch {
    /* the built-in fallback (official + black back) keeps the game playable */
  }
  // The header names the running build. A failure is cosmetic, so it never blocks.
  api('/api/health')
    .then((res) => {
      const version = res && res.body && res.body.version;
      const slot = $('#app-version');
      if (slot && version) slot.textContent = `v${version}`;
    })
    .catch(() => {});
  // Only correct a preference when the registry really loaded: the built-in
  // fallback has a single theme/back and would otherwise overwrite a valid choice.
  const registryLoaded = themes().length > 1 || backs().length > 1;
  if (registryLoaded) {
    if (!themes().some((t) => t.id === app.prefs.theme)) app.prefs.theme = registryDefaultTheme();
    if (!backs().some((b) => b.id === app.prefs.back)) {
      app.prefs.back = themeInfo(app.prefs.theme).defaultBack || registryDefaultBack();
    }
  }
  // A first run, a migrated single face, or a pair whose faces were retired gets a
  // real pair for the set this screen will show.
  if (registryLoaded && (
    !Array.isArray(app.prefs.jokers)
    || app.prefs.jokers.length < 2
    || !app.prefs.jokers.every((id) => jokers().some((j) => j.id === id))
  )) {
    app.prefs.jokers = jokerPairFor(app.prefs.theme);
  }
  applyPrefs();
  wire();
  // Card text and audio are progressive enhancements: a failure must never leave
  // the player staring at an empty page.
  try {
    await loadCardText();
  } catch {
    /* cards fall back to their id-based labels */
  }
  try {
    await app.audio.init();
  } catch {
    /* silence is an acceptable failure mode */
  }

  const params = new URLSearchParams(location.search);
  // The transport is a URL decision, so it is read before any screen branch returns
  // (the smoke harness returns early, and it must smoke the transport it was asked for).
  if (params.get('transport') === 'poll') transport = 'poll';
  const themeParam = params.get('theme');
  if (themeParam && themes().some((t) => t.id === themeParam)) {
    app.prefs.theme = themeParam;
    app.prefs.themeExplicit = true;
    // A shared ?theme= link should look like the tile: the deck's own back too.
    if (themeInfo(themeParam).defaultBack) app.prefs.back = themeInfo(themeParam).defaultBack;
    savePrefs();
  }
  const backParam = params.get('back');
  if (backParam && backs().some((b) => b.id === backParam)) {
    app.prefs.back = backParam;
    savePrefs();
  }
  // A spectator tab that closes releases its slot immediately (T8.5).
  window.addEventListener('pagehide', dropWatchOnExit);
  window.addEventListener('beforeunload', dropWatchOnExit);

  // One self-driving harness per entry point. A runner that throws reports a failure
  // in the same shape a runner that returns one does.
  const selfTest = { smoke: runSmoke, seats: runSeatsSmoke, coop: runCoopSmoke }[params.get('test')];
  if (selfTest) {
    selfTest().catch((err) => {
      const out = $('#result');
      out.hidden = false;
      out.textContent = JSON.stringify({ pass: false, error: String(err) });
      document.title = 'SMOKE-FAIL';
    });
    return;
  }

  // Co-op entry: /?room=CODE (seat, or spectator), /?room=CODE&watch=1,
  // /?room=CODE&seat=N&rejoin=KEY for a reconnect link. Handled before the solo
  // resume path so a stale solo session never wins over an explicit room link.
  const roomParam = params.get('room');
  if (roomParam) {
    await enterRoom(roomParam, params);
    return;
  }

  const urlSession = params.get('session');
  let stored = null;
  try {
    stored = localStorage.getItem(SESSION_KEY);
  } catch {
    /* storage blocked: the URL session still works, otherwise start fresh */
  }
  const candidate = urlSession || stored;
  if (candidate) {
    try {
      if (await resumeGame(candidate)) return;
    } catch {
      /* fall through to a fresh setup screen */
    }
  }

  // A shared ?seed= link must arrive with the seed panel already open.
  const seedParam = params.get('seed');
  if (seedParam) {
    $('#seed-input').value = seedParam;
    showSetup({ keepSeed: true });
    $('#setup-hint').textContent = 'Seed link loaded — this run replays that deal and is practice only.';
  } else {
    showSetup();
  }
}

boot();

// whyNotPlayable names the rule that stops a selection from being played, in the
// player's words. The server is still the only judge; this only explains a refusal
// the client can already predict, so a dead button is never silent.
//
// The card view carries id/rank/suit/value/label and nothing else, so the ranks here
// are the wire's numeric ranks (1 = Ace, 11 = Jack, 12 = Queen, 13 = King, 15 = Jester).
function whyNotPlayable(s, ids) {
  const cards = ids.map((id) => (s.hand || []).find((c) => c.id === id)).filter(Boolean);
  if (cards.length !== ids.length) return 'Those cards are not in your hand';
  if (ids.length > 4) return 'A play is at most 4 cards';
  if (cards.some((c) => c.rank === 0 || c.rank === 15 || /^j[12]$/i.test(String(c.id)))) {
    return 'A Jester is played on its own';
  }
  const companions = cards.filter((c) => c.rank === 1);
  if (companions.length > 0) {
    if (cards.length === 2) return 'Play the companion with one other card';
    return 'An Animal Companion pairs with exactly one card';
  }
  if (cards.length === 1) return 'That card cannot be played right now';
  const ranks = new Set(cards.map((c) => c.rank));
  const total = cards.reduce((n, c) => n + (c.value || 0), 0);
  if (ranks.size > 1) {
    return total > 10
      ? `These cards total ${total}: a mixed play must be 10 or less`
      : 'A mixed play must be a single card or a combo';
  }
  return `A combo of ${cards.length} must total 10 or less (this is ${total})`;
}
