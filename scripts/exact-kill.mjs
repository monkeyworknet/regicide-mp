#!/usr/bin/env node
// C4/C6, the exact-kill branch, proven in the browser the player uses.
//
// The engine already covers exact damage. The Phase C campaign could not force it in
// four turns, so this fixes the deal instead: seed `EXACTKILL-0` opens on the Jack of
// Spades (health 20) with the 10 of Clubs in hand, and clubs double to exactly 20 when
// the royal is not club-immune. The script drives the real menu, the real hand and the
// real Play button, then reads the log and the tavern count.
//
//   ./bin/regicide must exist. node scripts/exact-kill.mjs
import { spawn } from 'node:child_process';
import { mkdtempSync, rmSync } from 'node:fs';
import net from 'node:net';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { Browser, sleep } from './browse.mjs';

const ROOT = fileURLToPath(new URL('..', import.meta.url));
const SEED = 'EXACTKILL-0';

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
      const r = await fetch(`${base}/api/health`);
      if (r.ok) return true;
    } catch {
      /* not up yet */
    }
    await sleep(100);
  }
  return false;
}

const port = await freePort();
const base = `http://127.0.0.1:${port}`;
const data = mkdtempSync(join(tmpdir(), 'exactkill-'));
const profile = mkdtempSync(join(tmpdir(), 'exactkill-profile-'));
const srv = spawn(join(ROOT, 'bin/regicide'), ['-addr', `127.0.0.1:${port}`, '-data', data], { stdio: 'ignore' });
let browser = null;
try {
  if (!(await waitHealth(base, 8000))) throw new Error('server did not answer /api/health');
  browser = await Browser.start({ profile, width: 1440, height: 900 });
  await browser.open(`${base}/`);
  // The input is static HTML; the submit listener is not. Wait for an element the
  // module builds, or a click under load submits the form before app.js has run and
  // the page simply reloads (the gate runs this lane beside four others).
  await browser.waitFor('document.querySelector("#theme-choices button") && window.__regicide', 15000);
  await browser.waitFor('document.querySelector("#seed-input")', 8000);
  await browser.type('#seed-input', SEED);
  await browser.click('#btn-start');
  // Under the gate's parallel load the submit listener can be a beat behind the
  // typed value; one retry turns a harness race into a second click, not a failure.
  let started = await browser.waitFor(
    'window.__regicide && window.__regicide.state() && window.__regicide.state().enemy', 5000);
  if (!started) {
    await browser.click('#btn-start');
    started = await browser.waitFor(
      'window.__regicide && window.__regicide.state() && window.__regicide.state().enemy', 12000);
  }

  const before = await browser.eval(`(() => { const s = window.__regicide.state(); return {
    enemy: s.enemy && s.enemy.id, health: s.enemyHealth, hand: s.hand.map((c) => c.id),
    exact: s.rules.exactDamage, tavern: s.tavernCount,
  }; })()`);
  check('exact.seededDeal', before.enemy === 'JS' && before.health === 20 && before.exact === true && before.hand.includes('10C'), JSON.stringify(before));

  await browser.waitFor('document.querySelector(\'#hand .card[data-card="10C"]\')', 15000);
  await browser.click('#hand .card[data-card="10C"]');
  const preview = await browser.text('#btn-confirm');
  check('exact.playShowsTwenty', /20/.test(preview || ''), preview);
  await browser.click('#btn-confirm');
  await browser.waitFor('(() => { const l = (window.__regicide.state().log || []); return l.some((e) => e.type === "defeat" && e.note === "exact"); })()', 8000);

  const after = await browser.eval(`(() => { const s = window.__regicide.state(); const l = s.log || [];
    return {
      defeat: l.filter((e) => e.type === 'defeat').pop(),
      recruit: l.filter((e) => e.type === 'recruit').pop(),
      tavern: s.tavernCount,
    }; })()`);
  check('exact.defeatIsExact', after.defeat && after.defeat.note === 'exact' && after.defeat.cards.includes('JS'), JSON.stringify(after.defeat));
  check('exact.recruited', after.recruit && after.recruit.cards.includes('JS'), JSON.stringify(after.recruit));
  check('exact.royalBackInTavern', after.tavern === before.tavern + 1, `tavern ${before.tavern} -> ${after.tavern}`);

  // The solo table now shows the royal news the engine just logged: the fallen
  // royal's own art and name, then the next royal entering. Asserted before the
  // history click, which would otherwise let the 1.3 s hold expire.
  const defeatPanel = await browser.waitFor('document.querySelector("#table-reveal.is-defeat")', 6000);
  const defeatLabel = await browser.text('#table-reveal .reveal-label');
  const defeatArt = await browser.eval(`(() => {
    const img = document.querySelector('#table-reveal.is-defeat img.reveal-card');
    return img ? img.getAttribute('src') : '';
  })()`);
  check(
    'exact.defeatReveal',
    Boolean(defeatPanel) && /Jack of Spades is Defeated/.test(defeatLabel || '') && /js\./.test(defeatArt || ''),
    `${defeatLabel} art=${defeatArt}`,
  );
  await browser.shot(join(ROOT, 'artifacts/screens/royal-defeat-1440.png'));

  const enterPanel = await browser.waitFor('document.querySelector("#table-reveal.is-enter")', 8000);
  const enterLabel = await browser.text('#table-reveal .reveal-label');
  const enterArt = await browser.eval(`(() => {
    const img = document.querySelector('#table-reveal.is-enter img.reveal-card');
    return img ? img.getAttribute('src') : '';
  })()`);
  check(
    'exact.enterReveal',
    Boolean(enterPanel) && /Jack of Hearts enters the fight/.test(enterLabel || '') && /jh\./.test(enterArt || ''),
    `${enterLabel} art=${enterArt}`,
  );
  await browser.shot(join(ROOT, 'artifacts/screens/royal-enter-1440.png'));
  await browser.waitFor('!document.querySelector("#table-reveal")', 8000);
  check('exact.revealClearsItself', !(await browser.eval('Boolean(document.querySelector("#table-reveal"))')), 'overlay gone');

  // The same panel on a phone: the royal's art must fit the width, not overflow it.
  const phoneProfile = mkdtempSync(join(tmpdir(), 'exactkill-phone-'));
  const phone = await Browser.start({ profile: phoneProfile, width: 390, height: 844 });
  try {
    // A seed link sets the field after boot; typing into a field the setup screen may
    // rebuild is a race the phone does not need to run (the desktop above covers the
    // typed path).
    await phone.open(`${base}/?seed=${encodeURIComponent(SEED)}`);
    await phone.waitFor('document.querySelector("#theme-choices button") && window.__regicide', 15000);
    await phone.waitFor(`document.querySelector('#seed-input').value === ${JSON.stringify(SEED)}`, 15000);
    const typed = await phone.eval('document.querySelector("#seed-input").value');
    check('exact.phoneSeedTyped', typed === SEED, typed);
    await phone.click('#btn-start');
    // The gate runs this beside four other lanes; wait for the board itself, then
    // say which deal it got if the card is missing.
    await phone.waitFor('window.__regicide && window.__regicide.state() && window.__regicide.state().enemy', 15000);
    const phoneEnemy = await phone.eval('window.__regicide.state().enemy && window.__regicide.state().enemy.id');
    check('exact.phoneSeededDeal', phoneEnemy === 'JS', phoneEnemy);
    await phone.waitFor('document.querySelector(\'#hand .card[data-card="10C"]\')', 20000);
    await phone.click('#hand .card[data-card="10C"]');
    await phone.click('#btn-confirm');
    const phonePanel = await phone.waitFor('document.querySelector("#table-reveal.is-defeat .reveal-panel")', 6000);
    const box = phonePanel ? await phone.box('#table-reveal .reveal-panel') : null;
    check(
      'exact.defeatRevealFitsPhone',
      Boolean(box) && box.w >= 200 && box.w <= 390 - 16 && box.right <= 390,
      box ? `panel ${Math.round(box.w)}px at ${Math.round(box.x)}` : 'no panel',
    );
    await phone.shot(join(ROOT, 'artifacts/screens/royal-defeat-390.png'));
  } finally {
    await phone.close().catch(() => null);
    rmSync(phoneProfile, { recursive: true, force: true });
  }

  await browser.click('#btn-history').catch(() => null);
  await sleep(300);
  const history = await browser.eval('document.querySelector("#history-list") ? document.querySelector("#history-list").innerText : ""');
  check('exact.historySaysExact', /exact/i.test(history), history.split('\n').filter((l) => /exact|recruit/i.test(l)).join(' | '));
  await browser.shot(join(ROOT, 'artifacts/screens/exact-kill.png'));
} catch (err) {
  check('exact.exception', false, String(err && err.stack ? err.stack : err).slice(0, 500));
} finally {
  if (browser) await browser.close().catch(() => null);
  srv.kill();
  rmSync(profile, { recursive: true, force: true });
  rmSync(data, { recursive: true, force: true });
}

const pass = checks.every((c) => c.ok);
for (const c of checks) if (!c.ok) console.error(`FAIL ${c.name}: ${c.detail}`);
console.log(pass ? 'exact kill: PASS' : 'exact kill: FAIL');
process.exit(pass ? 0 : 1);
