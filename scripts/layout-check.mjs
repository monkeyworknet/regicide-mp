#!/usr/bin/env node
// Layout regression check for the solo board.
//
// Why this exists: the co-op table and the solo board share one DOM. A CSS rule
// written for a room can silently destroy the solo layout — it happened once, and
// both smokes passed because neither asserted anything about *layout*. Screenshot
// diffing would catch it but flakes on fonts and antialiasing, so this measures
// structure instead: the bounding box and a few computed properties of every
// landmark on the board, at both target widths, compared against a committed
// baseline with a pixel tolerance.
//
//   node scripts/layout-check.mjs                 # compare against the baseline
//   node scripts/layout-check.mjs --write         # rewrite the baseline
//   BASE=http://127.0.0.1:8321 node scripts/...   # measure a server already running
//
// Exit: 0 pass, 1 regression, 2 could not measure.
import { spawn } from 'node:child_process';
import net from 'node:net';
import { mkdtempSync, readFileSync, writeFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join, dirname } from 'node:path';
import { fileURLToPath } from 'node:url';

const HERE = dirname(fileURLToPath(import.meta.url));
const ROOT = join(HERE, '..');
const BASELINE = join(ROOT, 'docs/layout-baseline.json');
const TOLERANCE = 8; // px; the board is laid out with fixed sizes and a grid, so
                     // anything bigger than font rounding is a real change.
const VIEWPORTS = [
  { name: 'desktop', width: 1440, height: 900 },
  { name: 'phone', width: 390, height: 844 },
  // A reference laptop viewport: the solo board was squeezed into a 1150px strip here
  // because a co-op rail rule leaked into solo (playtest 3, note 2).
  { name: 'wide', width: 2560, height: 1600 },
];

// Landmarks, not every node: the board's areas, the piles, the enemy, the hand and
// the action bar. Each is measured as a box plus the properties that decide whether
// it is a box at all (display) and where the grid puts it (grid-area).
const LANDMARKS = [
  { name: 'board', sel: '#board' },
  { name: 'topbar', sel: '.topbar' },
  { name: 'cloth', sel: '#cloth' },
  { name: 'enemy', sel: '.zone-enemy' },
  { name: 'royal', sel: '#enemy-card' },
  { name: 'stats', sel: '.enemy-stats' },
  { name: 'seats', sel: '#seats' },
  { name: 'piles', sel: '.zone-piles' },
  { name: 'pile-side', sel: '#pile-side' },
  { name: 'tavern', sel: '#pile-tavern' },
  { name: 'discard', sel: '#pile-discard' },
  { name: 'jesters', sel: '#pile-jesters' },
  { name: 'hand', sel: '#hand' },
  { name: 'actions', sel: '#actionbar' },
];

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

class Page {
  constructor(ws) {
    this.ws = ws;
    this.id = 0;
    this.pending = new Map();
  }
  static async attach(target) {
    const ws = new WebSocket(target.webSocketDebuggerUrl);
    const page = new Page(ws);
    ws.addEventListener('message', (ev) => {
      const m = JSON.parse(ev.data);
      const slot = m.id && page.pending.get(m.id);
      if (!slot) return;
      page.pending.delete(m.id);
      if (m.error) slot.reject(new Error(JSON.stringify(m.error)));
      else slot.resolve(m.result);
    });
    await new Promise((res, rej) => {
      ws.addEventListener('open', res, { once: true });
      ws.addEventListener('error', rej, { once: true });
    });
    await page.send('Runtime.enable');
    await page.send('Page.enable');
    return page;
  }
  send(method, params = {}) {
    const id = ++this.id;
    this.ws.send(JSON.stringify({ id, method, params }));
    return new Promise((resolve, reject) => this.pending.set(id, { resolve, reject }));
  }
  async eval(expression) {
    const r = await this.send('Runtime.evaluate', {
      expression,
      awaitPromise: true,
      returnByValue: true,
    });
    if (r.exceptionDetails) {
      throw new Error(r.exceptionDetails.exception?.description || 'evaluate failed');
    }
    return r.result.value;
  }
}

// freePort asks the kernel for an unused port. The gate runs its browser stages in
// parallel lanes, so a guessed range is a collision waiting to happen.
function freePort() {
  return new Promise((resolve, reject) => {
    const srv = net.createServer();
    srv.once('error', reject);
    srv.listen(0, '127.0.0.1', () => {
      const { port } = srv.address();
      srv.close(() => resolve(port));
    });
  });
}

async function startServer() {
  const port = await freePort();
  const data = mkdtempSync(join(tmpdir(), 'layout-db-'));
  const proc = spawn(join(ROOT, 'bin/regicide'), ['-addr', `127.0.0.1:${port}`, '-data', data], {
    cwd: ROOT,
    stdio: 'ignore',
    detached: true,
  });
  return { port, proc, data, base: `http://127.0.0.1:${port}` };
}

