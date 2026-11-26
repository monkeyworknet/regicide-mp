#!/usr/bin/env node
// Referee for a simulated co-op game: three real browsers sit in the three seats, a
// fourth watches. It owns the room, injects each seat's token into that browser's
// localStorage, and exposes the whole game as plain HTTP, one call per action:
//
//   GET  /state?seat=N        what that browser can see right now, as JSON
//   GET  /facts?seat=N        the same, plus the visible DOM text (banner, hints)
//   GET  /play?seat=N&ids=3,7 click those cards in the hand, then Play
//   GET  /yield?seat=N        press Yield
//   GET  /shot?seat=N         screenshot; returns the file path
//   GET  /spec/state          the spectator's view (hands visible)
//   GET  /log                 every action the referee has performed
//   GET  /stop                close the browsers
//
// It exists so an automated player can hold a real seat: one HTTP call per action,
// no harness inside the page.
//
//   node scripts/coop-referee.mjs --base http://127.0.0.1:8321 --seats 3 --port 8501
//   curl -s "http://127.0.0.1:8501/facts?seat=0"
//   curl -s "http://127.0.0.1:8501/play?seat=0&ids=AS,7H"
//   curl -s "http://127.0.0.1:8501/spec/as?seat=1"   # spectator views a seat's hand
//   curl -s "http://127.0.0.1:8501/shot?seat=2"
import { spawn } from 'node:child_process';
import { mkdtempSync, writeFileSync, mkdirSync, rmSync } from 'node:fs';
import { createServer } from 'node:http';
import { tmpdir } from 'node:os';
import { join, dirname } from 'node:path';
import { fileURLToPath } from 'node:url';

const argv = process.argv.slice(2);
const arg = (name, dflt) => {
  const i = argv.indexOf('--' + name);
  return i >= 0 ? argv[i + 1] : dflt;
};
const BASE = arg('base', 'http://127.0.0.1:8321');
const SEATS = Number(arg('seats', 3));
const REF_PORT = Number(arg('port', 8501));
const OUT = arg('out', process.env.SIM_OUT || '/tmp/sim');
const HERE = dirname(fileURLToPath(import.meta.url));
void HERE;
mkdirSync(OUT, { recursive: true });

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const log = [];

