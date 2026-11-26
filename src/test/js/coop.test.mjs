// Unit tests for the co-op client module: the action path, the authority rules on
// adopt(), and the polling scheduler. These import the file the browser loads, so a
// regression in the co-op client breaks the gate instead of only the table.
//
// The action path is the F-1 regression test: for one release the whole co-op client
// was reported playable while act() still posted to a solo session nobody had.
import test from 'node:test';
import assert from 'node:assert/strict';

import {
  coop, adopt, play, watch, createPoller, backoffMs, POLL_INTERVAL,
  storeSeat, loadSeat, forgetSeat, returnSeat,
} from '../../regicide/server/web/coop.js';

// reset puts the module back to a known table between tests: it is a singleton.
function reset(extra = {}) {
  Object.assign(coop, {
    code: '', seat: -1, token: '', watchId: '', watchingAs: -1,
    room: null, state: null, lobby: true, error: '', status: '', seq: 0, poll: 0,
    eventSeq: 0, lastEvents: [], pendingReveal: null,
  }, extra);
}

// fakeFetch records calls and answers with one canned response.
function fakeFetch(responses) {
  const calls = [];
  const queue = [...responses];
  const fn = async (url, init) => {
    calls.push({ url, method: (init && init.method) || 'GET', headers: (init && init.headers) || {}, body: init && init.body });
    const next = queue.length > 1 ? queue.shift() : queue[0];
    return {
      ok: next.status >= 200 && next.status < 300,
      status: next.status,
      headers: { get: () => next.retryAfter || null },
      json: async () => next.body,
    };
  };
  return { fn, calls };
}

test('a fresh join never replays the log as reveals; a forward frame does', () => {
  // Joining or reloading mid-game hands over the last 40 log lines. The reveal feed
  // must start at the current sequence, not replay the whole window: the guard is
  // `coop.eventSeq && data.seq > coop.eventSeq` in adopt().
  reset();
  const events = [
    { turn: 1, type: 'play', seat: 0, cards: ['10C'], amount: 20 },
    { turn: 1, type: 'defeat', cards: ['JS'], amount: 20, note: 'exact' },
    { turn: 1, type: 'enemy_reveal', cards: ['JH'], seat: 1 },
  ];
  adopt({ code: 'ABCDE', seat: 0, token: 't', seq: 12, state: { events } });
  assert.equal(coop.pendingReveal, null, 'a join must not queue a reveal');
  assert.equal(coop.eventSeq, 12);

  // The same sequence arriving again (a poll of the frame just adopted) is not news.
  adopt({ code: 'ABCDE', seat: 0, token: 't', seq: 12, state: { events } });
  assert.equal(coop.pendingReveal, null, 'the same frame is not news');

  // A forward frame offers only its fresh suffix: one defeat, not the old window.
  adopt({
    code: 'ABCDE', seat: 0, token: 't', seq: 13,
    state: { events: [...events, { turn: 2, type: 'defeat', cards: ['JH'], amount: 20 }] },
  });
  assert.ok(coop.pendingReveal, 'a forward frame queues its news');
  assert.equal(coop.pendingReveal.seq, 13);
  assert.deepEqual(
    coop.pendingReveal.events.map((e) => e.type),
    ['defeat'],
    'only the suffix is offered to the reveal queue',
  );
});

test('adopt is authoritative: a seat never keeps a view-as', () => {
  reset({ watchingAs: 2, spectator: true });
  adopt({ code: 'ABCDE', seat: 0, spectator: false, watchingAs: 3, seq: 5 });
  assert.equal(coop.watchingAs, -1, 'a seat must not look through another hand');
  assert.equal(coop.seq, 5);
});

test('adopt takes the server\'s clamped view-as for a spectator', () => {
  reset();
  adopt({ code: 'ABCDE', seat: -1, spectator: true, watchingAs: 2, seq: 4 });
  assert.equal(coop.watchingAs, 2);
  // The host switched hands off: the server says -1, and it wins over whatever the
  // tab remembered (F-12).
  adopt({ code: 'ABCDE', seat: -1, spectator: true, watchingAs: -1, seq: 5 });
  assert.equal(coop.watchingAs, -1);
});