async function waitForHealth(base, tries = 100) {
  for (let i = 0; i < tries; i++) {
    try {
      const r = await fetch(base + '/api/health');
      if (r.ok) return true;
    } catch {
      /* not up yet */
    }
    await sleep(100);
  }
  return false;
}

async function launchChromium(userDir) {
  const proc = spawn(
    'chromium',
    [
      '--headless=new',
      '--no-sandbox',
      '--disable-gpu',
      '--hide-scrollbars',
      '--remote-debugging-port=0',
      `--user-data-dir=${userDir}`,
      'about:blank',
    ],
    { stdio: ['ignore', 'ignore', 'pipe'], detached: true }
  );
  // The port is written to DevToolsActivePort once the browser is listening.
  const portFile = join(userDir, 'DevToolsActivePort');
  for (let i = 0; i < 200; i++) {
    await sleep(100);
    try {
      const txt = readFileSync(portFile, 'utf8').split('\n');
      if (txt[0]) return { proc, port: Number(txt[0]) };
    } catch {
      /* not written yet */
    }
  }
  throw new Error('chromium did not open a debugging port');
}

async function measure(page, base, vp) {
  await page.send('Emulation.setDeviceMetricsOverride', {
    width: vp.width,
    height: vp.height,
    deviceScaleFactor: 1,
    mobile: vp.width < 600,
  });
  await page.send('Page.navigate', { url: base + '/?test=none' });
  await sleep(1200);
  // Start a solo run from the setup screen with a FIXED seed. The enemy decides how
  // many badge and note lines the board shows, which changes the height of the whole
  // stack — an unpinned deal moved the phone layout by 30 px between two runs and made
  // the comparison meaningless.
  const started = await page.eval(`(() => {
    const seedToggle = document.querySelector('#btn-seed-toggle');
    const seedInput = document.querySelector('#seed-input');
    const panel = document.querySelector('#seed-panel');
    if (panel && panel.hidden && seedToggle) seedToggle.click();
    if (seedInput) {
      seedInput.value = 'layout-check-1';
      seedInput.dispatchEvent(new Event('input', { bubbles: true }));
    }
    const btn = [...document.querySelectorAll('#setup-form button')]
      .find((b) => /start/i.test(b.textContent));
    if (!btn) return false;
    btn.click();
    return true;
  })()`);
  if (!started) throw new Error('setup screen had no start button');
  for (let i = 0; i < 40; i++) {
    if (await page.eval(`document.querySelectorAll('#hand .card').length > 0`)) break;
    await sleep(150);
  }
  await sleep(400);
  return page.eval(`(() => {
    const out = {};
    const landmarks = ${JSON.stringify(LANDMARKS)};
    for (const lm of landmarks) {
      const el = document.querySelector(lm.sel);
      if (!el) { out[lm.name] = { missing: true }; continue; }
      const b = el.getBoundingClientRect();
      const c = getComputedStyle(el);
      out[lm.name] = {
        x: Math.round(b.x), y: Math.round(b.y),
        w: Math.round(b.width), h: Math.round(b.height),
        display: c.display,
        area: c.gridArea || 'none',
      };
    }
    out['__counts'] = { hand: document.querySelectorAll('#hand .card').length,
                        coop: document.body.classList.contains('is-coop') };
    const cards = [...document.querySelectorAll('#hand .card')];
    if (cards.length) {
      // The union of every card, not first-to-last: a wrapped hand puts the last card
      // on a second, centred row, and reading it as the right edge made a centred phone
      // hand look 44 px off (playtest 3, note 3B).
      const boxes = cards.map((c) => c.getBoundingClientRect());
      const x = Math.min(...boxes.map((b) => b.x));
      const right = Math.max(...boxes.map((b) => b.right));
      out['__handGroup'] = { x: Math.round(x), right: Math.round(right), w: Math.round(right - x) };
    } else {
      out['__handGroup'] = null;
    }
    out['__view'] = { w: window.innerWidth, h: window.innerHeight };
    return out;
  })()`);
}

