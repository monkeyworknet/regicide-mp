#!/usr/bin/env node
// The co-op phone board, in Gecko, as a contract rather than a pixel baseline.
//
//   node scripts/layout-gecko.mjs --base http://127.0.0.1:8321
//
// A committed geometry baseline is engine-specific by nature; what must hold in every
// engine is structural: one column, no implicit grid tracks, every pile art containing
// its cards, and the hand keeping its width when the peek opens. Those are the properties
// that broke on a real phone, so those are what this checks — in Firefox, at 360 and 390
// CSS pixels, using a same-origin iframe because a Firefox window clamps at ~500.
import { startGecko, frameAt } from './gecko.mjs';

const argv = process.argv.slice(2);
const arg = (name, dflt) => {
  const i = argv.indexOf('--' + name);
  return i >= 0 ? argv[i + 1] : dflt;
};
const BASE = arg('base', 'http://127.0.0.1:8321');
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

const MEASURE = `(() => {
  const board = document.querySelector('#board');
  const cs = getComputedStyle(board);
  const cols = cs.gridTemplateColumns.split(' ').filter(Boolean);
  const rows = cs.gridTemplateRows.split(' ').filter(Boolean);
  // Zero-height rows are normal (an empty peek, an empty log). What must never happen is
  // an AUTO-PLACED item: an item with a named area reports a line, an auto-placed one
  // reports "auto" and is what creates implicit tracks.
  const autoPlaced = [...board.children].filter((el) => {
    const c = getComputedStyle(el);
    if (c.position === 'absolute' || c.position === 'fixed') return false;
    if (c.display === 'contents' || c.display === 'none') return false;
    return c.gridRowStart === 'auto' || c.gridColumnStart === 'auto';
  }).map((el) => el.id || el.className);
  const piles = [...document.querySelectorAll('.pile-art')].map((art) => {
    const ab = art.getBoundingClientRect();
    let worst = null;
    for (const c of art.children) {
      const cb = c.getBoundingClientRect();
      const over = Math.max(cb.width - ab.width, cb.height - ab.height);
      if (worst === null || over > worst) worst = over;
    }
    return { id: art.id, worstOverhang: worst === null ? 0 : Math.round(worst) };
  });
  const hand = document.querySelector('.zone-hand').getBoundingClientRect();
  const de = document.documentElement;
  return {
    cols: cols.length, rows: rows.length, autoPlaced,
    scrollW: de.scrollWidth, viewW: innerWidth,
    handWidth: Math.round(hand.width),
    piles,
  };
})()`;

async function main() {
  const room = await post('/api/rooms', { theme: 'simple' });
  const code = room.code;
  const host = { 'X-Regicide-Seat': `0:${room.token}` };
  const guest = await post(`/api/rooms/${code}/join`, {});
  await post(`/api/rooms/${code}/start`, { expectedSeq: guest.seq ?? room.seq ?? 0 }, host);

  const fx = await startGecko({ width: 1000, height: 900 });
  const failures = [];
  try {
    for (const [w, h] of [[360, 740], [390, 844]]) {
      const f = await frameAt(fx, { url: `${BASE}/?room=${code}`, width: w, height: h });
      // Attach the seat inside the frame so it is a real board, then measure closed…
      await f.evalIn(`localStorage.setItem('regicide.room.${code}', JSON.stringify({ seat: 0, token: ${JSON.stringify(room.token)} }))`);
      await f.evalIn(`location.reload()`);
      await sleep(2600);
      const closed = await f.evalIn(MEASURE);
      if (closed.cols !== 1) failures.push(`${w}: board is ${closed.cols} columns, want 1`);
      if (closed.autoPlaced.length) failures.push(`${w}: auto-placed grid items: ${closed.autoPlaced.join(', ')}`);
      if (closed.scrollW > closed.viewW + 1) failures.push(`${w}: document is ${closed.scrollW}px wide in a ${closed.viewW}px viewport`);
      for (const p of closed.piles) if (p.worstOverhang > 2) failures.push(`${w}: ${p.id} overhangs its box by ${p.worstOverhang}px`);
      // …then with the peek open, which is the case that used to collapse the board.
      await f.evalIn(`(() => { const c = document.querySelectorAll('#hand .card')[2]; if (c) c.click(); return true; })()`);
      await sleep(900);
      const open = await f.evalIn(MEASURE);
      if (open.cols !== closed.cols) failures.push(`${w}: peek changed the grid from ${closed.cols} to ${open.cols} columns`);
      // Opening the peek makes the page taller, a scrollbar appears, and Gecko reports
      // the scrollbar inside innerWidth — so a few pixels of shrink is the scrollbar, not
      // a layout collapse. The collapse this guards against was 314 -> 144 px.
      const closedShare = closed.handWidth / closed.viewW;
      const openShare = open.handWidth / open.viewW;
      if (closedShare < 0.7) failures.push(`${w}: the hand is only ${Math.round(closedShare * 100)}% of the width`);
      if (Math.abs(open.handWidth - closed.handWidth) > 20) failures.push(`${w}: peek changed the hand width ${closed.handWidth} -> ${open.handWidth} px (more than a scrollbar)`);
      console.log(`  ${w}x${h}: ${closed.cols} column, ${closed.autoPlaced.length} auto-placed items, hand ${Math.round(closedShare * 100)}% -> ${Math.round(openShare * 100)}% with the peek open`);
    }
  } finally {
    await fx.quit();
    await fetch(`${BASE}/api/rooms/${code}`, { method: 'DELETE', headers: host }).catch(() => {});
  }
  if (failures.length) {
    for (const f of failures) console.log('  FAIL ' + f);
    process.exit(1);
  }
  console.log('  gecko phone contract holds at both widths');
}

main().catch((e) => {
  console.error('layout-gecko:', e.message);
  process.exit(2);
});
