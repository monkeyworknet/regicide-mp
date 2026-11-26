#!/usr/bin/env node
// B13 browser check: a stalled stream must show the connection chip, and the table
// must recover when the link comes back — without applying the in-flight action twice.
//
// The third playtest's CDP network emulation did not actually freeze the SSE stream
// (`Network.emulateNetworkConditions offline:true` left the fetch reader fed), so the
// check could not be produced from the browser. This puts a pausable TCP proxy in front
// of the server: pause stops forwarding bytes in both directions while both sockets stay
// open — the shape of a dead network path, not a closed one. The browser's 12s silence
// watchdog is what has to notice.
//
//   ./bin/regicide must exist (the gate builds it first).
//   node scripts/stream-drop.mjs
//
// Prints one JSON line per check and exits non-zero if any check fails.
import { spawn } from 'node:child_process';
import { mkdirSync, mkdtempSync, rmSync } from 'node:fs';
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

// createStallProxy forwards TCP to the server. While paused it keeps both sockets and
// buffers each direction's bytes, so framing survives and a resume delivers what a
// stalled link would have delivered. Closing the client side (the browser's watchdog
// aborts its fetch) tears that connection down and drops its buffer.
function createStallProxy(upstreamPort) {
  let paused = false;
  const links = new Set();
  const server = net.createServer((client) => {
    const upstream = net.connect(upstreamPort, '127.0.0.1');
    const queues = { up: [], down: [] };
    const link = {
      flush() {
        for (const chunk of queues.up) upstream.write(chunk);
        for (const chunk of queues.down) client.write(chunk);
        queues.up = [];
        queues.down = [];
      },
    };
    links.add(link);
    const drop = () => {
      links.delete(link);
      client.destroy();
      upstream.destroy();
    };
    client.on('data', (chunk) => (paused ? queues.up.push(chunk) : upstream.write(chunk)));
    upstream.on('data', (chunk) => (paused ? queues.down.push(chunk) : client.write(chunk)));
    client.on('close', drop);
    upstream.on('close', drop);
    client.on('error', drop);
    upstream.on('error', drop);
  });
  return {
    server,
    setPaused(next) {
      paused = next;
      if (!paused) for (const link of links) link.flush();
    },
    close() {
      for (const link of links) link.flush();
      server.close();
    },
    get paused() {
      return paused;
    },
    get links() {
      return links.size;
    },
  };
}

const port = await freePort();
const proxyPort = await freePort();
const data = mkdtempSync(join(tmpdir(), 'regicide-drop-'));
const profile = mkdtempSync(join(tmpdir(), 'regicide-drop-profile-'));
const base = `http://127.0.0.1:${port}`;
const proxied = `http://127.0.0.1:${proxyPort}`;
const server = spawn('./bin/regicide', ['-addr', `127.0.0.1:${port}`, '-data', data], { stdio: ['ignore', 'ignore', 'pipe'] });
server.stderr.on('data', () => {});
let proxy = null;
let browser = null;
let failed = false;

