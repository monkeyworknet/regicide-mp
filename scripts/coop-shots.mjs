#!/usr/bin/env node
// Co-op board screenshots and pile geometry, for the playtest notes that were about
// what the table looked like rather than what it did.
//
// Why this exists: two playtest notes were visual (the second tavern back printed over
// the label, the empty discard slot overflowed its art box) and neither had a test.
// The board is measured as boxes, not compared as pixels, so a font change cannot make
// it flake — and a screenshot is still written for a human to look at.
//
//   node scripts/coop-shots.mjs                 # own server, temp data dir
//   BASE=http://127.0.0.1:8321 node scripts/coop-shots.mjs
//
// Exit: 0 pass, 1 a pile assertion failed, 2 could not measure.
import { spawn } from 'node:child_process';
import net from 'node:net';
import { mkdtempSync, mkdirSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join, dirname } from 'node:path';
import { fileURLToPath } from 'node:url';

const HERE = dirname(fileURLToPath(import.meta.url));
const ROOT = join(HERE, '..');
const SHOTS = join(ROOT, 'artifacts/screens');
const VIEWPORTS = [
  { name: '390', width: 390, height: 844 },
  { name: '1024', width: 1024, height: 768 },
  { name: '1440', width: 1440, height: 900 },
  { name: '1920', width: 1920, height: 1080 },
  // A reference laptop viewport. The table used to stay 1200px wide here, with the stats
  // stacked and the piles 818px from the royal (playtest 3, note 4).
  { name: '2560', width: 2560, height: 1600 },
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
    const r = await this.send('Runtime.evaluate', { expression, awaitPromise: true, returnByValue: true });
    if (r.exceptionDetails) throw new Error(r.exceptionDetails.exception?.description || 'evaluate failed');
    return r.result.value;
  }
  async goto(url) {
    await this.send('Page.navigate', { url });
    for (let i = 0; i < 100; i++) {
      await sleep(100);
      const ready = await this.eval('document.readyState').catch(() => '');
      if (ready === 'complete') return;
    }
  }
  async viewport(width, height) {
    await this.send('Emulation.setDeviceMetricsOverride', { width, height, deviceScaleFactor: 1, mobile: width < 600 });
  }
  async shot(path) {
    const r = await this.send('Page.captureScreenshot', { format: 'png' });
    writeFileSync(path, Buffer.from(r.data, 'base64'));
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
  const data = mkdtempSync(join(tmpdir(), 'coop-shot-'));
  const proc = spawn(join(ROOT, 'bin/regicide'), ['-addr', `127.0.0.1:${port}`, '-data', data], {
    cwd: ROOT, stdio: 'ignore', detached: true,
  });
  return { proc, data, base: `http://127.0.0.1:${port}` };
}

async function waitForHealth(base, tries = 100) {
  for (let i = 0; i < tries; i++) {
    try {
      const r = await fetch(base + '/api/health');
      if (r.ok) return true;
    } catch { /* not up yet */ }
    await sleep(100);
  }
  return false;
}

async function launchChromium(userDir) {
  const proc = spawn('chromium', [
    '--headless=new', '--no-sandbox', '--disable-gpu', '--hide-scrollbars',
    '--remote-debugging-port=0', `--user-data-dir=${userDir}`, 'about:blank',
  ], { stdio: ['ignore', 'ignore', 'pipe'], detached: true });
  const portFile = join(userDir, 'DevToolsActivePort');
  for (let i = 0; i < 200; i++) {
    await sleep(100);
    try {
      const txt = readFileSync(portFile, 'utf8').split('\n');
      if (txt[0] && txt[1]) return { proc, port: Number(txt[0]) };
    } catch { /* not yet */ }
  }
  throw new Error('chromium did not open a devtools port');
}

async function api(base, method, path, body, seat) {
  const headers = { 'Content-Type': 'application/json' };
  if (seat) headers['X-Regicide-Seat'] = `${seat.seat}:${seat.token}`;
  const r = await fetch(base + path, { method, headers, body: body === undefined ? undefined : JSON.stringify(body) });
  const text = await r.text();
  const json = text ? JSON.parse(text) : {};
  if (!r.ok) throw new Error(`${method} ${path}: ${r.status} ${text}`);
  return json;
}

