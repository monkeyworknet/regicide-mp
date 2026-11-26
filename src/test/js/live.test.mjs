// Unit tests for the stream client: the frame parser, the gap repair, and the
// terminal cases. These import the module the browser loads.
import test from 'node:test';
import assert from 'node:assert/strict';

import { createFrameParser, createLive } from '../../regicide/server/web/live.js';

test('the parser reassembles a frame split across chunks', () => {
  const seen = [];
  const feed = createFrameParser((e) => seen.push(e));
  feed('id: 4\nevent: state\nda');
  feed('ta: {"seq":4}\n\n');
  assert.equal(seen.length, 1);
  assert.equal(seen[0].event, 'state');
  assert.equal(seen[0].data.seq, 4);
  assert.equal(seen[0].id, '4');
});

test('the parser handles several frames in one chunk and ignores comments', () => {
  const seen = [];
  const feed = createFrameParser((e) => seen.push(e));
  feed(': ping\n\nevent: presence\ndata: {"seats":[]}\n\nevent: state\ndata: {"seq":2}\n\n');
  assert.equal(seen.length, 2);
  assert.equal(seen[0].event, 'presence');
  assert.equal(seen[1].data.seq, 2);
});

test('a truncated frame is dropped, never half-applied', () => {
  const seen = [];
  const feed = createFrameParser((e) => seen.push(e));
  feed('event: state\ndata: {"seq":2,"state":{"hand":[1,2]\n\n');
  assert.equal(seen.length, 0, 'invalid JSON must not reach the caller');
});

// A fetch stub that answers with one SSE body then closes.
function streamFetch(body, { status = 200 } = {}) {
  const calls = [];
  const fn = async (url, init) => {
    calls.push({ url, init });
    if (status !== 200) return { status, ok: false, body: null };
    const encoder = new TextEncoder();
    const chunks = Array.isArray(body) ? body : [body];
    let i = 0;
    return {
      status: 200,
      ok: true,
      body: {
        getReader() {
          return {
            async read() {
              if (i >= chunks.length) return { done: true, value: undefined };
              const value = encoder.encode(chunks[i++]);
              return { done: false, value };
            },
          };
        },
      },
    };
  };
  return { fn, calls };
}

const settle = () => new Promise((r) => setTimeout(r, 30));

test('state frames reach onState, presence never does', async () => {
  const states = [];
  const presence = [];
  const statuses = [];
  const { fn, calls } = streamFetch([
    'event: state\nid: 1\ndata: {"seq":1,"lobby":true,"room":{"code":"ABCDE"}}\n\n',
    'event: presence\ndata: {"seats":[{"seat":0,"x":0.2,"y":0.3}]}\n\n',
    'event: state\nid: 2\ndata: {"seq":2}\n\n',
  ]);
  const live = createLive({
    url: '/api/rooms/ABCDE/stream',
    headers: () => ({ 'X-Regicide-Seat': '0:tok' }),
    onState: (f) => states.push(f),
    onPresence: (f) => presence.push(f),
    onStatus: (s) => statuses.push(s),
    fetchImpl: fn,
  });
  live.start();
  await settle();
  assert.deepEqual(states.map((s) => s.seq), [1, 2]);
  assert.equal(presence.length, 1, 'presence is routed separately');
  assert.equal(presence[0].seats[0].x, 0.2);
  assert.equal(calls[0].init.headers['X-Regicide-Seat'], '0:tok', 'the seat travels in a header');
  assert.ok(statuses.includes('live'));
  live.stop();
});

test('a skipped sequence asks for a repair before applying', async () => {
  let repairs = 0;
  const states = [];
  const { fn } = streamFetch([
    'event: state\nid: 5\ndata: {"seq":5}\n\n',
    'event: state\nid: 9\ndata: {"seq":9}\n\n',
  ]);
  const live = createLive({
    url: '/x',
    onState: (f) => states.push(f.seq),
    repair: () => {
      repairs += 1;
    },
    fetchImpl: fn,
  });
  live.start();
  await settle();
  assert.deepEqual(states, [5, 9]);
  assert.equal(repairs, 1, 'seq 5 -> 9 is a gap and must be repaired');
  live.stop();
});