// properties are the rules the baseline cannot state: a phantom rail, a hand pinned to
// the left, a bar wider than the board. They run on every measurement, so a rewritable
// baseline can never bless the bugs these exist to catch (playtest 3, notes 2 and 3B).
function properties(actual) {
  const problems = [];
  for (const vp of VIEWPORTS) {
    const row = actual[vp.name];
    if (!row) continue;
    const seats = row.seats;
    if (!seats || seats.display !== 'none' || seats.w !== 0) {
      problems.push(`${vp.name}/seats: the solo board still pays for a rail (${seats ? `${seats.display} ${seats.w}px` : 'missing'})`);
    }
    const board = row.board;
    const hand = row['__handGroup'];
    if (board && hand) {
      const cardCentre = hand.x + hand.w / 2;
      const boardCentre = board.x + board.w / 2;
      if (Math.abs(cardCentre - boardCentre) > 4) {
        problems.push(`${vp.name}/hand: cards centred at ${Math.round(cardCentre)}, board centre ${Math.round(boardCentre)}`);
      }
    }
    if (board && row.actions) {
      const barCentre = row.actions.x + row.actions.w / 2;
      const boardCentre = board.x + board.w / 2;
      if (Math.abs(barCentre - boardCentre) > 4) {
        problems.push(`${vp.name}/actions: bar centred at ${Math.round(barCentre)}, board centre ${Math.round(boardCentre)}`);
      }
      if (row.actions.w > board.w + 1) problems.push(`${vp.name}/actions: bar ${row.actions.w}px wider than the ${board.w}px board`);
    }
    // Above the phone breakpoint the royal and the piles are two columns, never a stack.
    if (vp.width >= 1024 && row.enemy && row.piles) {
      if (row.enemy.x >= row.piles.x) problems.push(`${vp.name}: piles start at ${row.piles.x}, enemy at ${row.enemy.x}`);
      if (row.enemy.right > row.piles.x + 1) problems.push(`${vp.name}: enemy overlaps the piles (${row.enemy.right} > ${row.piles.x})`);
      const overlapY = row.enemy.y < row.piles.y + row.piles.h && row.piles.y < row.enemy.y + row.enemy.h;
      if (!overlapY) problems.push(`${vp.name}: enemy and piles are stacked, not side by side`);
    }
  }
  return problems;
}

function compare(baseline, actual) {
  const problems = [];
  for (const vp of VIEWPORTS) {
    const was = baseline[vp.name];
    const now = actual[vp.name];
    if (!was) {
      problems.push(`${vp.name}: no baseline recorded`);
      continue;
    }
    for (const key of Object.keys(was)) {
      if (key === '__counts') continue;
      const a = was[key];
      const b = now[key];
      if (!b || b.missing) {
        problems.push(`${vp.name}/${key}: disappeared`);
        continue;
      }
      if (a.display !== b.display) {
        problems.push(`${vp.name}/${key}: display ${a.display} -> ${b.display}`);
      }
      if (a.area !== b.area) {
        problems.push(`${vp.name}/${key}: grid-area ${a.area} -> ${b.area}`);
      }
      for (const side of ['x', 'y', 'w', 'h']) {
        if (Math.abs(a[side] - b[side]) > TOLERANCE) {
          problems.push(`${vp.name}/${key}: ${side} ${a[side]} -> ${b[side]}`);
        }
      }
    }
  }
  return problems;
}

async function main() {
  const write = process.argv.includes('--write');
  const external = process.env.BASE;
  let server;
  if (!external) {
    server = await startServer();
    const up = await waitForHealth(server.base);
    if (!up) throw new Error('server did not answer /api/health');
  }
  const base = external || server.base;
  const userDir = mkdtempSync(join(tmpdir(), 'layout-chrome-'));
  const browser = await launchChromium(userDir);
  const cleanup = () => {
    try { browser.proc.kill('SIGKILL'); } catch {}
    try { server?.proc.kill('SIGKILL'); } catch {}
    for (const dir of [userDir, server?.data].filter(Boolean)) {
      // A killed browser still flushes its profile for a moment: remove with a short
      // retry, or every run leaves a profile directory in /tmp.
      for (let i = 0; i < 10; i++) {
        try { rmSync(dir, { recursive: true, force: true }); break; } catch { /* retry */ }
      }
    }
  };
  let code = 2;
  try {
    const list = await (await fetch(`http://127.0.0.1:${browser.port}/json/list`)).json();
    const target = list.find((t) => t.type === 'page');
    const page = await Page.attach(target);
    const actual = {};
    for (const vp of VIEWPORTS) actual[vp.name] = await measure(page, base, vp);

    const bad = properties(actual);
    if (bad.length) {
      console.log('layout checks failed:');
      for (const p of bad) console.log('  ' + p);
      code = 1;
    }

    if (write) {
      writeFileSync(BASELINE, JSON.stringify(actual, null, 2) + '\n');
      console.log(`baseline written to ${BASELINE.replace(ROOT + '/', '')}`);
      for (const vp of VIEWPORTS) {
        const row = actual[vp.name];
        console.log(`  ${vp.name}: board ${row.board.w}x${row.board.h}, hand ${row.__counts.hand} cards`);
      }
      if (bad.length === 0) code = 0;
    } else {
      const baseline = JSON.parse(readFileSync(BASELINE, 'utf8'));
      const problems = compare(baseline, actual);
      if (problems.length === 0) {
        console.log('layout matches the baseline at both widths');
        code = 0;
      } else {
        console.log(`layout regressed (${problems.length}):`);
        for (const p of problems) console.log('  ' + p);
        code = 1;
      }
    }
  } finally {
    cleanup();
  }
  process.exit(code);
}

main().catch((e) => {
  console.error('layout check could not measure:', e.message);
  process.exit(2);
});
