#!/usr/bin/env node
// B7/B8 + B16 browser check, the two playtest-3 items the shared table never saw:
//   disconnect -> bot fill -> the bot actually takes the seat's turn
//   reopen the same profile -> the same chair and the chair's own remaining hand
//   end sheet: guest has no Play again and sees the waiting hint; the host's Download
//   the log is a real JSONL, Watch the replay mints a ticket, Play again re-deals;
//   the sheet is four controls with a log disclosure, and Main menu really goes home.
//
// Two real Chromium profiles on one room, API only for creating/joining the room (the
// campaign's join-by-typing is covered by the seat-lifecycle smoke). The server runs
// with a 3 s bot-fill window (`-botfill`), so the check still proves the real path —
// silence, a bot taking the chair, the bot's own turn — without sleeping through the
// shipped minute.
//
//   ./bin/regicide must exist. node scripts/coop-disconnect.mjs
import { spawn } from 'node:child_process';
import { mkdtempSync, rmSync } from 'node:fs';
import net from 'node:net';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { Browser, sleep } from './browse.mjs';

const checks = [];
const check = (name, ok, detail) => {
  checks.push({ name, ok: Boolean(ok), detail: detail ?? null });
  console.log(JSON.stringify({ name, ok: Boolean(ok), detail: detail ?? null }));
};

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

async function waitHealth(base, ms) {
  const end = Date.now() + ms;
  while (Date.now() < end) {
    try {
      const res = await fetch(`${base}/api/health`);
      if (res.ok) return true;
    } catch {
      /* not listening yet */
    }
    await sleep(100);
  }
  return false;
}

const port = await freePort();
const data = mkdtempSync(join(tmpdir(), 'regicide-b7-'));
const profiles = mkdtempSync(join(tmpdir(), 'regicide-b7-profiles-'));
const base = `http://127.0.0.1:${port}`;
const server = spawn('./bin/regicide', ['-addr', `127.0.0.1:${port}`, '-data', data, '-botfill', '3s'], { stdio: ['ignore', 'ignore', 'pipe'] });
server.stderr.on('data', () => {});
let host = null;
let guest = null;