test('a dead credential ends the stream instead of reconnecting forever', async () => {
  const statuses = [];
  const { fn } = streamFetch('', { status: 401 });
  const live = createLive({
    url: '/x',
    onStatus: (s) => statuses.push(s),
    fetchImpl: fn,
  });
  live.start();
  await settle();
  assert.equal(live.stopped, true);
  // 'ended', not 'offline': the tab must show the lost-seat screen, not a chip that
  // can never recover (playtest 3, seat identity).
  assert.deepEqual(statuses, ['ended']);
});

test('a bye closes the stream and says why', async () => {
  const statuses = [];
  const { fn } = streamFetch('event: bye\ndata: {"reason":"cancelled"}\n\n');
  const live = createLive({
    url: '/x',
    onStatus: (s) => statuses.push(s),
    fetchImpl: fn,
  });
  live.start();
  await settle();
  assert.equal(live.stopped, true);
  assert.ok(statuses.includes('ended'));
});

test('a ten-minute soft close reconnects and keeps the seat', async () => {
  // The server ends every stream after streamSoftLife with reason "reconnect". That
  // must not be terminal: before this the client reported 'ended', the room forgot
  // the seat, and the board froze while the table played on.
  let calls = 0;
  const statuses = [];
  const encoder = new TextEncoder();
  const fn = async () => {
    calls += 1;
    const body =
      calls === 1
        ? 'event: bye\ndata: {"reason":"reconnect"}\n\n'
        : 'event: state\ndata: {"seq":2,"room":{"code":"AAAA"}}\n\n';
    let sent = false;
    return {
      status: 200,
      ok: true,
      body: {
        getReader: () => ({
          read: async () => {
            if (sent) return { done: true, value: undefined };
            sent = true;
            return { done: false, value: encoder.encode(body) };
          },
        }),
      },
    };
  };
  const live = createLive({ url: '/x', onStatus: (s) => statuses.push(s), fetchImpl: fn });
  live.start();
  await new Promise((r) => setTimeout(r, 400));
  assert.ok(calls >= 2, 'the stream must reconnect after a soft close');
  assert.notEqual(live.stopped, true, 'a soft close must not end the seat');
  assert.ok(!statuses.includes('ended'), 'a soft close must never report ended');
  live.stop();
});

test('a real bye is still terminal', async () => {
  const statuses = [];
  const { fn } = streamFetch('event: bye\ndata: {"reason":"shutdown"}\n\n');
  const live = createLive({ url: '/x', onStatus: (s) => statuses.push(s), fetchImpl: fn });
  live.start();
  await settle();
  assert.equal(live.stopped, true);
});

test('a network failure reports offline and retries with backoff', async () => {
  let attempts = 0;
  const statuses = [];
  const fn = async () => {
    attempts += 1;
    throw new Error('network down');
  };
  const live = createLive({ url: '/x', onStatus: (s) => statuses.push(s), fetchImpl: fn });
  live.start();
  await settle();
  assert.equal(attempts, 1);
  assert.ok(statuses.includes('offline'));
  assert.equal(live.stopped, false, 'a transient failure keeps trying');
  live.stop();
});

test('a silent stream is declared offline by the watchdog', async () => {
  // Campaign B13: a network drop left the SSE reader hanging for minutes, so the board
  // froze with no connection chip. The server's presence heartbeat is every 60 ms; a
  // stream that says nothing for idleMs is dead and must surface as offline.
  const statuses = [];
  const live = createLive({
    url: '/api/rooms/AAAAA/stream',
    headers: () => ({}),
    idleMs: 20,
    fetchImpl: async () => ({
      ok: true,
      status: 200,
      body: { getReader: () => ({ read: () => new Promise(() => {}) }) },
    }),
    onStatus: (s) => statuses.push(s),
  });
  live.start();
  await new Promise((r) => setTimeout(r, 70));
  live.stop();
  assert.ok(statuses.includes('live'), JSON.stringify(statuses));
  assert.ok(statuses.includes('offline'), JSON.stringify(statuses));
});

test('the watchdog does not fire after stop', async () => {
  const statuses = [];
  const live = createLive({
    url: '/api/rooms/AAAAA/stream',
    headers: () => ({}),
    idleMs: 10,
    fetchImpl: async () => ({
      ok: true,
      status: 200,
      body: { getReader: () => ({ read: () => new Promise(() => {}) }) },
    }),
    onStatus: (s) => statuses.push(s),
  });
  live.start();
  live.stop();
  await new Promise((r) => setTimeout(r, 40));
  assert.ok(!statuses.includes('offline'), JSON.stringify(statuses));
});