test('adopt forgets the previous room entirely', () => {
  reset({ code: 'AAAAA', seq: 9, room: { code: 'AAAAA' }, state: { turnNo: 3 }, lobby: false });
  adopt({ code: 'BBBBB', seq: 1, lobby: true, room: { code: 'BBBBB' } });
  assert.equal(coop.code, 'BBBBB');
  assert.equal(coop.state, null, 'the old board must not survive a room change');
  assert.equal(coop.seq, 1, 'the old sequence must not survive a room change');
  assert.equal(coop.lobby, true);
});

test('play posts the action to the room with this seat and sequence', async () => {
  reset({ code: 'ABCDE', seat: 1, token: 'tok', seq: 7, lobby: false });
  const { fn, calls } = fakeFetch([{ status: 200, body: { code: 'ABCDE', seat: 1, seq: 8, state: { turnNo: 1 } } }]);
  const realFetch = globalThis.fetch;
  globalThis.fetch = fn;
  try {
    await play({ type: 'play', cardIds: ['s5'] }, 'act-1');
  } finally {
    globalThis.fetch = realFetch;
  }
  assert.equal(calls.length, 1);
  assert.equal(calls[0].method, 'POST');
  assert.equal(calls[0].url, '/api/rooms/ABCDE/actions');
  assert.equal(calls[0].headers['X-Regicide-Seat'], '1:tok');
  const body = JSON.parse(calls[0].body);
  assert.equal(body.actionId, 'act-1');
  assert.equal(body.expectedSeq, 7);
  assert.deepEqual(body.action, { type: 'play', cardIds: ['s5'] });
  assert.equal(coop.seq, 8, 'the answer is adopted');
});

test('play never posts to the solo session route', async () => {
  reset({ code: 'ABCDE', seat: 0, token: 'tok', seq: 2, lobby: false });
  const { fn, calls } = fakeFetch([{ status: 200, body: { code: 'ABCDE', seat: 0, seq: 3 } }]);
  const realFetch = globalThis.fetch;
  globalThis.fetch = fn;
  try {
    await play({ type: 'yield' }, 'act-2');
  } finally {
    globalThis.fetch = realFetch;
  }
  assert.ok(calls.every((c) => !c.url.includes('/api/sessions')), calls.map((c) => c.url).join(' '));
});

test('a refusal carries the state so the caller can repair', async () => {
  reset({ code: 'ABCDE', seat: 0, token: 'tok', seq: 7, lobby: false });
  const { fn } = fakeFetch([{ status: 409, body: { error: 'stale_seq', seq: 9, state: { turnNo: 2 } } }]);
  const realFetch = globalThis.fetch;
  globalThis.fetch = fn;
  try {
    await assert.rejects(() => play({ type: 'yield' }, 'act-3'), (err) => {
      assert.equal(err.status, 409);
      assert.equal(err.body.seq, 9);
      assert.ok(err.body.state);
      return true;
    });
  } finally {
    globalThis.fetch = realFetch;
  }
});

test('watching drops any seat identity', async () => {
  reset({ code: 'ABCDE', seat: 2, token: 'leftover', lobby: false });
  const { fn } = fakeFetch([{ status: 200, body: { code: 'ABCDE', seat: -1, spectator: true, watchingAs: -1, watchId: 'w1', lobby: false } }]);
  const realFetch = globalThis.fetch;
  globalThis.fetch = fn;
  try {
    await watch('ABCDE');
  } finally {
    globalThis.fetch = realFetch;
  }
  assert.equal(coop.token, '', 'a spectator must not authenticate as a player');
  assert.equal(coop.seat, -1);
  assert.equal(coop.watchId, 'w1');
});

test('backoff doubles and is capped', () => {
  assert.equal(backoffMs(1), 1000);
  assert.equal(backoffMs(2), 2000);
  assert.equal(backoffMs(3), 4000);
  assert.equal(backoffMs(8), 15000);
});

