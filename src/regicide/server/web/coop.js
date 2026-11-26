// Co-op client: rooms, the seat rail, and presence.
//
// The server owns every rule and the whole room book; this module owns exactly two
// things — the calls, and how a table is drawn. Solo play never touches this file.
//
import { artUrl, jesterArt } from './themes.js';
import { SEAT_COLOURS } from './format.js';

// Docs: docs/MP-PLAN.md (§4 presence, §5 board, §6 discovery), D-38 names,
// D-43 spectators, D-44 hand size, D-45 view-as-a-player.

// The host marker is the brand crown, so the table and the header agree.
export const CROWN_PATH = '/img/brand/crown.svg';
// live.js is loaded on demand: the smoke harness and the node tests never open a
// stream, and a module that only the stream needs should not be on the import path.
const liveModule = {
  createLive: null,
};

export async function loadLive() {
  if (!liveModule.createLive) {
    const mod = await import('./live.js');
    liveModule.createLive = mod.createLive;
  }
  return liveModule.createLive;
}

const SEAT_STORAGE = 'regicide.room.';   // + CODE + '.' + tab -> {seat, token}
const RETURN_STORAGE = 'regicide.room.'; // + CODE + '.return.' + tab -> {seat, token}
const TAB_STORAGE = 'regicide.tab';      // per-tab id, so two tabs are two chairs

// tabId names this tab inside the shared localStorage. A room-scoped seat record let
// a second tab of the same profile adopt (and then overwrite) the chair the first tab
// was sitting in: two tabs had one identity (T10). sessionStorage is per tab and
// survives a reload, which is exactly the lifetime a seat record needs.
function tabId() {
  try {
    let id = sessionStorage.getItem(TAB_STORAGE);
    if (!id) {
      id = Math.random().toString(36).slice(2) + Date.now().toString(36);
      sessionStorage.setItem(TAB_STORAGE, id);
    }
    return id;
  } catch {
    // No session storage (private mode, a test harness): one chair per profile is
    // still better than a broken join.
    return 'shared';
  }
}

function seatKey(code) {
  return SEAT_STORAGE + code + '.' + tabId();
}

function returnKey(code) {
  return RETURN_STORAGE + code + '.return.' + tabId();
}

// ---------- state ----------------------------------------------------------

export const coop = {
  code: '',
  seat: -1,          // -1 when watching or before joining
  token: '',
  watchId: '',
  watchingAs: -1,    // the seat a spectator is looking through, or -1
  room: null,
  state: null,
  lobby: true,
  error: '',
  status: '',
  seq: 0,           // the last sequence number the server handed this client
  poll: 0,          // the live poller, or 0 when idle
  live: null,       // the stream, when one is open
  frames: { state: 0, presence: 0 },
  presence: null,   // the last presence frame, never part of app.state
  eventSeq: 0,      // the last sequence whose events were offered to the table reveal
  lastEvents: [],   // the previous state.events window, to cut the new suffix
  pendingReveal: null, // {events, seq} for the next render to show, or null
};

// adoptAndReturn applies a server answer and hands it back. adopt() itself returns
// nothing, so six helpers used to resolve to undefined and any caller expecting the
// response got nothing.
function adoptAndReturn(data) {
  adopt(data);
  return data;
}

// ---------- plumbing ------------------------------------------------------

export function seatHeader() {
  if (coop.watchId) return { 'X-Regicide-Watch': coop.watchId };
  if (coop.token) return { 'X-Regicide-Seat': `${coop.seat}:${coop.token}` };
  return {};
}

// roomErrorText turns the server's codes into something a player can act on. The raw
// codes used to reach the UI through err.message: "not_host" as a toast is not an
// explanation. Unknown codes fall back to a sentence, never to the code.
const ROOM_ERROR_TEXT = {
  not_host: 'Only the host can do that.',
  not_ready: 'Not everyone is ready yet.',
  room_full: 'That table is full.',
  game_started: 'That game has already started.',
  seat_required: 'Take a seat first.',
  not_your_turn: 'It is not your turn.',
  stale_sequence: 'The table moved on — syncing.',
  seat_not_away: 'That player is still here.',
  seat_present: 'That player is still at the table.',
  bad_rejoin_key: 'That reconnect link is invalid or has expired.',
  game_cancelled: 'The host closed the table.',
  watching_disabled: 'Watching is turned off for this table.',
  watchers_full: 'This table already has enough spectators.',
  room_not_found: 'That room could not be found.',
  no_room: 'That room could not be found.',
  bad_theme: 'Choose a card set the room supports.',
  coop_theme: 'Co-op plays with the Official, Simple, Standard or Pokémon deck.',
  slow_down: 'That was quick — try again in a moment.',
  expected_seq_required: 'Something went out of sync — try again.',
  log_busy: 'The game log is still being written — try again in a moment.',
  watch_required: 'That spectator link is not valid.',
  'no replay recorded': 'No replay is available for this game.',
};

export function roomErrorText(code) {
  if (!code) return 'That did not work.';
  const key = String(code).trim().toLowerCase();
  return ROOM_ERROR_TEXT[key] || 'That did not work.';
}