// seatTable opens a two-seat room. The lobby is captured before the deal, because the
// host's table settings and the kick control only exist there.
async function seatTable(base) {
  const room = await api(base, 'POST', '/api/rooms', { theme: 'official' });
  const guest = await api(base, 'POST', `/api/rooms/${room.code}/join`);
  return {
    code: room.code,
    host: { seat: room.seat, token: room.token },
    guest: { seat: guest.seat, token: guest.token },
    // The deal is conditional on the lobby this client saw (T5).
    deal: () => api(base, 'POST', `/api/rooms/${room.code}/start`, { expectedSeq: guest.seq }, { seat: room.seat, token: room.token }),
  };
}

// pileGeometry reports the boxes the two playtest visuals were about.
const GEOMETRY = `(() => {
  const box = (sel) => {
    const el = document.querySelector(sel);
    if (!el) return null;
    const r = el.getBoundingClientRect();
    return { x: r.x, y: r.y, w: r.width, h: r.height, bottom: r.bottom, right: r.right };
  };
  const tavern = document.querySelector('#tavern-art');
  const discard = document.querySelector('#discard-art');
  const cards = [...document.querySelectorAll('#hand .card')];
  const handGroup = cards.length
    ? (() => {
        const boxes = cards.map((c) => c.getBoundingClientRect());
        const x = Math.min(...boxes.map((b) => b.x));
        const right = Math.max(...boxes.map((b) => b.right));
        return { x, right, w: right - x };
      })()
    : null;
  return {
    tavern: box('#tavern-art'),
    discard: box('#discard-art'),
    board: box('#board'),
    royalCol: box('.zone-enemy'),
    royal: box('#enemy-card'),
    hp: box('.stat-hp'),
    attack: box('.stat-attack'),
    handGroup,
    hand: box('#hand'),
    tavernBacks: tavern ? tavern.querySelectorAll('.card-back').length : -1,
    tavernLabel: box('#pile-tavern .pile-label'),
    tavernBack: box('#tavern-art .card-back'),
    discardEmpty: box('#discard-art .pile-empty'),
    discardCards: discard ? discard.querySelectorAll('.card').length : -1,
    coop: document.body.classList.contains('is-coop'),
    seats: document.querySelectorAll('#seats .seat').length,
    rail: box('#seats'),
    railScroll: document.querySelector('#seats') ? document.querySelector('#seats').scrollWidth : -1,
    railClient: document.querySelector('#seats') ? document.querySelector('#seats').clientWidth : -1,
    seatBoxes: [...document.querySelectorAll('#seats .seat')].map((n) => {
      const r = n.getBoundingClientRect();
      return { x: r.x, right: r.right, w: r.width };
    }),
    view: { w: window.innerWidth, h: window.innerHeight,
            scroll: document.scrollingElement.scrollWidth,
            scrollH: document.scrollingElement.scrollHeight },
  };
})()`;

async function removeWithRetry(dir, tries = 10) {
  for (let i = 0; i < tries; i++) {
    try {
      rmSync(dir, { recursive: true, force: true });
      return;
    } catch {
      await sleep(150);
    }
  }
}

function inside(inner, outer, slack = 1) {
  if (!inner || !outer) return false;
  return inner.x >= outer.x - slack && inner.y >= outer.y - slack &&
    inner.right <= outer.right + slack && inner.bottom <= outer.bottom + slack;
}

function overlaps(a, b) {
  if (!a || !b) return false;
  return a.x < b.right && b.x < a.right && a.y < b.bottom && b.y < a.bottom;
}