// A manual clock, so scheduler assertions do not depend on wall time.
function fakeTimers() {
  let now = 0;
  let seq = 1;
  const queue = new Map();
  const flush = () => new Promise((resolve) => setImmediate(resolve));
  return {
    setTimeout(fn, ms) {
      const id = seq++;
      queue.set(id, { at: now + ms, fn });
      return id;
    },
    clearTimeout(id) {
      queue.delete(id);
    },
    async advance(ms) {
      const target = now + ms;
      for (;;) {
        let dueKey = 0;
        let due = null;
        for (const [id, entry] of queue) {
          if (entry.at <= target && (!due || entry.at < due.at)) {
            dueKey = id;
            due = entry;
          }
        }
        if (!due) break;
        queue.delete(dueKey);
        now = due.at;
        due.fn();
        await flush();
        await flush();
      }
      now = target;
    },
    get pending() {
      return queue.size;
    },
  };
}

test('the poller schedules the next tick only after the last one settles', async () => {
  const timers = fakeTimers();
  const events = [];
  let ticks = 0;
  const poller = createPoller({
    timers,
    tick: async () => {
      ticks += 1;
    },
    onEvent: (kind) => events.push(kind),
  });
  poller.start(0);
  await timers.advance(0);
  assert.equal(ticks, 1);
  assert.equal(timers.pending, 1, 'exactly one timer waits for the next tick');
  await timers.advance(POLL_INTERVAL);
  assert.equal(ticks, 2);
  poller.stop();
  assert.equal(timers.pending, 0, 'stopping clears the timer');
});

test('three consecutive failures stop the poller as offline', async () => {
  const timers = fakeTimers();
  const events = [];
  const poller = createPoller({
    timers,
    tick: async () => {
      throw Object.assign(new Error('boom'), { status: 503 });
    },
    onEvent: (kind) => events.push(kind),
  });
  poller.start(0);
  await timers.advance(0);
  assert.equal(poller.failures, 1);
  assert.deepEqual(events, ['backoff']);
  await timers.advance(1000);
  assert.equal(poller.failures, 2);
  await timers.advance(2000);
  assert.equal(poller.failures, 3);
  assert.equal(poller.stopped, true);
  assert.equal(events.at(-1), 'offline');
  assert.equal(timers.pending, 0, 'an offline poller makes no more requests');
});

test('a dead seat stops the poller at once', async () => {
  const timers = fakeTimers();
  const events = [];
  const poller = createPoller({
    timers,
    tick: async () => {
      throw Object.assign(new Error('unauthorized'), { status: 401 });
    },
    onEvent: (kind) => events.push(kind),
  });
  poller.start(0);
  await timers.advance(0);
  assert.equal(poller.stopped, true);
  assert.deepEqual(events, ['stop'], 'a 401 is terminal, not a backoff');
});

test('a success clears the failure count and a 429 retry-after is honoured', async () => {
  const timers = fakeTimers();
  let fail = true;
  const poller = createPoller({
    timers,
    tick: async () => {
      if (fail) throw Object.assign(new Error('slow down'), { status: 429, retryAfter: 5000 });
    },
  });
  poller.start(0);
  await timers.advance(0);
  assert.equal(poller.failures, 1);
  fail = false;
  await timers.advance(5000);
  assert.equal(poller.failures, 0, 'a good tick clears the count');
});

test('a stale frame from the same room is discarded', () => {
  // A slow poll answer must not put the board back a turn (F-51).
  adopt({ code: 'AAAAA', seat: 0, token: 't', seq: 9, state: { turnNo: 9 } });
  coop.seq = 9;
  adopt({ code: 'AAAAA', seat: 0, token: 't', seq: 4, state: { turnNo: 4 } });
  assert.equal(coop.seq, 9, 'a lower sequence must be ignored');
  assert.equal(coop.state.turnNo, 9, 'and must not overwrite the board');

  // The same answer for a newer sequence is applied.
  adopt({ code: 'AAAAA', seat: 0, token: 't', seq: 10, state: { turnNo: 10 } });
  assert.equal(coop.state.turnNo, 10);

  // A different room is never stale, even at a lower sequence.
  adopt({ code: 'BBBBB', seat: 0, token: 'u', seq: 1, state: { turnNo: 1 } });
  assert.equal(coop.code, 'BBBBB');
  assert.equal(coop.seq, 1);
});

// ---- per-tab seat records (T10) -------------------------------------------
//
// Two tabs of one profile used to share the room-scoped record, so the second tab
// adopted (and could overwrite) the first tab's chair. The record is keyed by a
// sessionStorage tab id now; these tests drive the module the way two tabs do.

