#!/usr/bin/env node
// Run the in-page smokes in Firefox, which is the engine the players use.
//
//   node scripts/gecko-smoke.mjs --base http://127.0.0.1:8321
//
// The smokes write their results into #result and set document.title to SMOKE-PASS or
// SMOKE-FAIL, so this waits for the title instead of guessing at a delay: a headless
// screenshot taken at load proved nothing (it caught the setup screen before the module
// had painted the tiles).
import { startGecko } from './gecko.mjs';

const argv = process.argv.slice(2);
const arg = (name, dflt) => {
  const i = argv.indexOf('--' + name);
  return i >= 0 ? argv[i + 1] : dflt;
};
const BASE = arg('base', 'http://127.0.0.1:8321');
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

async function runSmoke(fx, name, path, limitMs) {
  await fx.goto(BASE + path);
  const deadline = Date.now() + limitMs;
  while (Date.now() < deadline) {
    const title = await fx.eval('document.title');
    if (typeof title === 'string' && title.startsWith('SMOKE-')) {
      const raw = await fx.eval(`(document.querySelector('#result') || {}).textContent || ''`);
      let parsed = null;
      try {
        parsed = JSON.parse(raw);
      } catch {
        /* reported below */
      }
      const failed = parsed ? parsed.checks.filter((c) => !c.ok).map((c) => c.name) : [];
      return { name, pass: title === 'SMOKE-PASS' && Boolean(parsed && parsed.pass), failed, viewport: parsed && parsed.viewport };
    }
    await sleep(250);
  }
  return { name, pass: false, failed: ['timeout waiting for a smoke result'], viewport: null };
}

async function main() {
  const fx = await startGecko({ width: 1440, height: 900 });
  const results = [];
  try {
    results.push(await runSmoke(fx, 'solo smoke', '/?test=smoke', 90000));
    results.push(await runSmoke(fx, 'co-op smoke (sse)', '/?test=coop', 120000));
    results.push(await runSmoke(fx, 'co-op smoke (poll)', '/?test=coop&transport=poll', 120000));

    await fx.setWindow(390, 844);
    results.push(await runSmoke(fx, 'co-op smoke (phone)', '/?test=coop&transport=poll', 120000));
  } finally {
    await fx.quit();
  }
  let failed = 0;
  for (const r of results) {
    const vp = r.viewport ? `${r.viewport.w}x${r.viewport.h}` : '-';
    if (r.pass) console.log(`  ok    ${r.name} ${vp}`);
    else {
      failed++;
      console.log(`  FAIL  ${r.name} ${vp}: ${r.failed.join(', ') || 'unknown'}`);
    }
  }
  process.exit(failed === 0 ? 0 : 1);
}

main().catch((e) => {
  console.error('gecko smoke:', e.message);
  process.exit(2);
});
