#!/usr/bin/env node
// Soak: one room, four seats and three spectators, real browsers and real streams.
//
//   node scripts/soak.mjs --base http://127.0.0.1:8321 --minutes 3
//
// It samples goroutines-equivalent proxies from the server's own health output (session
// count), the process RSS, and the number of frames each stream received, then prints a
// table. The point is flatness: a table that leaks a goroutine or a subscriber per turn
// shows up as a line that climbs.
import { spawn } from 'node:child_process';
import { readFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';

const argv = process.argv.slice(2);
const arg = (name, dflt) => {
  const i = argv.indexOf('--' + name);
  return i >= 0 ? argv[i + 1] : dflt;
};
const BASE = arg('base', 'http://127.0.0.1:8321');
const MINUTES = Number(arg('minutes', 3));
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

const post = async (path, body, headers) => {
  const res = await fetch(BASE + path, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', ...(headers || {}) },
    body: JSON.stringify(body || {}),
  });
  const text = await res.text();
  if (!res.ok) throw new Error(`${path} -> ${res.status} ${text.slice(0, 120)}`);
  return text ? JSON.parse(text) : null;
};

async function main() {
  const room = await post('/api/rooms', { theme: 'simple' });
  const code = room.code;
  const host = { 'X-Regicide-Seat': `0:${room.token}` };
  const seats = [{ seat: 0, token: room.token }];
  let seq = room.seq ?? 0;
  for (let i = 1; i < 4; i++) {
    const j = await post(`/api/rooms/${code}/join`, {});
    seq = j.seq ?? seq;
    seats.push({ seat: j.seat, token: j.token });
  }
  await post(`/api/rooms/${code}/start`, { expectedSeq: seq }, host);
  await post(`/api/rooms/${code}/watching`, { allowed: true, hands: false }, host);

  // Three spectators, each with its own watch id and its own stream.
  const watchers = [];
  for (let i = 0; i < 3; i++) {
    const w = await post(`/api/rooms/${code}/watch`, {});
    watchers.push(w.watchId);
  }

  // Four seat streams and three watcher streams, read by plain fetch (the same path the
  // browser takes), counting frames.
  const streams = [];
  const open = async (label, headers) => {
    const ctl = new AbortController();
    const state = { label, frames: 0, bytes: 0, closed: false };
    streams.push(state);
    (async () => {
      try {
        const res = await fetch(`${BASE}/api/rooms/${code}/stream`, {
          headers: { Accept: 'text/event-stream', ...headers() },
          signal: ctl.signal,
        });
        const reader = res.body.getReader();
        const dec = new TextDecoder();
        for (;;) {
          const { value, done } = await reader.read();
          if (done) break;
          state.bytes += value.length;
          state.frames += (dec.decode(value, { stream: true }).match(/event: /g) || []).length;
        }
      } catch {
        /* stopped */
      } finally {
        state.closed = true;
      }
    })();
    return { ctl, state };
  };
  for (const s of seats) await open(`seat${s.seat}`, () => ({ 'X-Regicide-Seat': `${s.seat}:${s.token}` }));
  for (let i = 0; i < watchers.length; i++) await open(`watch${i}`, () => ({ 'X-Regicide-Watch': watchers[i] }));

  const pid = Number(arg('pid', 0));
  const rss = () => {
    if (!pid) return 0;
    try {
      const s = readFileSync(`/proc/${pid}/status`, 'utf8');
      return Number((/VmRSS:\s+(\d+)/.exec(s) || [])[1] || 0) / 1024;
    } catch {
      return 0;
    }
  };

  const samples = [];
  const started = Date.now();
  const total = MINUTES * 60 * 1000;
  let turn = 0;
  while (Date.now() - started < total) {
    // Keep the table moving: the active seat plays whatever the server offers.
    const seat = seats[turn % seats.length];
    turn++;
    try {
      const view = await (await fetch(`${BASE}/api/rooms/${code}`, {
        headers: { 'X-Regicide-Seat': `${seat.seat}:${seat.token}` },
      })).json();
      const st = view.state || {};
      const legal = (st.legal || []).find((a) => a.type === 'play') ||
        (st.legal || []).find((a) => a.type === 'pay_damage') ||
        (st.legal || [])[0];
      if (view.state && view.state.activeSeat === seat.seat && legal) {
        await post(`/api/rooms/${code}/actions`, {
          actionId: `soak-${Date.now()}-${turn}`,
          expectedSeq: view.seq,
          action: { type: legal.type, cardIds: legal.cardIds },
        }, { 'X-Regicide-Seat': `${seat.seat}:${seat.token}` }).catch(() => {});
      }
    } catch {
      /* a refused turn is fine: the soak measures resources, not play */
    }
    await sleep(500);
    if (samples.length < (Date.now() - started) / 10000) {
      const health = await (await fetch(BASE + '/api/health')).json();
      samples.push({
        t: Math.round((Date.now() - started) / 1000),
        sessions: health.sessions,
        rssMB: Math.round(rss()),
        frames: streams.reduce((n, s) => n + s.frames, 0),
      });
    }
  }

  console.log('  t(s)  sessions  rss(MB)  frames');
  for (const s of samples) console.log(`  ${String(s.t).padStart(4)}  ${String(s.sessions).padStart(8)}  ${String(s.rssMB).padStart(7)}  ${String(s.frames).padStart(6)}`);
  const openCount = streams.filter((s) => !s.closed).length;
  console.log(`streams still open: ${openCount} of ${streams.length}`);
  for (const s of streams) s.ctl?.abort?.();
  console.log(`frames per stream: ${streams.map((s) => `${s.label}=${s.frames}`).join(' ')}`);
  await fetch(`${BASE}/api/rooms/${code}`, { method: 'DELETE', headers: host }).catch(() => {});
}

main().catch((e) => {
  console.error('soak failed:', e.message);
  process.exit(1);
});