function fakeStorage(shared) {
  return {
    getItem: (k) => (shared.has(k) ? shared.get(k) : null),
    setItem: (k, v) => shared.set(k, String(v)),
    removeItem: (k) => shared.delete(k),
  };
}

function withStorage(run) {
  const realLocal = globalThis.localStorage;
  const realSession = globalThis.sessionStorage;
  try {
    return run();
  } finally {
    globalThis.localStorage = realLocal;
    globalThis.sessionStorage = realSession;
  }
}

test('two tabs of one profile keep their own seat record', () => {
  withStorage(() => {
    const shared = new Map();
    const tabA = new Map([['regicide.tab', 'A']]);
    const tabB = new Map([['regicide.tab', 'B']]);
    globalThis.localStorage = fakeStorage(shared);

    // Tab A hosts.
    globalThis.sessionStorage = fakeStorage(tabA);
    reset({ code: 'ABCDE', seat: 0, token: 'tokenA' });
    storeSeat();

    // Tab B opens the same room link: it must not inherit A's chair.
    globalThis.sessionStorage = fakeStorage(tabB);
    reset({ code: 'ABCDE', seat: -1, token: '' });
    assert.equal(loadSeat('ABCDE'), false, 'the second tab adopted the first tab seat');
    reset({ code: 'ABCDE', seat: 1, token: 'tokenB' });
    storeSeat();

    // A reload in each tab still finds that tab's own chair.
    globalThis.sessionStorage = fakeStorage(tabA);
    reset({ code: 'ABCDE', seat: -1, token: '' });
    assert.equal(loadSeat('ABCDE'), true, 'tab A lost its own chair');
    assert.equal(coop.seat, 0);
    assert.equal(coop.token, 'tokenA', 'tab B overwrote tab A seat');

    globalThis.sessionStorage = fakeStorage(tabB);
    reset({ code: 'ABCDE', seat: -1, token: '' });
    assert.equal(loadSeat('ABCDE'), true, 'tab B lost its chair');
    assert.equal(coop.seat, 1);
    assert.equal(coop.token, 'tokenB');

    // Forgetting one tab's chair leaves the other tab's record alone.
    forgetSeat('ABCDE');
    globalThis.sessionStorage = fakeStorage(tabA);
    reset({ code: 'ABCDE', seat: -1, token: '' });
    assert.equal(loadSeat('ABCDE'), true, 'closing tab B forgot tab A seat');
  });
});

test('a covering record belongs to the tab that covered', () => {
  withStorage(() => {
    const shared = new Map();
    const tabA = new Map([['regicide.tab', 'A']]);
    const tabB = new Map([['regicide.tab', 'B']]);
    globalThis.localStorage = fakeStorage(shared);
    shared.set('regicide.room.ABCDE.return.A', JSON.stringify({ seat: 0, token: 'ownA' }));

    globalThis.sessionStorage = fakeStorage(tabB);
    reset({ code: 'ABCDE', seat: 1, token: 'covering' });
    assert.equal(returnSeat(), null, 'tab B inherited tab A covering record');

    globalThis.sessionStorage = fakeStorage(tabA);
    reset({ code: 'ABCDE', seat: 2, token: 'covering' });
    assert.deepEqual(returnSeat(), { seat: 0, token: 'ownA' });
  });
});

test('the pre-T10 room record is adopted once', () => {
  withStorage(() => {
    const shared = new Map();
    const tabA = new Map([['regicide.tab', 'A']]);
    const tabB = new Map([['regicide.tab', 'B']]);
    globalThis.localStorage = fakeStorage(shared);
    shared.set('regicide.room.ABCDE', JSON.stringify({ seat: 0, token: 'legacy' }));

    globalThis.sessionStorage = fakeStorage(tabA);
    reset({ code: 'ABCDE', seat: -1, token: '' });
    assert.equal(loadSeat('ABCDE'), true, 'the upgrade lost the existing chair');
    assert.equal(coop.token, 'legacy');
    assert.equal(shared.has('regicide.room.ABCDE'), false, 'the legacy record was left for a second tab');

    globalThis.sessionStorage = fakeStorage(tabB);
    reset({ code: 'ABCDE', seat: -1, token: '' });
    assert.equal(loadSeat('ABCDE'), false, 'a second tab adopted the migrated chair');
  });
});