try {
  if (!(await waitHealth(base, 10000))) throw new Error('server did not answer /api/health');
  proxy = createStallProxy(port);
  await new Promise((resolve, reject) => {
    proxy.server.once('error', reject);
    proxy.server.listen(proxyPort, '127.0.0.1', resolve);
  });

  // The browser goes through the proxy from the first byte, so the stream it opens is
  // the one that will stall.
  browser = await Browser.start({ profile, width: 1440, height: 900 });
  await browser.open(proxied + '/');
  // boot() fills the theme tiles and wires the buttons in the same tick, so the tiles
  // are the signal that a click cannot land before its listener exists.
  await browser.waitFor('document.querySelector("#theme-choices button")', 15000);
  await browser.click('#btn-host');
  await browser.waitFor(`document.querySelector('#lobby') && !document.querySelector('#lobby').hidden`, 15000);
  const clickText = async (text) =>
    browser.eval(`(() => {
      const b = [...document.querySelectorAll('#lobby-body button')].find((n) => (n.textContent || '').trim() === ${JSON.stringify(text)});
      // A disabled control is not clickable: report the miss so the failure names the
      // real state instead of a silent no-op that later times out at the board wait.
      if (!b || b.disabled) return false;
      b.click();
      return true;
    })()`);
  if (!(await clickText('Add a bot'))) throw new Error('Add a bot button not found');
  await sleep(500);
  if (!(await clickText('Start the game'))) throw new Error('Start the game button not found');
  await browser.waitFor(`document.querySelector('#board') && !document.querySelector('#board').hidden && document.querySelectorAll('#hand .card').length > 0`, 20000);

  const before = await browser.eval(`(() => {
    const s = window.__regicide.state();
    return { hand: (s.hand || []).length, turn: s.turnNo, log: (s.log || []).length, card: s.hand[0].id };
  })()`);

  // Select a card, stall the link, then play it: the action's POST and its answer both
  // cross the paused proxy, so the play is genuinely in flight when the stream goes
  // silent. A resumed link must apply that one action once.
  await browser.eval(`document.querySelector('#hand .card').click()`);
  proxy.setPaused(true);
  await browser.eval(`document.querySelector('#btn-confirm').click()`);

  // Five seconds of silence is a slow link, not a dead one: no chip yet. The 12 s
  // watchdog is what turns silence into "stopped answering".
  await sleep(5000);
  const early = await browser.eval(`(() => {
    const chip = document.querySelector('#conn-chip');
    return { chipHidden: !chip || chip.hidden === true, hand: (window.__regicide.state().hand || []).length };
  })()`);
  check('drop.noChipWhileTheLinkIsOnlySlow', early.chipHidden && early.hand === before.hand, JSON.stringify(early));

  await sleep(9000);
  const stalled = await browser.eval(`(() => {
    const chip = document.querySelector('#conn-chip');
    return {
      chip: Boolean(chip) && chip.hidden === false,
      text: chip ? chip.textContent.replace(/\\s+/g, ' ').trim() : null,
      hand: (window.__regicide.state().hand || []).length,
      confirm: document.querySelector('#btn-confirm').textContent.trim(),
    };
  })()`);
  check('drop.chipAppears', stalled.chip && /stopped answering/.test(stalled.text || ''), JSON.stringify(stalled));
  check('drop.actionNotAppliedWhileStalled', stalled.hand === before.hand, `hand ${stalled.hand} vs ${before.hand}`);

  proxy.setPaused(false);
  const recovered = await browser.waitFor(
    `(() => {
      const chip = document.querySelector('#conn-chip');
      const s = window.__regicide.state();
      return Boolean(chip && chip.hidden && (s.log || []).some((e) => e.type === 'play'));
    })()`,
    20000,
  );
  await sleep(1500); // let the resumed stream settle and the bot's own frames land
  const after = await browser.eval(`(() => {
    const s = window.__regicide.state();
    const plays = (s.log || []).filter((e) => e.type === 'play');
    const mine = plays.filter((e) => (e.cards || []).includes(${JSON.stringify(before.card)}));
    const chip = document.querySelector('#conn-chip');
    return {
      chipHidden: Boolean(chip && chip.hidden),
      hand: (s.hand || []).length,
      turn: s.turnNo,
      log: (s.log || []).length,
      plays: plays.length,
      sameCardPlays: mine.length,
      url: location.href,
    };
  })()`);
  check('drop.chipRecovers', recovered !== null && after.chipHidden, JSON.stringify(after));
  check(
    'drop.actionAppliedExactlyOnce',
    after.sameCardPlays === 1 && after.hand <= before.hand,
    `card ${before.card}: ${after.sameCardPlays} play(s), hand ${before.hand} -> ${after.hand}`,
  );
  check('drop.tableAdvanced', after.log > before.log && after.turn >= before.turn, `log ${before.log} -> ${after.log}, turn ${before.turn} -> ${after.turn}`);
  check('drop.liveTransport', String(await browser.eval('location.protocol')) === 'http:', after.url);
  check('drop.noConsoleErrors', browser.errors().filter((e) => e.kind === 'exception').length === 0, JSON.stringify(browser.errors().slice(0, 3)));
} catch (err) {
  check('drop.exception', false, String(err && err.stack ? err.stack : err).slice(0, 500));
} finally {
  if (browser) await browser.close().catch(() => {});
  if (proxy) proxy.close();
  server.kill();
  await sleep(300);
  rmSync(data, { recursive: true, force: true });
  rmSync(profile, { recursive: true, force: true });
}

failed = checks.some((c) => !c.ok);
console.log(JSON.stringify({ pass: !failed, count: checks.length, failed: checks.filter((c) => !c.ok).map((c) => c.name) }));
process.exit(failed ? 1 : 0);