class Page {
  constructor(ws, port) {
    this.ws = ws;
    this.port = port;
    this.id = 0;
    this.pending = new Map();
  }
  static async attach(port) {
    for (let i = 0; i < 100; i++) {
      try {
        const list = await (await fetch(`http://127.0.0.1:${port}/json/list`)).json();
        const target = list.find((t) => t.type === 'page');
        if (target) {
          const ws = new WebSocket(target.webSocketDebuggerUrl);
          const page = new Page(ws, port);
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
      } catch {
        /* not up */
      }
      await sleep(150);
    }
    throw new Error(`chromium on ${port} never answered`);
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

const FACTS = `(() => {
  const t = (sel) => { const e = document.querySelector(sel); return e ? e.textContent.replace(/\\s+/g, ' ').trim() : null; };
  const RANKS = { A: 'Ace', 2: 'Two', 3: 'Three', 4: 'Four', 5: 'Five', 6: 'Six', 7: 'Seven',
    8: 'Eight', 9: 'Nine', T: 'Ten', J: 'Jack', Q: 'Queen', K: 'King', JKR: 'Jester' };
  const SUITS = { S: 'Spades', H: 'Hearts', D: 'Diamonds', C: 'Clubs', R: 'Jester' };
  const nameOf = (id) => {
    const m = /^([0-9TJQKA]+)([SHDCR])$/.exec(id || '');
    if (!m) return id;
    return (RANKS[m[1]] || m[1]) + ' of ' + (SUITS[m[2]] || m[2]);
  };
  const hand = [...document.querySelectorAll('#hand .card')].map((c) => ({
    id: c.dataset.card,
    name: c.getAttribute('aria-label') || nameOf(c.dataset.card),
    text: c.textContent.replace(/\\s+/g, ' ').trim().slice(0, 80),
    selected: c.classList.contains('is-selected') || c.getAttribute('aria-selected') === 'true',
  }));
  const seats = [...document.querySelectorAll('#seats .seat')].map((s) => ({
    label: s.getAttribute('aria-label') || s.textContent.replace(/\\s+/g, ' ').trim(),
    current: s.getAttribute('aria-current') === 'true',
  }));
  const btn = (sel) => { const e = document.querySelector(sel); return e ? { text: e.textContent.trim(), disabled: Boolean(e.disabled), hidden: Boolean(e.hidden || e.offsetParent === null) } : null; };
  return {
    mode: document.body.classList.contains('is-coop') ? 'coop' : 'solo',
    url: location.pathname + location.search,
    phase: t('#phase-badge'),
    banner: t('#turn-banner'),
    castle: t('#castle-count'),
    turn: t('#turn-count'),
    // The royal is a picture in the DOM; its name lives in the projection (and now in
    // the card's aria-label). Report both so an agent does not have to guess.
    enemy: t('#enemy-card'),
    royal: (() => {
      const st = window.__regicide && window.__regicide.state;
      if (!st || !st.enemy) return null;
      return { label: st.enemy.label || st.enemy.id, health: st.enemyHealth, damage: st.damage, attack: st.enemyAttack, phase: st.phase, won: Boolean(st.won), lost: Boolean(st.lost) };
    })(),
    health: t('#enemy-health') + '/' + t('#enemy-health-total'),
    attack: t('#enemy-incoming') + '/' + t('#enemy-attack-total'),
    hint: t('#step-hint'),
    seats,
    hand,
    confirm: btn('#btn-confirm'),
    yield: btn('#btn-yield'),
    toast: t('#toast') || null,
    sheet: Boolean(document.querySelector('#overlay-root')?.firstChild),
    streamError: window.__liveError || null,
  };
})()`;

async function setupGame() {
  const post = async (path, body, seatToken) => {
    // The seat authenticates with one header: "seat:token" (room_api.go seatFromRequest).
    const res = await fetch(BASE + path, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        ...(seatToken ? { 'X-Regicide-Seat': seatToken } : {}),
      },
      body: JSON.stringify(body || {}),
    });
    const text = await res.text();
    let data = null;
    try {
      data = JSON.parse(text);
    } catch {
      /* not json */
    }
    if (!res.ok) throw new Error(`${path} -> ${res.status} ${text.slice(0, 200)}`);
    return data;
  };

  const room = await post('/api/rooms', { theme: 'simple' });
  const code = room.code || room.room?.code;
  const hostSeat = room.seat ?? room.room?.hostSeat ?? 0;
  const tokens = [{ seat: hostSeat, token: room.token, name: room.room?.seats?.[hostSeat]?.name, header: `${hostSeat}:${room.token}` }];
  let seq = room.seq ?? 0;
  for (let i = 1; i < SEATS; i++) {
    const j = await post(`/api/rooms/${code}/join`, {});
    seq = j.seq ?? seq;
    tokens.push({ seat: j.seat, token: j.token, name: j.room?.seats?.[j.seat]?.name, header: `${j.seat}:${j.token}` });
  }
  await post(`/api/rooms/${code}/start`, { expectedSeq: seq }, tokens[0].header);
  // The spectator may see hands: this is what D-45's host switch is for.
  await post(`/api/rooms/${code}/watching`, { allowed: true, hands: true }, tokens[0].header);
  const watch = await post(`/api/rooms/${code}/watch`, {});
  return { code, tokens, watchId: watch.watchId };
}

async function openBrowser(port, name) {
  const dir = mkdtempSync(join(tmpdir(), `sim-${name}-`));
  const proc = spawn(
    'chromium',
    ['--headless=new', '--no-sandbox', '--disable-gpu', '--hide-scrollbars',
     `--remote-debugging-port=${port}`, `--user-data-dir=${dir}`,
     '--window-size=1440,900', 'about:blank'],
    { stdio: 'ignore', detached: true }
  );
  const page = await Page.attach(port);
  await page.send('Emulation.setDeviceMetricsOverride', {
    width: 1440, height: 900, deviceScaleFactor: 1, mobile: false,
  });
  return { page, proc, dir };
}

async function main() {
  const game = await setupGame();
  console.log(JSON.stringify({ room: game.code, seats: game.tokens.map((t) => ({ seat: t.seat, name: t.name })) }));

  const browsers = new Map(); // seat -> browser; key 'spec' for the spectator
  let nextPort = 9401;
  for (const t of game.tokens) {
    const b = await openBrowser(nextPort++, `seat${t.seat}`);
    // Attach the seat: the token goes into localStorage, then the room URL resumes it.
    await b.page.send('Page.navigate', { url: BASE + '/' });
    await sleep(700);
    await b.page.eval(`localStorage.setItem('regicide.room.' + ${JSON.stringify(game.code)}, ${JSON.stringify(
      JSON.stringify({ seat: t.seat, token: t.token })
    )})`);
    await b.page.send('Page.navigate', { url: `${BASE}/?room=${game.code}` });
    browsers.set(t.seat, b);
  }
  const spec = await openBrowser(nextPort++, 'spec');
  await spec.page.send('Page.navigate', { url: BASE + '/' });
  await sleep(700);
  await spec.page.eval(
    `localStorage.setItem('regicide.watch.' + ${JSON.stringify(game.code)}, ${JSON.stringify(game.watchId)})`
  );
  // A spectator resumes from ?room=CODE&watch=1 (app.js reads the flag, then the
  // watch id from localStorage).
  await spec.page.send('Page.navigate', { url: `${BASE}/?room=${game.code}&watch=1` });
  browsers.set('spec', spec);

  const pageFor = (key) => {
    const b = browsers.get(key === 'spec' ? 'spec' : Number(key));
    return b && b.page;
  };

  const server = createServer(async (req, res) => {
    const url = new URL(req.url, 'http://x');
    const seat = url.searchParams.get('seat');
    const key = url.pathname === '/spec/state' ? 'spec' : seat;
    const reply = (code, body, type = 'application/json') => {
      res.writeHead(code, { 'Content-Type': type });
      res.end(typeof body === 'string' ? body : JSON.stringify(body));
    };
    try {
      if (url.pathname === '/log') return reply(200, log);
      if (url.pathname === '/stop') {
        reply(200, { ok: true });
        for (const b of browsers.values()) {
          try { b.proc.kill('SIGKILL'); } catch {}
          try { rmSync(b.dir, { recursive: true, force: true }); } catch {}
        }
        setTimeout(() => process.exit(0), 300);
        return;
      }
      const page = pageFor(key);
      if (!page) return reply(400, { error: `no such seat: ${key}` });

      if (url.pathname === '/state' || url.pathname === '/spec/state' || url.pathname === '/facts') {
        const facts = await page.eval(FACTS);
        return reply(200, { seat: key, ...facts });
      }
      if (url.pathname === '/play') {
        const ids = (url.searchParams.get('ids') || '').split(',').map((s) => s.trim()).filter(Boolean);
        if (ids.length === 0) return reply(400, { error: 'ids required' });
        const clicked = await page.eval(`(() => {
          const ids = ${JSON.stringify(ids)};
          const missing = [];
          for (const id of ids) {
            const card = document.querySelector('#hand .card[data-card="' + id + '"]');
            if (!card) { missing.push(id); continue; }
            card.click();
          }
          const confirm = document.querySelector('#btn-confirm');
          const state = { missing, confirmDisabled: confirm ? confirm.disabled : null };
          if (missing.length === 0 && confirm && !confirm.disabled) confirm.click();
          return state;
        })()`);
        log.push({ at: new Date().toISOString(), seat: key, action: 'play', ids, clicked });
        await sleep(700);
        const facts = await page.eval(FACTS);
        return reply(200, { seat: key, attempted: { ids, clicked }, ...facts });
      }
      if (url.pathname === '/yield') {
        const ok = await page.eval(`(() => {
          const b = document.querySelector('#btn-yield');
          if (!b || b.disabled) return false;
          b.click();
          return true;
        })()`);
        log.push({ at: new Date().toISOString(), seat: key, action: 'yield', ok });
        await sleep(700);
        const facts = await page.eval(FACTS);
        return reply(200, { seat: key, attempted: { yield: ok }, ...facts });
      }
      if (url.pathname === '/spec/as') {
        // A spectator sees a hand only by "viewing as" a seat (D-45). Click that seat.
        const want = Number(url.searchParams.get('seat') || 0);
        const ok = await page.eval(`(() => {
          const seats = [...document.querySelectorAll('#seats .seat')];
          const el = seats[${want}];
          if (!el) return { ok: false, seats: seats.length };
          el.click();
          return { ok: true };
        })()`);
        log.push({ at: new Date().toISOString(), seat: 'spec', action: 'viewAs', want, ok });
        await sleep(900);
        const facts = await page.eval(FACTS);
        return reply(200, { seat: 'spec', attempted: { viewAs: want, ok }, ...facts });
      }
      if (url.pathname === '/jester') {
        const ok = await page.eval(`(() => {
          const b = document.querySelector('#btn-jester');
          if (!b || b.disabled || b.hidden) return false;
          b.click();
          return true;
        })()`);
        log.push({ at: new Date().toISOString(), seat: key, action: 'jester', ok });
        await sleep(700);
        const facts = await page.eval(FACTS);
        return reply(200, { seat: key, attempted: { jester: ok }, ...facts });
      }
      if (url.pathname === '/move' || url.pathname === '/sweep') {
        // Real mouse movement through the browser's input pipeline: the same events a
        // person's hand produces, for exercising hover and focus behaviour. There is no
        // cursor relay any more (D-85), so a move changes only this tab's hover state.
        const box = await page.eval(`(() => { const b = document.querySelector('#board').getBoundingClientRect(); return { x: b.left, y: b.top, w: b.width, h: b.height }; })()`);
        const points = [];
        if (url.pathname === '/move') {
          points.push([Number(url.searchParams.get('x') || 0.5), Number(url.searchParams.get('y') || 0.5)]);
        } else {
          const steps = Math.min(24, Math.max(2, Number(url.searchParams.get('steps') || 8)));
          const y = Number(url.searchParams.get('y') || 0.55);
          for (let i = 0; i < steps; i++) points.push([0.12 + (0.76 * i) / (steps - 1), y]);
        }
        for (const [fx, fy] of points) {
          await page.send('Input.dispatchMouseEvent', {
            type: 'mouseMoved',
            x: Math.round(box.x + fx * box.w),
            y: Math.round(box.y + fy * box.h),
            button: 'none',
            buttons: 0,
          });
          await sleep(120);
        }
        log.push({ at: new Date().toISOString(), seat: key, action: 'move', points: points.length });
        await sleep(200);
        return reply(200, { seat: key, moved: points.length, from: points[0], to: points[points.length - 1] });
      }
      if (url.pathname === '/shot') {
        const { data } = await page.send('Page.captureScreenshot', { format: 'png' });
        const file = join(OUT, `${key}-${Date.now()}.png`);
        writeFileSync(file, Buffer.from(data, 'base64'));
        return reply(200, { seat: key, file });
      }
      return reply(404, { error: 'unknown path' });
    } catch (e) {
      return reply(500, { error: String(e && e.message ? e.message : e) });
    }
  });
  server.listen(REF_PORT, '127.0.0.1', () => {
    console.log(`referee on http://127.0.0.1:${REF_PORT} (room ${game.code})`);
  });
}

main().catch((e) => {
  console.error('referee failed:', e.message);
  process.exit(1);
});
