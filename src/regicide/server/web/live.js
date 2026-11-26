// live.js — the client half of the presence stream (D-34, D-49).
//
// One `text/event-stream` per tab carries state and presence; actions go
// up as POSTs. This is deliberately a `fetch` reader and not `EventSource`:
// EventSource cannot set request headers, and the seat token travels in
// `X-Regicide-Seat`. Putting a credential in the query string to suit the browser API
// would leak it into logs and referrers, so the parser is 40 lines instead (D-49).
//
// The contract with the caller is small:
//   onState(frame)     a state frame; frame.seq is the table's sequence
//   onPresence(frame)  a presence frame; never a render, never app.state
//   onStatus(status)   'live' | 'offline' | 'ended'
//   repair()           called when a frame skips ahead, so the caller can resync

const BACKOFF = [1000, 2000, 4000, 8000, 15000];

// parseFrames turns a byte stream into complete SSE events. It is incremental: one
// network chunk can hold half a frame, and a frame can span chunks.
// maxFrameBuffer bounds one frame. A server that never sends the blank line would
// otherwise grow this string for as long as the tab is open (F-51); a frame that big is
// not a state frame, so the buffer is dropped instead of kept.
const maxFrameBuffer = 1 << 20;

export function createFrameParser(onEvent) {
  let buffer = '';
  return (chunk) => {
    buffer += chunk;
    if (buffer.length > maxFrameBuffer) buffer = '';
    for (;;) {
      const cut = buffer.indexOf('\n\n');
      if (cut < 0) break;
      const raw = buffer.slice(0, cut);
      buffer = buffer.slice(cut + 2);
      let id = '';
      let event = 'message';
      const data = [];
      for (const line of raw.split('\n')) {
        if (line.startsWith(':')) continue; // a comment: the ping
        if (line.startsWith('id: ')) id = line.slice(4);
        else if (line.startsWith('event: ')) event = line.slice(7);
        else if (line.startsWith('data: ')) data.push(line.slice(6));
      }
      if (!data.length) continue;
      let payload = null;
      try {
        payload = JSON.parse(data.join('\n'));
      } catch {
        continue; // a truncated frame is dropped, never half-applied
      }
      onEvent({ id, event, data: payload });
    }
  };
}

// createLive keeps one stream open for as long as the tab is on the table.
export function createLive({
  url,
  headers = () => ({}),
  onState,
  onPresence,
  onStatus,
  repair,
  fetchImpl = null,
  signal = null,
  // idleMs is how long a stream may be silent before it is declared offline. The
  // server sends a presence heartbeat every 60 ms, so silence is a dead stream, not a
  // quiet table. Without this a network drop froze the board with no chip: the fetch
  // reader can hang for minutes before it rejects (campaign B13).
  idleMs = 12000,
}) {
  const doFetch = fetchImpl || ((u, init) => fetch(u, init));
  let controller = null;
  let stopped = false;
  let attempt = 0;
  let timer = 0;
  let lastSeq = 0;
  let watchdog = 0;

  const status = (s) => onStatus && onStatus(s);

  function clearWatchdog() {
    if (watchdog) clearTimeout(watchdog);
    watchdog = 0;
  }

  // armWatchdog restarts the silence timer. Every parsed frame (state, presence or
  // bye) is a sign of life; a stream that has sent nothing for idleMs is dead.
  function armWatchdog() {
    if (stopped) return;
    clearWatchdog();
    watchdog = setTimeout(() => {
      watchdog = 0;
      if (stopped) return;
      status('offline');
      try {
        controller.abort();
      } catch {
        /* already gone */
      }
      schedule();
    }, idleMs);
    // Node's test runner keeps the process alive for a pending timer; the browser has
    // no unref. A live stream that is never stopped must not hold a test for 12 s.
    if (watchdog && typeof watchdog.unref === 'function') watchdog.unref();
  }

  // softClose ends this connection without ending the seat: the reader is released,
  // no bye is reported as terminal, and the loop reconnects at once with the same
  // headers, so lastSeq, the backoff and the caller's state all survive.
  function softClose() {
    if (stopped) return;
    clearWatchdog();
    if (timer) clearTimeout(timer);
    timer = 0;
    attempt = 0;
    try {
      controller.abort();
    } catch {
      /* already gone */
    }
    timer = setTimeout(connect, 250);
  }

  function schedule() {
    if (stopped) return;
    const wait = BACKOFF[Math.min(attempt, BACKOFF.length - 1)];
    attempt += 1;
    timer = setTimeout(connect, wait);
  }

  async function connect() {
    if (stopped) return;
    controller = new AbortController();
    const onAbort = () => controller.abort();
    if (signal) {
      if (signal.aborted) return stop();
      signal.addEventListener('abort', onAbort, { once: true });
    }
    try {
      const res = await doFetch(url, {
        method: 'GET',
        headers: { Accept: 'text/event-stream', ...headers() },
        signal: controller.signal,
        cache: 'no-store',
      });
      if (res.status === 401 || res.status === 403 || res.status === 404) {
        // Terminal: the credential is gone (the seat was taken, the watch was switched
        // off) or the room is. Reporting a dead credential as "offline" left the board
        // frozen behind a chip that could never recover and never offered the
        // lost-seat screen — the stream's version of the playtest-3 seat incident.
        stopped = true;
        status('ended');
        return;
      }
      if (!res.ok || !res.body) {
        schedule();
        status('offline');
        return;
      }
      attempt = 0;
      status('live');
      armWatchdog();
      const reader = res.body.getReader();
      const decoder = new TextDecoder();
      const feed = createFrameParser(({ id, event, data }) => {
        armWatchdog();
        if (event === 'state') {
          const seq = Number(data.seq) || 0;
          // A jump means frames were missed: ask for a snapshot rather than apply a
          // state that skipped an action.
          if (seq > lastSeq + 1 && lastSeq > 0 && repair) repair();
          lastSeq = seq;
          onState && onState(data);
        } else if (event === 'presence') {
          onPresence && onPresence(data);
        } else if (event === 'bye') {
          const reason = (data && data.reason) || '';
          // The server ends every stream after ten minutes and expects the client to
          // come straight back ("reconnect"). Treating that as terminal forgot the
          // seat, wiped its token and froze the board while the table played on: a
          // twelve-royal game could never be finished.
          if (reason === 'reconnect') {
            softClose();
            return;
          }
          stopped = true;
          status(reason === 'shutdown' ? 'offline' : 'ended');
        }
      });
      for (;;) {
        const { value, done } = await reader.read();
        if (done) break;
        feed(decoder.decode(value, { stream: true }));
      }
      // The server closes a stream every ten minutes on purpose; reconnect at once
      // rather than after a backoff.
      attempt = 0;
      if (!stopped) {
        status('offline');
        timer = setTimeout(connect, 250);
      }
    } catch (err) {
      if (stopped || (err && err.name === 'AbortError')) return;
      status('offline');
      schedule();
    }
  }

  function stop() {
    stopped = true;
    clearWatchdog();
    if (timer) clearTimeout(timer);
    if (controller) controller.abort();
  }

  return {
    start() {
      // A second start() used to run a second reader on the same callbacks.
      if (timer) clearTimeout(timer);
      if (controller) {
        try {
          controller.abort();
        } catch {
          /* already gone */
        }
      }
      timer = 0;
      stopped = false;
      attempt = 0;
      connect();
      return this;
    },
    stop,
    get lastSeq() {
      return lastSeq;
    },
    get stopped() {
      return stopped;
    },
  };
}