const api = async (method, path, body, headers = {}) => {
  const res = await fetch(base + path, {
    method,
    headers: { 'Content-Type': 'application/json', ...headers },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  return { status: res.status, body: await res.json().catch(() => null) };
};

// makeRoom creates a two-seat dealt room: host seat 0, guest seat 1. `botFill: false`
// turns the disconnect recovery off before the deal, which the end-sheet scenario
// wants: on a 3 s test window an idle seat would become a bot in the middle of the
// scripted losing turns, and that has nothing to do with the sheet under test.
async function makeRoom(theme, { botFill = true } = {}) {
  const created = await api('POST', '/api/rooms', { theme });
  if (created.status !== 201) throw new Error(`create room: ${created.status}`);
  const code = created.body.code;
  const hostToken = created.body.token;
  const joined = await api('POST', `/api/rooms/${code}/join`);
  if (joined.status !== 200) throw new Error(`join room: ${joined.status}`);
  const seat = { 'X-Regicide-Seat': `0:${hostToken}` };
  if (!botFill) {
    const off = await api('POST', `/api/rooms/${code}/botfill`, { on: false }, seat);
    if (off.status !== 200) throw new Error(`botfill off: ${off.status} ${JSON.stringify(off.body)}`);
  }
  const room = await api('GET', `/api/rooms/${code}`, undefined, seat);
  const started = await api('POST', `/api/rooms/${code}/start`, { expectedSeq: room.body.seq }, seat);
  if (started.status !== 200) throw new Error(`start room: ${started.status} ${JSON.stringify(started.body)}`);
  return { code, hostToken, guestToken: joined.body.token };
}

// openSeat opens a profile already holding a seat token, the way a returning tab does.
async function openSeat(profile, room, seat, token) {
  const browser = await Browser.start({ profile, width: 1440, height: 900 });
  await browser.open(base + '/');
  await browser.waitFor('document.querySelector("#theme-choices button")', 15000);
  await browser.eval(`localStorage.setItem(${JSON.stringify(`regicide.room.${room.code}`)}, JSON.stringify({ seat: ${seat}, token: ${JSON.stringify(token)} }))`);
  await browser.open(`${base}/?room=${room.code}`);
  await browser.waitFor('document.querySelector("#board") && !document.querySelector("#board").hidden', 20000);
  return browser;
}

const clickText = (browser, selector, text) =>
  browser.eval(`(() => {
    const b = [...document.querySelectorAll(${JSON.stringify(selector)})].find((n) => (n.textContent || '').trim() === ${JSON.stringify(text)});
    if (!b) return false;
    b.click();
    return true;
  })()`);

const seatState = (browser) =>
  browser.eval(`(() => {
    const rows = [...document.querySelectorAll('#seats .seat')];
    return rows.map((n) => ({
      you: n.classList.contains('is-you'),
      active: n.classList.contains('is-active'),
      bot: n.classList.contains('is-bot'),
      text: (n.getAttribute('aria-label') || n.textContent || '').replace(/\\s+/g, ' ').trim(),
      hand: (n.querySelector('.seat-hand, .seat-cards, .hand-count') || {}).textContent || '',
    }));
  })()`);

try {
  if (!(await waitHealth(base, 10000))) throw new Error('server did not answer /api/health');

  // ---- B7/B8: disconnect, bot fill, bot turn, reclaim ----------------------
  const room = await makeRoom('official');
  host = await openSeat(join(profiles, 'host'), room, 0, room.hostToken);
  guest = await openSeat(join(profiles, 'guest'), room, 1, room.guestToken);
  const guestBefore = await guest.eval(`(() => window.__regicide.state().hand.map((c) => c.id))()`);
  const guestHandCount = guestBefore.length;

  // The host takes its turn: play one card, then pay the attack with every card left.
  await host.eval(`document.querySelector('#hand .card').click()`);
  await host.eval(`document.querySelector('#btn-confirm').click()`);
  await sleep(500);
  await host.eval(`(() => { for (const c of document.querySelectorAll('#hand .card')) c.click(); })()`);
  await host.eval(`document.querySelector('#btn-confirm').click()`);
  const handed = await host.waitFor(`(() => {
    const s = window.__regicide.state();
    return s && (s.turnNo > 1 || (s.log || []).some((e) => e.type === 'yield')) ? true : false;
  })()`, 10000);
  check('disconnect.hostTurnPasses', handed !== null, handed ? 'turn advanced' : 'turn did not pass');

  // Close the guest tab. The marker is the wall clock; the seat is the thing under test.
  await guest.close();
  guest = null;
  const closedAt = Date.now();

  // The table waits for the away seat. Bot fill needs 3 s of silence here (the
  // shipped grace is a minute); poll for the rail to say bot, then for the bot's own
  // action to land.
  let elapsed = 0;
  let rows = [];
  let botActedAt = 0;
  const deadline = closedAt + 30000;
  const baseline = await host.eval(`(() => (window.__regicide.state().log || []).length)()`);
  while (Date.now() < deadline) {
    rows = await seatState(host);
    const row = rows[1] || {};
    if (row.bot && !botActedAt) elapsed = Math.round((Date.now() - closedAt) / 1000);
    const acted = await host.eval(`(() => {
      const log = window.__regicide.state().log || [];
      return log.length > ${baseline} && log.some((e, i) => i >= ${baseline} && e.seat === 1);
    })()`);
    if (acted) {
      botActedAt = Math.round((Date.now() - closedAt) / 1000);
      break;
    }
    await sleep(2000);
  }
  check('disconnect.seatBecomesBot', rows[1] && rows[1].bot === true, JSON.stringify(rows[1] || null));
  check('disconnect.botTakesTheTurn', botActedAt > 0, botActedAt ? `seat 1 acted ${botActedAt}s after close` : 'no seat-1 action within 30s');
  check('disconnect.insideTheWindow', botActedAt > 0 && botActedAt <= 15, `${botActedAt}s`);

  // Reopen the same profile: same chair, and the hand can only have shrunk by the
  // bot's own play (never another seat's cards).
  guest = await openSeat(join(profiles, 'guest'), room, 1, room.guestToken);
  const reclaimed = await guest.eval(`(() => {
    const rows = [...document.querySelectorAll('#seats .seat')];
    const s = window.__regicide.state();
    return {
      seat: rows.findIndex((n) => n.classList.contains('is-you')),
      viewer: s.viewerSeat,
      bot: rows[1] ? rows[1].classList.contains('is-bot') : null,
      lost: Boolean(document.querySelector('#btn-take-chair')),
      hand: (s.hand || []).map((c) => c.id),
    };
  })()`);
  check(
    'reclaim.sameChair',
    reclaimed.seat === 1 && reclaimed.viewer === 1 && reclaimed.lost === false,
    JSON.stringify(reclaimed),
  );
  check('reclaim.notABot', reclaimed.bot === false, `bot=${reclaimed.bot}`);
  // The chair's hand must be its own: a tab that came back in another chair would show
  // that chair's cards. Diamond powers can add cards, so the count alone proves nothing;
  // an identical hand to the other seat's is the wrong-seat signal.
  const hostHand = await host.eval(`(() => { const s = window.__regicide.state(); return s && s.hand ? s.hand.map((c) => c.id) : []; })()`);
  check(
    'reclaim.handIsItsOwn',
    reclaimed.hand.length > 0 && reclaimed.hand.join(',') !== hostHand.join(','),
    `${reclaimed.hand.length} cards (${guestHandCount} before the disconnect), host has ${hostHand.length}`,
  );
  await api('DELETE', `/api/rooms/${room.code}`, undefined, { 'X-Regicide-Seat': `0:${room.hostToken}` }).catch(() => null);
  await host.close();
  host = null;
  await guest.close();
  guest = null;

  // ---- B16: the end sheet, forced loss ------------------------------------
  // Bot fill is off here: the sheet must not race a bot taking a chair (see makeRoom).
  const final = await makeRoom('official', { botFill: false });
  host = await openSeat(join(profiles, 'host2'), final, 0, final.hostToken);
  guest = await openSeat(join(profiles, 'guest2'), final, 1, final.guestToken);
  // Drive both seats through cheap, losing turns: play one card, pay the smallest
  // legal set, never flip a Jester. Co-op hands are not refilled, so the table runs out
  // of cards to pay the attack — the legal loss the end sheet is built for.
  const phaseOf = (browser) =>
    browser.eval(`(() => {
      const s = window.__regicide.state();
      return { phase: s.phase, seat: s.activeSeat, lost: s.lost, confirm: !document.querySelector('#btn-confirm').disabled,
               sheet: Boolean(document.querySelector('#overlay-root .overlay')) };
    })()`);
  let ended = null;
  for (let step = 0; step < 40 && !ended; step += 1) {
    const st = await phaseOf(host);
    if (st.sheet || st.lost) {
      ended = st;
      break;
    }
    const b = st.seat === 0 ? host : guest;
    if (process.env.DEBUG_LOSS) console.log('loss step', step, JSON.stringify(st));
    if (st.phase === 'step1') {
      const has = await b.eval(`Boolean(document.querySelector('#hand .card:not(.is-disabled)'))`);
      if (has) await b.eval(`document.querySelector('#hand .card:not(.is-disabled)').click()`);
      else await b.eval(`(() => { const y = document.querySelector('#btn-yield'); if (y && !y.hidden && !y.disabled) y.click(); })()`);
    } else {
      // Select cards one at a time until the button says the selection is valid.
      await b.eval(`(() => {
        const btn = document.querySelector('#btn-confirm');
        for (const c of document.querySelectorAll('#hand .card')) {
          if (!btn.disabled) break;
          c.click();
        }
      })()`);
    }
    if (await b.eval(`!document.querySelector('#btn-confirm').disabled`)) {
      await b.eval(`document.querySelector('#btn-confirm').click()`);
    } else {
      // The hand cannot cover the bill and a Jester is the only legal move left.
      // The harness never flips one, so without this the loop sat in step4_pre until
      // its steps ran out — a harness stall, not a game stall.
      await b.eval(`(() => { const j = document.querySelector('#btn-jester'); if (j && !j.hidden && !j.disabled) j.click(); })()`);
    }
    // Wait for the table to answer, not a fixed 700 ms: under the gate's parallel
    // lanes a fixed sleep can spend the whole step budget on a state that has not
    // rendered yet (the failure was a step4_pre that was still in flight).
    await host.waitFor(`(() => {
      const s = window.__regicide.state();
      return s.phase !== ${JSON.stringify(st.phase)} || s.activeSeat !== ${st.seat}
        || s.lost || Boolean(document.querySelector('#overlay-root .overlay'));
    })()`, 5000).catch(() => null);
  }
  if (!ended) ended = await phaseOf(host);
  // The sheet now waits for the last table reveal (plus a beat), so a loss can be on
  // the board for a moment before the overlay exists. Wait for it instead of reading
  // the buttons in the gap.
  if (ended && !ended.sheet) {
    // The sheet waits for the reveal queue (royal news adds up to two panels per kill)
    // plus a beat and an 8 s guard. Under the gate's parallel lanes that can take
    // longer than the old 8 s wait, which is a harness timeout, not a missing sheet.
    await host.waitFor('document.querySelector("#overlay-root .overlay")', 15000).catch(() => null);
    await guest.waitFor('document.querySelector("#overlay-root .overlay")', 15000).catch(() => null);
    ended = await phaseOf(host);
  }
  check('endsheet.opensOnLoss', Boolean(ended && (ended.sheet || ended.lost)), JSON.stringify(ended));

  const buttons = (browser) =>
    browser.eval(`(() => {
      const sheet = document.querySelector('#overlay-root .overlay');
      return sheet ? [...sheet.querySelectorAll('button')].map((b) => (b.textContent || '').trim()) : [];
    })()`).catch(() => []);
  const guestButtons = await buttons(guest);
  const guestText = await guest.eval(`(document.querySelector('#overlay-root .overlay') || {}).innerText || ''`);
  const hostButtons = await buttons(host);
  check('endsheet.guestHasNoPlayAgain', guestButtons.length > 0 && !guestButtons.includes('Play again'), JSON.stringify(guestButtons));
  check('endsheet.guestWaitingHint', /Waiting for the host to deal again/.test(guestText), guestText.slice(0, 180));
  check(
    'endsheet.hostControls',
    ['Download the log', 'Watch the replay', 'Play again'].every((t) => hostButtons.includes(t)),
    JSON.stringify(hostButtons),
  );
  check('endsheet.guestNoDownload', !guestButtons.includes('Download the log') && !guestButtons.includes('Watch the replay'), JSON.stringify(guestButtons));

  // 0.9.8: the sheet is four controls, not seven. The log and the replay live behind
  // one disclosure, the way back to the menu is a control, and the duplicate "Back to
  // the table" is gone (Close does the same thing).
  const topLevel = (browser) =>
    browser.eval(`(() => {
      const sheet = document.querySelector('#overlay-root .overlay');
      if (!sheet) return [];
      return [...sheet.querySelectorAll('.sheet-actions > button, .sheet-actions > details > summary')]
        .map((n) => (n.textContent || '').trim());
    })()`).catch(() => []);
  const guestTop = await topLevel(guest);
  const hostTop = await topLevel(host);
  check(
    'endsheet.guestShape',
    guestTop.includes('Log & replay') && guestTop.includes('Main menu') && guestTop.includes('Close')
      && guestTop.length === 3,
    JSON.stringify(guestTop),
  );
  check(
    'endsheet.hostShape',
    hostTop[0] === 'Play again' && hostTop.includes('Log & replay') && hostTop.includes('Main menu')
      && hostTop.includes('Close') && hostTop.length === 4,
    JSON.stringify(hostTop),
  );
  // Evidence for the release: the sheet as a player sees it, at both widths, with the
  // log group closed at phone size and open on desktop. SHOTS_DIR is optional so the
  // gate run stays screen-free.
  if (process.env.SHOTS_DIR) {
    const dir = process.env.SHOTS_DIR;
    await guest.viewport(390, 844);
    await guest.shot(join(dir, 'endsheet-0.9.8-390-closed.png'));
    await guest.viewport(1440, 900);
  }

  const openLog = (browser) =>
    browser.eval(`(() => {
      const d = document.querySelector('#overlay-root .overlay details.log-replay');
      if (!d) return false;
      d.open = true;
      return true;
    })()`);
  // The disclosure is a control, not decoration: it takes focus, so a keyboard user
  // reaches it inside the modal (summary was missing from the Tab trap's list).
  const summary = await guest.eval(`(() => {
    const s = document.querySelector('#overlay-root summary');
    if (!s) return null;
    s.focus();
    return { tabIndex: s.tabIndex, focused: document.activeElement === s };
  })()`);
  check('endsheet.logGroupFocusable', Boolean(summary && summary.tabIndex >= 0 && summary.focused), JSON.stringify(summary));

  await openLog(guest);
  await openLog(host);
  if (process.env.SHOTS_DIR) {
    const dir = process.env.SHOTS_DIR;
    await host.viewport(1440, 900);
    await host.shot(join(dir, 'endsheet-0.9.8-1440-open.png'));
    await host.viewport(390, 844);
    await host.shot(join(dir, 'endsheet-0.9.8-390-open.png'));
    await host.viewport(1440, 900);
  }

  // The guest's Copy log is local text, no credential needed.
  await clickText(guest, '#overlay-root .overlay button', 'Copy log');
  await guest.waitFor(`/copied/i.test((document.querySelector('#log-strip') || {}).innerText || '')`, 4000);
  const copyToast = await guest.eval(`(document.querySelector('#log-strip') || {}).innerText || ''`);
  check('endsheet.copyLog', /copied/i.test(copyToast), copyToast);

  // Main menu is the way home 0.9.8 added. It frees the chair and shows the setup
  // screen; the sheet must be gone with it, not left on top of the menu.
  await clickText(guest, '#overlay-root .overlay button', 'Main menu');
  // The leave call is a round trip: wait for the menu, not for a fixed beat.
  await guest.waitFor(`!document.querySelector('#setup').hidden && !location.search`, 10000);
  const home = await guest.eval(`({
    sheet: Boolean(document.querySelector('#overlay-root .overlay')),
    setup: !document.querySelector('#setup').hidden,
    board: document.querySelector('#board').hidden,
    url: location.search,
  })`);
  check(
    'endsheet.mainMenuGoesHome',
    !home.sheet && home.setup && home.board && !/room=/.test(home.url),
    JSON.stringify(home),
  );

  // Download: capture the blob the anchor would save and read it.
  await host.eval(`(() => {
    window.__download = null;
    window.__opened = [];
    const create = URL.createObjectURL.bind(URL);
    URL.createObjectURL = (blob) => {
      blob.text().then((text) => {
        const lines = text.trim().split('\\n');
        window.__download = { size: blob.size, lines: lines.length, first: lines[0].slice(0, 120), last: lines[lines.length - 1].slice(0, 160) };
      }).catch(() => {});
      return create(blob);
    };
    window.open = (url) => { window.__opened.push(String(url)); return null; };
  })()`);
  await clickText(host, '#overlay-root .overlay button', 'Download the log');
  await host.waitFor(`Boolean(window.__download)`, 10000);
  const download = await host.eval(`window.__download`);
  check(
    'endsheet.downloadIsArealLog',
    Boolean(download && download.lines > 1 && /"kind":"header"/.test(download.first) && /"kind":"result"/.test(download.last)),
    JSON.stringify(download),
  );
  await clickText(host, '#overlay-root .overlay button', 'Watch the replay');
  await host.waitFor(`Array.isArray(window.__opened) && window.__opened.length > 0`, 10000);
  const opened = await host.eval(`window.__opened[0] || null`);
  check('endsheet.replayTicketOpens', Boolean(opened && /\/replay\?replay=[0-9a-f]+/.test(opened)), String(opened));

  // Play again re-deals the same seats for the host only.
  await clickText(host, '#overlay-root .overlay button', 'Play again');
  await host.waitFor(`(() => {
    const s = window.__regicide.state();
    return s && s.turnNo === 1 && (s.hand || []).length > 0 && !document.querySelector('#overlay-root .overlay');
  })()`, 10000);
  const redealt = await host.eval(`(() => { const s = window.__regicide.state(); return { turn: s.turnNo, hand: (s.hand || []).length }; })()`);
  check('endsheet.playAgainRedeals', redealt.turn === 1 && redealt.hand > 0, JSON.stringify(redealt));
  check('endsheet.noConsoleExceptions', host.errors().filter((e) => e.kind === 'exception').length === 0, JSON.stringify(host.errors().slice(0, 3)));
  await api('DELETE', `/api/rooms/${final.code}`, undefined, { 'X-Regicide-Seat': `0:${final.hostToken}` }).catch(() => null);
} catch (err) {
  check('disconnect.exception', false, String(err && err.stack ? err.stack : err).slice(0, 500));
} finally {
  for (const b of [guest, host]) if (b) await b.close().catch(() => {});
  server.kill();
  await sleep(300);
  rmSync(data, { recursive: true, force: true });
  rmSync(profiles, { recursive: true, force: true });
}

const failed = checks.some((c) => !c.ok);
console.log(JSON.stringify({ pass: !failed, count: checks.length, failed: checks.filter((c) => !c.ok).map((c) => c.name) }));
process.exit(failed ? 1 : 0);