async function call(method, path, body) {
  const res = await fetch(path, {
    method,
    headers: { 'Content-Type': 'application/json', ...seatHeader() },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  let data = null;
  try {
    data = await res.json();
  } catch {
    data = null;
  }
  if (!res.ok) {
    const err = new Error(roomErrorText(data && data.error));
    err.status = res.status;
    err.body = data;
    const after = Number(res.headers.get('Retry-After'));
    if (Number.isFinite(after) && after > 0) err.retryAfter = after * 1000;
    throw err;
  }
  return data;
}

// storeSeat writes this tab's chair. Exported so the node tests can prove two tabs
// do not share one record without standing up a browser.
export function storeSeat() {
  try {
    localStorage.setItem(seatKey(coop.code), JSON.stringify({ seat: coop.seat, token: coop.token }));
  } catch {
    /* private mode: the seat still works for this tab */
  }
}

export function loadSeat(code) {
  try {
    const key = seatKey(code);
    let raw = localStorage.getItem(key);
    if (!raw) {
      // One-time migration of the pre-T10 room-scoped record. The first tab to ask
      // takes it and removes it, so a second tab cannot inherit the same chair.
      const legacy = SEAT_STORAGE + code;
      raw = localStorage.getItem(legacy);
      if (raw) {
        localStorage.setItem(key, raw);
        localStorage.removeItem(legacy);
      }
    }
    if (!raw) return false;
    const saved = JSON.parse(raw);
    if (!saved || typeof saved.token !== 'string') return false;
    coop.seat = Number(saved.seat);
    coop.token = saved.token;
    return true;
  } catch {
    return false;
  }
}

export function forgetSeat(code) {
  try {
    localStorage.removeItem(seatKey(code));
    localStorage.removeItem(returnKey(code));
    // A pre-T10 record is not this tab's to rely on any more; drop it with the seat.
    localStorage.removeItem(SEAT_STORAGE + code);
    localStorage.removeItem(RETURN_STORAGE + code + '.return');
  } catch {
    /* nothing to forget */
  }
}

// forgetLostSeat drops the chair the server just rejected and keeps everything else.
// A tab that was covering another chair still owns the chair in its return record;
// forgetting that too left the server holding a seat whose token no browser had
// (the seats smoke's "leave gives up both chairs" invariant).
export function forgetLostSeat(code) {
  try {
    localStorage.removeItem(seatKey(code));
  } catch {
    /* nothing to forget */
  }
}

// ---- covering another seat ------------------------------------------------
//
// A host may take an away or bot seat to keep a table moving. Their own seat is still
// theirs, so the identity they left behind is kept beside the seat, not on top of it:
// "Return to your own seat" restores exactly the token the server still honours.

function storeReturn(seat, token) {
  try {
    localStorage.setItem(returnKey(coop.code), JSON.stringify({ seat, token }));
  } catch {
    /* private mode */
  }
}

// returnSeat is the seat this tab was sitting on before it covered another, or null.
export function returnSeat() {
  try {
    const key = returnKey(coop.code);
    let raw = localStorage.getItem(key);
    if (!raw) {
      // The same one-time migration as loadSeat: the covering record was room-scoped.
      const legacy = RETURN_STORAGE + coop.code + '.return';
      raw = localStorage.getItem(legacy);
      if (raw) {
        localStorage.setItem(key, raw);
        localStorage.removeItem(legacy);
      }
    }
    if (!raw) return null;
    const saved = JSON.parse(raw);
    if (!saved || typeof saved.token !== 'string' || typeof saved.seat !== 'number') return null;
    return saved;
  } catch {
    return null;
  }
}

// coveringSeat is true while this tab holds somebody else's chair.
export function coveringSeat() {
  const saved = returnSeat();
  return Boolean(saved && saved.seat !== coop.seat);
}

// returnToOwnSeat goes back to the chair this tab left, with the token it kept. The
// server never minted a new one, so nothing has to be re-authenticated.
//
// A failed read must not leave the tab holding the dead token: the covered chair is
// still usable, so the previous identity is put back and the caller can say so. If the
// server says the old token is gone (401), the saved chair is dropped — a menu entry
// that can never work is worse than no entry.
export async function returnToOwnSeat() {
  const saved = returnSeat();
  if (!saved) return refresh();
  const covered = { seat: coop.seat, token: coop.token };
  coop.seat = saved.seat;
  coop.token = saved.token;
  try {
    const data = await refresh();
    storeSeat();
    try {
      localStorage.removeItem(returnKey(coop.code));
    } catch {
      /* private mode */
    }
    return data;
  } catch (err) {
    coop.seat = covered.seat;
    coop.token = covered.token;
    if (err && err.status === 401) {
      try {
        localStorage.removeItem(returnKey(coop.code));
      } catch {
        /* private mode */
      }
    }
    throw err;
  }
}

// eventsAfter cuts the events of the new action out of the rolling window: the log is
// append-only, so everything after the previous window's last event is new. Finding
// that event by content is enough because the window never inserts into the middle.
function eventKey(ev) {
  return [ev.turn, ev.type, (ev.cards || []).join('+'), ev.amount || 0, ev.seat ?? ''].join('|');
}

function eventsAfter(prev, next) {
  if (!Array.isArray(prev) || prev.length === 0) return next;
  const last = eventKey(prev[prev.length - 1]);
  for (let i = next.length - 1; i >= 0; i -= 1) {
    if (eventKey(next[i]) === last) return next.slice(i + 1);
  }
  return next;
}

// adopt applies a server answer to the module state, and it is authoritative: the
// server is the only thing that knows whether this tab is still looking through a
// seat, so anything it does not assert is cleared rather than remembered. A stale
// watchingAs used to survive a switch-off and keep requesting a hand (F-12).
export function adopt(data) {
  if (!data) return;
  let moved = false;
  // Within one room, sequence always wins: a frame or a poll answer older than what
  // this tab is showing is discarded, or a slow in-flight GET could put the board back
  // a turn after the stream had moved on (F-51). A DIFFERENT room starts its own count,
  // so a switch is never stale.
  const sameRoom = !data.code || data.code === coop.code;
  if (sameRoom && typeof data.seq === 'number' && coop.seq && data.seq < coop.seq) return;
  if (data.code && data.code !== coop.code) {
    // A different room. Nothing about the old table may leak into the new one.
    coop.state = null;
    coop.room = null;
    coop.lobby = true;
    coop.watchingAs = -1;
    coop.status = '';
    coop.error = '';
    coop.seq = 0;
    coop.eventSeq = 0;
    coop.lastEvents = [];
    coop.pendingReveal = null;
  }
  if (data.code) coop.code = data.code;
  if (typeof data.seat === 'number' && data.seat >= 0) {
    if (data.seat !== coop.seat && coop.token) moved = true;
    coop.seat = data.seat;
  }
  if (data.token) coop.token = data.token;
  if (data.watchId) coop.watchId = data.watchId;
  // Only a spectator can look through a seat, and only the server decides whether
  // it still may: it clamps the view to -1 when hands are switched off.
  coop.watchingAs = data.spectator && typeof data.watchingAs === 'number' ? data.watchingAs : -1;
  if (data.room) coop.room = data.room;
  // The table reveal is fed from the events of a forward sequence. `state.events` is
  // the last 40 log entries, not this action's events, so the news is only the part
  // after the previous window: a Jester choice, a yield or an end-payment then has no
  // play of its own and cannot re-show the last one (playtest 4, note 8).
  if (data.state && typeof data.seq === 'number') {
    const events = Array.isArray(data.state.events) ? data.state.events : [];
    if (coop.eventSeq && data.seq > coop.eventSeq && events.length) {
      const fresh = eventsAfter(coop.lastEvents, events);
      if (fresh.length) coop.pendingReveal = { events: fresh, seq: data.seq };
    }
    coop.lastEvents = events;
    coop.eventSeq = data.seq;
  }
  if (data.state) coop.state = data.state;
  if (typeof data.lobby === 'boolean') coop.lobby = data.lobby;
  if (typeof data.seq === 'number') coop.seq = data.seq;
  // A lobby settings answer settles whatever the next click was patching on top of.
  rulesPending = null;
  // A compaction moved this tab to a new chair: the saved seat number has to follow,
  // or a reload would restore the old label even though the token still works.
  if (moved) storeSeat();
}

// ---------- actions -------------------------------------------------------

export async function host(theme, jokers = [], jokersCustom = false) {
  const data = await call('POST', '/api/rooms', { theme: theme || 'official', jokers, jokersCustom });
  adopt(data);
  storeSeat();
  return data;
}

export async function join(code) {
  const data = await call('POST', `/api/rooms/${encodeURIComponent(code)}/join`);
  adopt(data);
  storeSeat();
  return data;
}

export async function watch(code) {
  const data = await call('POST', `/api/rooms/${encodeURIComponent(code)}/watch`);
  adopt(data);
  coop.seat = -1;
  // Watching is not a seat and must never carry one: a leftover token would make
  // every later call authenticate as a player who is not watching.
  coop.token = '';
  coop.watchingAs = typeof data.watchingAs === 'number' && data.spectator ? data.watchingAs : -1;
  // A watch id is ephemeral and never restored: the server keeps it in memory only, so
  // writing it to storage would be a second, dead copy of a value no code reads.
  return data;
}

export async function reclaim(code, seat, key) {
  const data = await call('POST', `/api/rooms/${encodeURIComponent(code)}/seats/${seat}/reclaim`, { key });
  adopt(data);
  coop.watchId = '';
  storeSeat();
  return data;
}

// roomPath is the read endpoint for whoever this tab is. A spectator has no seat, so
// it must read the watch route; asking the seat route with only a watch id is a 401,
// which is exactly the bug that froze the first watched game.
export function roomPath(code, isWatcher, watchingAs) {
  const base = `/api/rooms/${encodeURIComponent(code)}`;
  if (!isWatcher) return base;
  return watchingAs >= 0 ? `${base}/watch?as=${watchingAs}` : `${base}/watch`;
}

export async function refresh() {
  const data = await call('GET', roomPath(coop.code, Boolean(coop.watchId), coop.watchingAs));
  adopt(data);
  return data;
}

// play is the co-op action path: the same action objects the board builds in solo,
// posted to the room with this seat's token and the sequence the client last saw.
// A 409 carries the server's truth, so the caller repairs instead of retrying blind.
export async function play(action, actionId) {
  const data = await call('POST', `/api/rooms/${encodeURIComponent(coop.code)}/actions`, {
    actionId: actionId || '',
    expectedSeq: coop.seq,
    action,
  });
  adopt(data);
  return data;
}

// handOverHost gives the crown to another present seat (D-60).
export async function handOverHost(seat) {
  return adoptAndReturn(await call('POST', `/api/rooms/${encodeURIComponent(coop.code)}/host`, { seat }));
}

// setBotSeat hands an away seat to the bot, or takes it back (T7.5).
export async function setBotSeat(seat, on) {
  return adoptAndReturn(await call('POST', `/api/rooms/${encodeURIComponent(coop.code)}/bot`, { seat, on: Boolean(on) }));
}

export async function setReady(ready) {
  return adoptAndReturn(await call('POST', `/api/rooms/${coop.code}/ready`, { ready }));
}

export async function startGame() {
  // Send the sequence this screen is showing: a seat that joined after the host looked
  // must not be dealt into a table they did not see (F-33).
  return adoptAndReturn(await call('POST', `/api/rooms/${coop.code}/start`, { expectedSeq: coop.seq }));
}

// playAgain re-deals the same seats after a result. The finished log is kept.
export async function playAgain() {
  const data = await call('POST', `/api/rooms/${coop.code}/again`, { expectedSeq: coop.seq });
  adopt(data);
  return data;
}

export async function setWatching(allowed, hands) {
  return adoptAndReturn(await call('POST', `/api/rooms/${coop.code}/watching`, { allowed, hands }));
}

// setTheme chooses the card set the next deal uses. Host only, lobby only, and one of
// the sets co-op can read at four hands.
export async function setTheme(theme, jokers = [], jokersCustom = false) {
  return adoptAndReturn(await call('POST', `/api/rooms/${coop.code}/theme`, { theme, jokers, jokersCustom }));
}

// setRules changes the table settings the next deal uses. Host only, lobby only; the
// server refuses the rest with not_host or game_started.
export async function setRules(rules) {
  return adoptAndReturn(await call('POST', `/api/rooms/${coop.code}/rules`, { rules }));
}

// setBotFill turns the 60-second disconnect recovery on or off. Host only, lobby only.
export async function setBotFill(on) {
  return adoptAndReturn(await call('POST', `/api/rooms/${coop.code}/botfill`, { on: Boolean(on) }));
}

// addBot seats a bot in the next empty lobby chair. One human plus one bot is a table.
export async function addBot() {
  return adoptAndReturn(await call('POST', `/api/rooms/${coop.code}/addbot`));
}

// kick removes somebody else's chair. Host only: the server decides, not the menu.
export async function kick(seat) {
  return adoptAndReturn(await call('DELETE', `/api/rooms/${coop.code}/seats/${seat}`));
}

// leaveTable gives up this seat. In a lobby the chair disappears; in a dealt game the
// seat stays and a bot or the host's menu takes it over. The local identity goes with
// it, because the token that held the chair no longer holds anything.
//
// A tab that was covering another seat holds two credentials. "Leave the table" means
// both chairs, so the covered one goes first and then the tab's own one: dropping the
// return record without that second call left the host's own chair held by a token no
// browser had any more.
export async function leaveTable() {
  const own = returnSeat();
  await call('POST', `/api/rooms/${coop.code}/leave`);
  if (own && own.seat !== coop.seat) {
    const covered = { seat: coop.seat, token: coop.token };
    coop.seat = own.seat;
    coop.token = own.token;
    try {
      await call('POST', `/api/rooms/${coop.code}/leave`);
    } catch (err) {
      // The own chair may already be gone (taken over, or the room ended). The covered
      // one is freed either way, so this is not a failure to leave.
      if (err && err.status !== 401 && err.status !== 404 && err.status !== 409) {
        coop.seat = covered.seat;
        coop.token = covered.token;
        throw err;
      }
    }
  }
  forgetSeat(coop.code);
  coop.token = '';
  coop.seat = -1;
  coop.room = null;
  coop.state = null;
  coop.seq = 0;
  coop.lobby = true;
  coop.status = 'left';
}

// stopWatching ends a spectator's view. The watch id dies with the call, so this tab
// cannot go on reading a table the host believes it left.
export async function stopWatching() {
  try {
    await call('DELETE', `/api/rooms/${coop.code}/watch`);
  } finally {
    coop.watchId = '';
    coop.watchingAs = -1;
    coop.room = null;
    coop.state = null;
    coop.seq = 0;
    coop.status = 'left';
  }
}

// takeSeat hands an away seat to this device: the old token dies with it (D-41).
// Taking a chair other than our own keeps the one we left, because covering a seat
// must not cost the host their seat.
export async function takeSeat(seat) {
  if (coop.token && seat !== coop.seat && !returnSeat()) storeReturn(coop.seat, coop.token);
  const data = await call('POST', `/api/rooms/${coop.code}/seats/${seat}/take`);
  adopt(data);
  storeSeat();
  return data;
}

// closeTable ends it for everyone, with a reason on their screen (D-42).
export async function closeTable() {
  return adoptAndReturn(await call('POST', `/api/rooms/${coop.code}/cancel`));
}

export async function invite(seat) {
  return call('POST', `/api/rooms/${coop.code}/seats/${seat}/invite`);
}

// viewAs switches a spectator to another player's board.
export async function viewAs(seat) {
  coop.watchingAs = seat;
  return refresh();
}

export function isHost() {
  return Boolean(coop.room) && coop.seat >= 0 && coop.seat === coop.room.hostSeat;
}

export function isSpectator() {
  return Boolean(coop.watchId);
}

// Polling is a chain of timeouts, not an interval: an interval keeps firing on top
// of a request that has not answered, so a slow server turns into a queue of
// requests and a phone burns its battery twice (F-27).
export const POLL_INTERVAL = 1500;

// backoffMs is the delay after the nth consecutive failure.
export function backoffMs(failures) {
  return Math.min(15000, 1000 * Math.pow(2, Math.max(0, failures - 1)));
}

// createPoller runs `tick` until it is stopped. Terminal statuses (a dead seat, a
// dead room) stop it; three consecutive soft failures stop it as offline, because a
// screen that retries forever is a screen that never tells the player.
export function createPoller({ tick, onEvent, interval = POLL_INTERVAL, timers = null, maxFailures = 3 }) {
  const clock = timers || { setTimeout: (fn, ms) => setTimeout(fn, ms), clearTimeout: (id) => clearTimeout(id) };
  let timer = 0;
  let failures = 0;
  let running = false;
  let stopped = true;

  const terminal = (status) => status === 401 || status === 404 || status === 410;

  function schedule(ms) {
    if (stopped) return;
    timer = clock.setTimeout(run, ms);
  }

  async function run() {
    timer = 0;
    if (stopped || running) return;
    running = true;
    let wait = interval;
    try {
      await tick();
      if (failures > 0) {
        failures = 0;
        if (onEvent) onEvent('recovered');
      }
    } catch (err) {
      const status = err && err.status;
      if (terminal(status)) {
        stopped = true;
        if (onEvent) onEvent('stop', err);
        running = false;
        return;
      }
      failures += 1;
      if (failures >= maxFailures) {
        stopped = true;
        if (onEvent) onEvent('offline', err);
        running = false;
        return;
      }
      wait = err && err.retryAfter ? Math.max(err.retryAfter, backoffMs(failures)) : backoffMs(failures);
      if (onEvent) onEvent('backoff', err, failures);
    } finally {
      running = false;
    }
    schedule(wait);
  }

  return {
    start(delay = 0) {
      stopped = false;
      failures = 0;
      schedule(delay);
      return this;
    },
    stop() {
      stopped = true;
      if (timer) clock.clearTimeout(timer);
      timer = 0;
    },
    get failures() {
      return failures;
    },
    get stopped() {
      return stopped;
    },
  };
}

// adoptFrame applies one state frame from the stream. It is a room response without
// the credential, so it goes through the same adopt() rules: a seat never keeps a
// view-as, and the server's sequence always wins.
//
// The frame names this subscriber's seat. A lobby compaction renumbers everybody
// above the chair that left, and a browser that kept its old number would send the
// wrong label on every later request (the token still authenticates, but the tab
// would print the wrong seat until the next GET).
function adoptFrame(frame) {
  if (!frame) return;
  adopt({
    seq: frame.seq,
    seat: frame.seat,
    room: frame.room,
    lobby: frame.lobby,
    state: frame.state,
    spectator: Boolean(coop.watchId),
    watchingAs: frame.state ? Number.isInteger(frame.state.viewerSeat) && coop.watchId ? frame.state.viewerSeat : -1 : undefined,
  });
}

// startStreaming opens the presence stream (T1.4). The poll path stays available
// behind ?transport=poll for one release (T1.5).
export async function startStreaming(onChange, onPresence) {
  stopPolling();
  stopStreaming();
  const createLive = await loadLive();
  const live = createLive({
    url: `/api/rooms/${encodeURIComponent(coop.code)}/stream`,
    headers: seatHeader,
    onState: (frame) => {
      coop.frames.state = (coop.frames.state || 0) + 1;
      coop.lastFrameAt = performance.now();
      const before = coop.state && coop.state.turnNo;
      adoptFrame(frame);
      if (onChange) onChange(before);
    },
    onPresence: (frame) => {
      coop.frames.presence = (coop.frames.presence || 0) + 1;
      coop.presence = frame;
      if (onPresence) onPresence(frame);
    },
    onStatus: (status) => {
      if (status === 'live') {
        // A reconnect that works must take the "stopped answering" chip away: it only
        // ever cleared on a manual retry, so a table that came back still looked dead.
        if (coop.status === 'offline') {
          coop.status = '';
          if (onChange) onChange();
        }
        return;
      }
      if (status === 'offline' && coop.status !== 'offline') {
        // The stream is down but the seat is still ours: say so, keep the board.
        coop.status = 'offline';
        if (onChange) onChange();
        return;
      }
      if (status === 'ended') {
        if (coop.watchId) coop.status = 'watch_ended';
        else {
          forgetSeat(coop.code);
          coop.token = '';
          coop.seat = -1;
          coop.status = 'seat_lost';
        }
        if (onChange) onChange();
      }
    },
    repair: () => {
      // A gap: take a full snapshot, exactly as a reconnect would.
      refresh().then(() => onChange && onChange()).catch(() => {});
    },
  });
  live.start();
  coop.live = live;
  return live;
}

export function stopStreaming() {
  if (coop.live && typeof coop.live.stop === 'function') coop.live.stop();
  coop.live = null;
}

export function stopPolling() {
  if (coop.poll && typeof coop.poll.stop === 'function') coop.poll.stop();
  coop.poll = 0;
}

export function startPolling(onChange, ms = POLL_INTERVAL) {
  stopPolling();
  const poller = createPoller({
    interval: ms,
    tick: async () => {
      const before = coop.state && coop.state.turnNo;
      await refresh();
      if (onChange) onChange(before);
    },
    onEvent: (kind, err) => {
      if (kind === 'backoff') {
        coop.status = 'offline';
        if (onChange) onChange();
        return;
      }
      if (kind === 'recovered') {
        if (coop.status === 'offline') coop.status = '';
        return;
      }
      if (kind === 'offline') {
        coop.status = 'offline';
        if (onChange) onChange();
        return;
      }
      // A dead seat or a dead room: say so, and forget the identity that died.
      if (err && err.status === 401) {
        if (coop.watchId) {
          coop.status = 'watch_ended';
        } else {
          forgetSeat(coop.code);
          coop.token = '';
          coop.seat = -1;
          coop.status = 'seat_lost';
        }
      } else {
        coop.status = 'room_ended';
      }
      if (onChange) onChange();
    },
  });
  poller.start(ms);
  coop.poll = poller;
  return poller;
}

// __coopFrameAt is a read-only seam for the latency measurement: when the last state
// frame was parsed by this tab, in this tab's own clock.
if (typeof window !== 'undefined') {
  window.__coopFrameAt = () => coop.lastFrameAt || 0;
  window.__coopPresence = () => coop.presence;
}

// copyToClipboard copies text and says whether it worked. The async Clipboard API only
// exists in a secure context, so a table on http://192.168.x.x has no navigator.clipboard
// at all: every button that called it directly threw and the empty catch hid it. The
// textarea + execCommand path works on an insecure origin from a real click, in every
// engine.
export async function copyToClipboard(text) {
  if (navigator.clipboard && navigator.clipboard.writeText) {
    try {
      await navigator.clipboard.writeText(text);
      return true;
    } catch {
      /* fall through: a blocked permission is not a reason to stop trying */
    }
  }
  try {
    const area = document.createElement('textarea');
    area.value = text;
    area.setAttribute('readonly', '');
    area.style.cssText = 'position:fixed;top:0;left:0;opacity:0;pointer-events:none;';
    document.body.append(area);
    area.select();
    area.setSelectionRange(0, text.length);
    const ok = document.execCommand('copy');
    area.remove();
    return ok;
  } catch {
    return false;
  }
}

// ---------- rendering -----------------------------------------------------

function el(tag, className, text) {
  const node = document.createElement(tag);
  if (className) node.className = className;
  if (text !== undefined) node.textContent = text;
  return node;
}

// crown is the host marker: the same crown as the brand, small, sitting over the
// name rather than beside it, so a rail of four names reads at a glance.
export function crown(className = 'host-crown') {
  const img = document.createElement('img');
  img.src = CROWN_PATH;
  img.alt = 'Host';
  img.className = className;
  img.width = 14;
  img.height = 14;
  return img;
}

// seatColour is the rail colour for a seat; the rail, the log and the turn banner agree.
// The seat colour order is defined once, in format.js, with the rest of the pure UI
// helpers; the import lives in the top block and this re-export keeps the module's
// public surface unchanged.
export { SEAT_COLOURS };

// seatSummary is the whole public view of one seat: what the rail prints, with no
// DOM involved, so it can be tested directly (D-20).
export function seatSummary(room, state, seat) {
  const found = ((state && state.players) || []).find((p) => p.seat === seat.seat) || {};
  // won/lost live on the board, not on a seat: fold them in so the rail can stop
  // announcing a turn that has ended.
  const view = { ...found, won: Boolean(state && state.won), lost: Boolean(state && state.lost) };
  const limit = view.handLimit;
  const row = {
    seat: seat.seat,
    name: seat.name,
    host: Boolean(room) && room.hostSeat === seat.seat,
    you: Boolean(seat.you),
    away: Boolean(seat.away),
    bot: Boolean(seat.bot),
    left: Boolean(seat.left),
    takeable: Boolean(seat.takeable),
    active: Boolean(view.active),
    yielded: Boolean(view.yielded),
    handCount: view.handCount === undefined ? null : view.handCount,
    handLimit: limit === undefined ? null : limit,
    // The server sends handFull, but the rail must never disagree with the count it
    // prints, so it also derives it: a full hand is skipped by the diamond draw and
    // that has to be true of the number on screen.
    handFull: Boolean(view.handFull) ||
      (typeof view.handCount === 'number' && typeof limit === 'number' && view.handCount >= limit),
    count: view.handCount === undefined ? '—' : `${view.handCount}/${view.handLimit}`,

    // the fill of the hand bar, as a percentage of the limit
    fill: typeof view.handCount === 'number' && typeof limit === 'number' && limit > 0
      ? `${Math.min(100, Math.round((view.handCount / limit) * 100))}%`
      : '0%',
    lastPlay: Array.isArray(view.lastPlay) ? view.lastPlay : [],
  };
  // One word for the strip, derived from the row itself so it cannot disagree.
  row.state = seatState(room, seat, view, row);
  return row;
}

// seatState is the one word a seat strip carries (D-41, D-44).
function seatState(room, seat, view, row) {
  // A finished table has no "playing": the rail kept calling a seat active next to a
  // result sheet, which read as if the game were still going.
  if (view.won || view.lost) return '—';
  if (seat.left) return 'left';
  if (seat.away) return 'away';
  if (room && room.lobby) return seat.ready === false ? 'not ready' : 'ready';
  if (view.active) return row.you ? 'your turn' : 'playing';
  if (view.yielded) return 'yielded';
  return 'waiting';
}

// lastPlayThumb is one card plus a xN badge: the smallest thing that says "this seat
// just played three cards" without growing the strip.
function lastPlayThumb(row, viewerSeat, jokers) {
  if (!row.lastPlay || !row.lastPlay.length) return null;
  const wrap = el('span', 'seat-last');
  const id = row.lastPlay[row.lastPlay.length - 1];
  const img = document.createElement('img');
  const jester = /^j[12]$/i.test(String(id));
  img.src = jester
    ? jesterArt(coopThemeForArt(row), jokers)[String(id).toUpperCase() === 'J2' ? 1 : 0]
    : artUrl(coopThemeForArt(row), id);
  img.alt = '';
  img.loading = 'lazy';
  wrap.append(img);
  if (row.lastPlay.length > 1) wrap.append(el('span', 'combo', `×${row.lastPlay.length}`));
  return wrap;
}

// coopThemeForArt: co-op plays two sets only (D-32), and the client already knows
// which one the table was dealt with.
function coopThemeForArt(row) {
  return (coop.state && coop.state.theme) || coop.theme || 'simple';
}

// seatRail renders the public table: one place setting per seat, the host crowned,
// the state word, hand size against the limit (D-44), and the last play.
export function seatRail(room, state, viewerSeat, bubbles, jokers = null) {
  const rail = el('ol', 'seats');
  rail.classList.toggle('has-bubbles', Boolean(bubbles && bubbles.size));
  if (!room) return rail;
  for (const seat of room.seats) {
    const row = seatSummary(room, state, seat);
    if (seat.seat === viewerSeat) row.you = true;
    const item = el('li', 'seat');
    item.style.setProperty('--seat-c', `var(--seat-${SEAT_COLOURS[seat.seat % SEAT_COLOURS.length]})`);
    if (row.host) item.classList.add('is-host');
    if (row.you) item.classList.add('is-you');
    if (row.active) item.classList.add('is-active');
    if (row.away) item.classList.add('is-away');
    if (row.bot) item.classList.add('is-bot');
    // The state is a word as well as a colour, and the active seat is announced:
    // a rail that leaned on hue alone was unreadable for colour-blind players (F-22).
    item.setAttribute('aria-label', `${row.name}, ${row.state}, ${row.count} cards`);
    if (row.active) item.setAttribute('aria-current', 'true');

    const who = el('span', 'seat-who');
    // The crown sits over the host's name, not beside it.
    if (row.host) item.append(crown());
    who.append(el('span', 'seat-name', row.name));
    // A bot seat says so, in the state word: a table must never wonder whether it is
    // playing a person (T7.5).
    who.append(el('span', 'seat-state', row.bot ? 'bot' : row.state));
    item.append(who);
    // "6/6", with the limit dimmed: stripping the slash to save width produced "66",
    // which reads as sixty-six cards.
    const count = el('span', 'seat-count');
    const [have, limit] = String(row.count).split('/');
    const haveNode = el('b', null, have);
    count.append(haveNode, el('i', null, '/' + (limit || '')));
    item.append(count);

    // The last play, and a combo count when it was more than one card: a single
    // thumbnail of "the last card" lies about a three-card play.
    const play = lastPlayThumb(row, viewerSeat, jokers);
    if (play) item.append(play);

    // The bubble says what this player just did, beside their name. Its element is
    // built by the caller (it needs card chips), so this only places it.
    if (bubbles && bubbles.get(seat.seat)) {
      item.classList.add('has-bubble');
      item.append(bubbles.get(seat.seat));
    }

    // A spectator may tap a player to see the board as that player (D-45). The whole
    // chip is the button: a small "view as" label is one more thing to aim at.
    if (coop.watchId && room.watchHands) {
      item.tabIndex = 0;
      item.setAttribute('role', 'button');
      item.dataset.seat = String(seat.seat);
      item.setAttribute('aria-label', `View the board as ${seat.name}`);
      if (seat.seat === coop.watchingAs) item.classList.add('is-viewing');
    }
    rail.append(item);
  }
  return rail;
}

// DECK_LABELS is how the lobby names a card set. Co-op accepts three; the registry's
// own labels are for the solo menu, where every set is offered.
const DECK_LABELS = { official: 'Official', simple: 'Simple', '8bit': 'Standard', pokemon: 'Pokémon' };

// rulesPending carries the switches this host has changed but whose answers have not
// landed. Two clicks inside one round trip used to both patch the same rendered
// snapshot, so the second POST erased the first change.
let rulesPending = null;

// lobbySettings is the host's table-settings block: the rule switches the next deal
// uses, the spectator gates, and the disconnect recovery. Everything here is frozen at
// the deal, which is why it lives in the lobby and nowhere else (decision 8).
function lobbySettings(room, onAction) {
  const panel = el('section', 'lobby-settings');
  panel.setAttribute('aria-label', 'Table settings');
  panel.append(el('h2', 'lobby-settings-title', 'Table settings'));
  const rules = room.rules || {};
  // The deck the table will deal. Co-op limits the choices to the sets that read at
  // four hands; the host picks here, and the deal freezes it (decision 8).
  const deckRow = el('div', 'lobby-decks');
  deckRow.append(el('span', 'hint', 'Card set'));
  const sets = el('div', 'segmented lobby-deck-set');
  const current = room.theme || 'official';
  const deckHints = {
    official: 'The published Regicide deck (the default).',
    simple: 'A clean, high-contrast deck that reads at a glance.',
    '8bit': 'The 8-bit Standard deck with chunky, pixel-art faces.',
    pokemon: 'Detailed art: hard to read at four hands, and on a phone.',
  };
  for (const theme of room.themes || []) {
    const b = el('button', 'seg', DECK_LABELS[theme] || theme);
    b.type = 'button';
    b.title = `Card set: ${deckHints[theme] || 'The art this table will use.'} Fixed when the host deals.`;
    b.setAttribute('aria-pressed', String(theme === current));
    b.addEventListener('click', () => {
      if (theme !== current) onAction('theme', theme);
    });
    sets.append(b);
  }
  deckRow.append(sets);
  panel.append(deckRow);
  const toggles = el('div', 'lobby-toggles');

  const toggle = (label, checked, disabled, onChange, title) => {
    const wrap = el('label', 'lobby-toggle');
    const box = document.createElement('input');
    box.type = 'checkbox';
    box.checked = Boolean(checked);
    box.disabled = Boolean(disabled);
    if (title) {
      box.title = title;
      wrap.title = title;
    }
    box.addEventListener('change', () => onChange(box.checked));
    wrap.append(box, document.createTextNode(` ${label}`));
    return wrap;
  };
  const nextRules = (patch) => {
    rulesPending = { ...(rulesPending || rules), ...patch };
    return onAction('rules', rulesPending);
  };

  toggles.append(toggle('Chaos (royals mixed in)', rules.chaos, false,
    (on) => nextRules({ chaos: on }),
    'Shuffle all twelve royals into one pile, so a King can open the run. Chaos runs do not enter the leaderboard.'));
  toggles.append(toggle('Turn passes when a royal falls (easier)', rules.passOnDefeat, false,
    (on) => nextRules({ passOnDefeat: on }),
    'By default the killing blow is not a rest: the same seat faces the next royal. On, the next seat takes the fresh royal.'));
  toggles.append(toggle('Exact damage', rules.exactDamage, false,
    (on) => nextRules({ exactDamage: on }),
    'A royal killed with exactly its remaining health goes face down on top of the tavern and joins a hand when drawn.'));
  toggles.append(toggle('Spectators', room.allowWatch, false,
    (on) => onAction('watch', on, on ? room.watchHands : false),
    'People without a seat may watch the table read-only. They cannot act or join a chair.'));
  toggles.append(toggle('Spectator hands', room.watchHands, !room.allowWatch,
    (on) => onAction('watch', room.allowWatch, on),
    'A spectator may look through one player\'s hand at a time. Off: they see the table only.'));
  toggles.append(toggle('Bots fill missing seats', room.botFill, false,
    (on) => onAction('botfill', on),
    'A seat silent for a minute gets a bot until its player touches it again. The bot uses the same rules and cannot see another hand.'));
  panel.append(toggles);

  return panel;
}

// lobbyBotRow is the host's way to fill the next free chair. It is a seating action,
// so it lives under the seat list with the seats it fills (0.9.8): it used to sit at
// the bottom of Table settings, below six switches, which is the last place a host
// looks while filling a table.
function lobbyBotRow(room, onAction) {
  const row = el('div', 'lobby-bot-row');
  const add = el('button', 'ghost small', 'Add a bot');
  add.type = 'button';
  add.disabled = room.players >= room.capacity;
  add.title = add.disabled
    ? 'The table is full.'
    : 'Put a bot in the next free chair. Bots count as seats and take a full turn.';
  add.addEventListener('click', () => onAction('addbot'));
  row.append(add, el('span', 'hint', 'Bots count as seats.'));
  return row;
}

// lobbyBody renders the waiting room: the code to share, the seats, and the start
// controls. It is deliberately plain — a lobby is read once and left.
export function lobbyBody(room, onAction) {
  const wrap = el('div', 'lobby-inner');
  if (!room) return wrap;

  const codeRow = el('div', 'lobby-code-row');
  const code = el('button', 'room-code', room.code);
  code.type = 'button';
  code.title = 'Copy the room code';
  code.addEventListener('click', async () => {
    if (await copyToClipboard(room.code)) {
      code.classList.add('is-copied');
      setTimeout(() => code.classList.remove('is-copied'), 1200);
    } else {
      code.classList.add('is-copied');
      code.textContent = 'select it';
      setTimeout(() => {
        code.classList.remove('is-copied');
        code.textContent = room.code;
      }, 2400);
    }
  });
  codeRow.append(el('span', 'lobby-label', 'room'), code);
  const link = el('button', 'ghost lobby-share', 'Copy invite link');
  link.type = 'button';
  link.addEventListener('click', async () => {
    const url = `${location.origin}/?room=${room.code}`;
    if (await copyToClipboard(url)) {
      link.textContent = 'Copied';
      setTimeout(() => { link.textContent = 'Copy invite link'; }, 1200);
      return;
    }
    // Say so, and put the link where it can be read: a silent failure reads as a bug
    // in the button, which is exactly what it was.
    const shown = el('input', 'lobby-share-fallback');
    shown.value = url;
    shown.readOnly = true;
    shown.addEventListener('focus', () => shown.select());
    link.replaceWith(shown);
    shown.focus();
  });
  codeRow.append(link);
  // The two links belong together: the invite is the seat link, the spectator link is
  // the read-only one, and a host reads both from the same row (playtest 4, note 5).
  if (room.watchLink && isHost()) {
    const share = el('button', 'ghost lobby-share', 'Copy spectator link');
    share.type = 'button';
    share.addEventListener('click', async () => {
      const url = `${location.origin}${room.watchLink}`;
      if (await copyToClipboard(url)) {
        share.textContent = 'Copied';
        setTimeout(() => { share.textContent = 'Copy spectator link'; }, 1200);
      } else {
        share.textContent = url;
        share.title = 'Copy failed — this link is selectable';
      }
    });
    codeRow.append(share);
  }
  wrap.append(codeRow);

  const rules = room.nextRules || {};
  const deck = DECK_LABELS[room.theme] || room.theme;
  wrap.append(el('p', 'lobby-rules',
    `${room.players} of ${room.capacity} seated` +
    `${deck ? ` · ${deck} deck` : ''} · hand limit ${rules.maxHand} · ` +
    `${rules.jestersInTavern} Jester${rules.jestersInTavern === 1 ? '' : 's'} in the tavern`));

  const list = el('ol', 'lobby-seats');
  for (const seat of room.seats) {
    const row = el('li', 'lobby-seat');
    if (seat.seat === room.hostSeat) row.classList.add('is-host');
    if (seat.you) row.classList.add('is-you');
    if (seat.away) row.classList.add('is-away');
    const head = el('span', 'seat-head');
    if (seat.seat === room.hostSeat) head.append(crown());
    head.append(el('span', 'seat-name', seat.name));
    head.append(el('span', `seat-dot seat-${SEAT_COLOURS[seat.seat] || 'grey'}`));
    row.append(head);
    const state = seat.you ? 'you' : (seat.bot ? 'bot' : (seat.left ? 'left' : (seat.away ? 'away' : (seat.ready ? 'ready' : 'waiting'))));
    row.append(el('span', 'seat-state', state));
    // The host can free a chair before the deal: the row is the only place where the
    // person to remove is named (note 1).
    if (isHost() && !seat.you) {
      const remove = el('button', 'ghost small lobby-remove', 'Remove');
      remove.type = 'button';
      remove.setAttribute('aria-label', `Remove ${seat.name}`);
      remove.addEventListener('click', () => onAction('kick', seat.seat));
      row.append(remove);
      if (!seat.bot) {
        const bot = el('button', 'ghost small lobby-bot', 'Bot');
        bot.type = 'button';
        bot.title = 'A bot plays this seat until the player returns';
        bot.addEventListener('click', () => onAction('bot', seat.seat, true));
        row.append(bot);
      }
    }
    list.append(row);
  }
  // The free chairs are drawn, not implied: a "1 of 4 seated" table that shows one row
  // reads smaller than it is, and the Add-a-bot row below them is their footer.
  for (let i = (room.seats || []).length; i < (room.capacity || 0); i += 1) {
    // No aria-label and no seat dot: the row's own text names it, a second label
    // announces twice, and a colourless dot class had no rule to fill (review NITs).
    const empty = el('li', 'lobby-seat is-empty');
    empty.append(el('span', 'seat-head', 'Empty chair'));
    empty.append(el('span', 'seat-state', 'empty'));
    list.append(empty);
  }
  wrap.append(list);

  // The host's seating controls come next, before any settings: the common flow is
  // add-a-bot (up to three) then deal, and it must not scroll past six switches
  // (0.9.8, operator note).
  if (isHost()) wrap.append(lobbyBotRow(room, onAction));

  const actions = el('div', 'lobby-actions');
  // Only the host may deal: the server refuses everyone else with 409 not_host, so
  // offering the button to every seat was an invitation to a refusal (note 2). The
  // host's button is always on screen — a deal that is one ready-toggle away should
  // not have to be searched for, and a disabled button with its reason says what is
  // left to do.
  if (isHost()) {
    const start = el('button', 'primary', 'Start the game');
    start.type = 'button';
    start.disabled = !room.startable;
    start.title = 'Deal the cards with the current table and its settings.';
    start.addEventListener('click', () => onAction('start'));
    actions.append(start);
    if (!room.startable) {
      actions.append(el('p', 'lobby-wait', (room.players || 0) < 2
        ? 'Fill another chair — add a bot or share the code.'
        : 'Waiting for everyone to be ready.'));
    }
  } else if (room.startable) {
    // The pre-0.9.8 line passed an attrs object where this module's el() takes a
    // class string, so the guest's waiting line rendered as an empty paragraph with
    // class "[object Object]" — invisible for as long as it has existed.
    actions.append(el('p', 'lobby-wait', 'Everyone is ready — waiting for the host to deal.'));
  }
  if (actions.childElementCount) wrap.append(actions);

  // Table settings are the host's, and they are frozen at the deal: the server
  // refuses them from anyone else and after the cards are out (decision 8).
  if (isHost()) wrap.append(lobbySettings(room, onAction));
  return wrap;
}