async function main() {
  const own = !process.env.BASE;
  const srv = own ? await startServer() : null;
  const base = process.env.BASE || srv.base;
  const userDir = mkdtempSync(join(tmpdir(), 'coop-shot-chrome-'));
  let chrome = null;
  const failures = [];
  try {
    if (!(await waitForHealth(base))) throw new Error('server did not answer /api/health');
    const table = await seatTable(base);
    mkdirSync(SHOTS, { recursive: true });

    chrome = await launchChromium(userDir);
    const targets = await (await fetch(`http://127.0.0.1:${chrome.port}/json/list`)).json();
    const target = targets.find((t) => t.type === 'page');
    const page = await Page.attach(target);

    // The seat lives in localStorage, exactly as a returning player's tab has it.
    await page.goto(base + '/');
    await page.eval(`localStorage.setItem('regicide.room.${table.code}', ${JSON.stringify(JSON.stringify(table.host))})`);
    await page.goto(`${base}/?room=${table.code}`);
    // Wait for the board, not a timer: the first state frame decides when it is drawn.
    for (let i = 0; i < 100; i++) {
      const ok = await page.eval("Boolean(document.querySelector('#board') && !document.querySelector('#board').hidden)").catch(() => false);
      if (ok) break;
      await sleep(100);
    }

    // The lobby, as the host sees it: the settings block and a way to free a chair.
    // 0.9.8: the seating cluster (seats, empty chairs, Add a bot, Start) comes before
    // Table settings in the DOM, and every deck choice is reachable at phone width.
    await page.viewport(1440, 900);
    await sleep(300);
    const lobby = await page.eval(`(() => {
      const body = document.querySelector('#lobby-body');
      const settings = body.querySelector('.lobby-settings');
      const botRow = body.querySelector('.lobby-bot-row');
      const actions = body.querySelector('.lobby-actions');
      const after = (a, b) => Boolean(a && b) && (a.compareDocumentPosition(b) & Node.DOCUMENT_POSITION_FOLLOWING) !== 0;
      return {
        settings: settings ? settings.querySelectorAll('input[type="checkbox"]').length : 0,
        addBot: Boolean(botRow && botRow.querySelector('button')),
        kick: body.querySelectorAll('.lobby-remove').length,
        seats: body.querySelectorAll('.lobby-seat:not(.is-empty)').length,
        empty: body.querySelectorAll('.lobby-seat.is-empty').length,
        order: after(botRow, settings) && after(actions, settings),
        start: [...body.querySelectorAll('button')].some((b) => /Start the game/.test(b.textContent || '')),
      };
    })()`);
    await page.shot(join(SHOTS, 'coop-lobby-1440.png'));
    if (!(lobby && lobby.settings >= 5 && lobby.addBot && lobby.kick >= 1
      && lobby.seats === 2 && lobby.empty === 2 && lobby.order && lobby.start)) {
      failures.push(`lobby controls missing: ${JSON.stringify(lobby)}`);
    }

    // The deck control at phone width: before 0.9.8 the Pokemon segment was 59px
    // clipped and untappable (measured). Every segment must end inside its container.
    await page.viewport(390, 844);
    await sleep(400);
    const deck = await page.eval(`(() => {
      const seg = document.querySelector('#lobby-body .lobby-deck-set');
      if (!seg) return null;
      const r = seg.getBoundingClientRect();
      return {
        wrapped: [...seg.querySelectorAll('.seg')].every((b) => {
          const br = b.getBoundingClientRect();
          return br.width > 0 && br.right <= r.right + 0.5 && br.left >= r.left - 0.5;
        }),
        rows: new Set([...seg.querySelectorAll('.seg')].map((b) => Math.round(b.getBoundingClientRect().top))).size,
      };
    })()`);
    await page.shot(join(SHOTS, 'coop-lobby-390.png'));
    if (!(deck && deck.wrapped && deck.rows >= 2)) {
      failures.push(`lobby deck not reachable at 390: ${JSON.stringify(deck)}`);
    }
    await page.viewport(1440, 900);
    await sleep(300);

    // Deal, then wait for the board the same way.
    await table.deal();
    for (let i = 0; i < 100; i++) {
      const dealt = await page.eval('Boolean(document.querySelector("#board") && !document.querySelector("#board").hidden)').catch(() => false);
      if (dealt) break;
      await sleep(100);
    }

    for (const vp of VIEWPORTS) {
      await page.viewport(vp.width, vp.height);
      await sleep(400);
      const g = await page.eval(GEOMETRY);
      await page.shot(join(SHOTS, `coop-${vp.name}.png`));
      if (!g || !g.coop || g.seats !== 2) {
        failures.push(`${vp.name}: not a two-seat co-op board (coop=${g && g.coop}, seats=${g && g.seats})`);
        continue;
      }
      // Note 7: one tavern back in co-op, and it never covers the label.
      if (g.tavernBacks !== 1) failures.push(`${vp.name}: tavern shows ${g.tavernBacks} card backs, want 1`);
      if (overlaps(g.tavernBack, g.tavernLabel)) failures.push(`${vp.name}: the tavern back covers its label`);
      // Note 8: the empty discard slot fills its art box and never spills out of it.
      if (g.discardCards === 0) {
        if (!g.discardEmpty) failures.push(`${vp.name}: an empty discard has no placeholder`);
        else if (!inside(g.discardEmpty, g.discard, 1)) {
          failures.push(`${vp.name}: discard placeholder ${JSON.stringify(g.discardEmpty)} is outside the art box ${JSON.stringify(g.discard)}`);
        }
      }
      // Note 4: health and attack share one line under the royal, at every width.
      if (!g.hp || !g.attack) failures.push(`${vp.name}: the royal has no health/attack boxes`);
      else {
        if (Math.abs(g.hp.y - g.attack.y) > 2) {
          failures.push(`${vp.name}: health and attack are stacked (y ${Math.round(g.hp.y)} vs ${Math.round(g.attack.y)})`);
        }
        if (g.hp.right > g.attack.x + 1) {
          failures.push(`${vp.name}: health and attack overlap (${Math.round(g.hp.right)} > ${Math.round(g.attack.x)})`);
        }
      }
      // Note 3B: the hand is centred on the board, not pinned to the rail.
      if (!g.handGroup || !g.hand) failures.push(`${vp.name}: no hand cards to measure`);
      else {
        const cardCentre = g.handGroup.x + g.handGroup.w / 2;
        const boardCentre = g.board.x + g.board.w / 2;
        if (Math.abs(cardCentre - boardCentre) > 4) {
          failures.push(`${vp.name}: hand centred at ${Math.round(cardCentre)}, board centre ${Math.round(boardCentre)}`);
        }
      }
      // Note 4: on a 27" desk the piles stay within pointing distance of the royal.
      if (vp.width >= 1800 && g.royalCol && g.tavern) {
        const distance = g.tavern.x - g.royalCol.right;
        if (distance > 760) failures.push(`${vp.name}: royal to tavern is ${Math.round(distance)}px, want <= 760`);
        if (g.royalCol.w < 240) failures.push(`${vp.name}: royal column is ${Math.round(g.royalCol.w)}px, want >= 240`);
      }
      // The rail must fit the screen: a clipped seat is a seat nobody can read.
      if (g.railScroll > g.railClient + 1) failures.push(`${vp.name}: the seat rail overflows (${g.railScroll} > ${g.railClient})`);
      for (const [i, b] of g.seatBoxes.entries()) {
        if (b.right > g.view.w + 1) failures.push(`${vp.name}: seat ${i} ends at ${Math.round(b.right)} past the ${g.view.w}px viewport`);
      }
      if (g.view.scroll > g.view.w + 1) failures.push(`${vp.name}: the page scrolls sideways (${g.view.scroll} > ${g.view.w})`);
      writeFileSync(join(SHOTS, `coop-${vp.name}.json`), JSON.stringify(g, null, 2));
    }

    // The rules sheet at phone width: the co-op block is new copy and a long line could
    // push the sheet sideways.
    await page.viewport(390, 844);
    await sleep(200);
    await page.eval("document.querySelector('#btn-rules') && document.querySelector('#btn-rules').click()");
    await sleep(400);
    const rules = await page.eval(`(() => {
      const sheet = document.querySelector('#overlay-root .sheet');
      if (!sheet) return null;
      const body = sheet.querySelector('.rules-body');
      const heading = sheet.querySelector('h2');
      return {
        title: heading ? heading.textContent : '',
        coopBlock: Boolean(body && /Co-op at a table/.test(body.textContent || '')),
        killingBlow: Boolean(body && /killing blow is not a rest/i.test(body.textContent || '')),
        bodyOverflow: body ? body.scrollWidth > body.clientWidth + 1 : null,
        sheetOverflow: sheet.scrollWidth > sheet.clientWidth + 1,
      };
    })()`);
    await page.shot(join(SHOTS, 'coop-rules-390.png'));
    if (!(rules && rules.coopBlock && rules.killingBlow && rules.bodyOverflow === false && rules.sheetOverflow === false)) {
      failures.push(`rules sheet at 390: ${JSON.stringify(rules)}`);
    }

    if (failures.length) {
      console.log(failures.map((f) => 'FAIL ' + f).join('\n'));
      console.log('screens in ' + SHOTS);
      process.exitCode = 1;
      return;
    }
    console.log('PASS co-op piles at 390/1024/1440/1920/2560; screens in ' + SHOTS);
  } catch (err) {
    console.log('could not measure: ' + (err && err.stack ? err.stack : err));
    process.exitCode = 2;
  } finally {
    if (chrome) try { process.kill(-chrome.proc.pid); } catch { /* gone */ }
    if (srv) try { process.kill(-srv.proc.pid); } catch { /* gone */ }
    // A browser still flushing its profile can hold files for a moment: retry, or the
    // run leaves a 30 MB profile in /tmp every time.
    await removeWithRetry(userDir);
    if (srv) await removeWithRetry(srv.data);
  }
}

main();
