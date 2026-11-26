#!/usr/bin/env node
// A minimal WebDriver client for Firefox, so the gate can see the engine the players
// actually use. Chromium is driven over CDP elsewhere; this speaks plain WebDriver over
// HTTP to geckodriver, which is a dependency of the test environment, not of the game.
//
//   node scripts/gecko.mjs --url http://127.0.0.1:8321/ --width 360 --height 780 \
//        --shot /tmp/out.png --eval "document.title"
//
// As a module:
//   import { startGecko } from './gecko.mjs';
//   const fx = await startGecko({ width: 360, height: 780 });
//   await fx.goto(url); await fx.eval('…'); await fx.shot('/tmp/x.png'); await fx.quit();
import { spawn } from 'node:child_process';
import { mkdtempSync, writeFileSync, readFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

function freePort() {
  // A port that nothing is listening on; geckodriver binds it immediately after.
  return 4444 + Math.floor(Math.random() * 400);
}

async function waitFor(fn, { tries = 150, gap = 100 } = {}) {
  for (let i = 0; i < tries; i++) {
    try {
      const v = await fn();
      if (v) return v;
    } catch {
      /* not up yet */
    }
    await sleep(gap);
  }
  throw new Error('geckodriver did not answer');
}

// startGecko launches geckodriver and opens one window at the requested CSS size.
export async function startGecko({ width = 1440, height = 900, binary = null, prefs = {}, cssWidth = 0 } = {}) {
  const port = freePort();
  const profile = mkdtempSync(join(tmpdir(), 'gecko-'));
  // A window cannot be smaller than the platform minimum, but headless Firefox honours
  // MOZ_HEADLESS_WIDTH/HEIGHT for the viewport itself: that is how a 360 px phone is
  // reproduced in Gecko.
  const driver = spawn('geckodriver', ['--port', String(port), '--log', 'error'], {
    stdio: ['ignore', 'ignore', 'ignore'],
    detached: false,
    env: {
      ...process.env,
      MOZ_HEADLESS_WIDTH: String(width),
      MOZ_HEADLESS_HEIGHT: String(height),
    },
  });

  const base = `http://127.0.0.1:${port}`;
  await waitFor(async () => {
    const r = await fetch(base + '/status');
    return r.ok;
  });

  const bin = binary || process.env.GECKO_BINARY || '/usr/bin/firefox-esr';
  // A window smaller than the platform minimum is clamped (500 px wide here), so a
  // narrow CSS viewport is reproduced by scaling: devPixelsPerPx = window / cssWidth.
  // The layout engine then lays out at exactly width/cssWidth CSS pixels.
  if (cssWidth > 0 && cssWidth < width) {
    prefs['layout.css.devPixelsPerPx'] = String(width / cssWidth);
  }
  const capabilities = {
    capabilities: {
      alwaysMatch: {
        browserName: 'firefox',
        pageLoadStrategy: 'eager',
        'moz:firefoxOptions': {
          binary: bin,
          // -profile takes a path; the `profile` capability wants a base64 zip, which is
          // why it is passed as an argument instead.
          args: ['-headless', '-profile', profile, `--width=${Math.max(width, 500)}`, `--height=${height}`, '--no-remote'],
          prefs: {
            // Headless defaults that make the measurements honest: no smooth scrolling,
            // no animation throttling differences between engines.
            'dom.webdriver.enabled': true,
            'ui.systemUsesDarkTheme': 0,
            ...prefs,
          },
        },
      },
    },
  };
  const created = await (await fetch(base + '/session', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(capabilities),
  })).json();
  const sid = created.value && created.value.sessionId;
  if (!sid) throw new Error('geckodriver refused a session: ' + JSON.stringify(created).slice(0, 300));

  let closed = false;
  const call = async (method, path, body) => {
    const res = await fetch(`${base}/session/${sid}${path}`, {
      method,
      headers: { 'Content-Type': 'application/json' },
      body: body === undefined ? undefined : JSON.stringify(body),
    });
    const json = await res.json().catch(() => ({}));
    if (json.value && json.value.error) {
      throw new Error(`${json.value.error}: ${json.value.message}`);
    }
    return json.value;
  };

  return {
    async goto(url) {
      await call('POST', '/url', { url });
    },
    async setWindow(width, height) {
      // Firefox takes the window rect, not a CSS viewport; the app's own meta viewport
      // decides the CSS width from it, which is what a phone does too.
      await call('POST', '/window/rect', { width: Math.max(width, 500), height, x: 0, y: 0 });
    },
    // eval takes an EXPRESSION and returns its value. WebDriver runs the script as a
    // function body, so a bare expression would be discarded — hence the explicit return.
    async eval(expression) {
      return call('POST', '/execute/sync', { script: `return (${expression});`, args: [] });
    },
    // exec takes statements (no wrapping), for setup work inside the page.
    async exec(script) {
      return call('POST', '/execute/sync', { script, args: [] });
    },
    async evalAsync(script) {
      return call('POST', '/execute/async', { script, args: [] });
    },
    async shot(file) {
      const b64 = await call('GET', '/screenshot');
      writeFileSync(file, Buffer.from(b64, 'base64'));
      return file;
    },
    async source() {
      return call('GET', '/source');
    },
    async quit() {
      if (closed) return;
      closed = true;
      try {
        await call('DELETE', '');
      } catch {
        /* already gone */
      }
      try {
        driver.kill('SIGKILL');
      } catch {
        /* already gone */
      }
      try {
        rmSync(profile, { recursive: true, force: true });
      } catch {
        /* leave it */
      }
    },
  };
}

async function main() {
  const argv = process.argv.slice(2);
  const arg = (name, dflt) => {
    const i = argv.indexOf('--' + name);
    return i >= 0 ? argv[i + 1] : dflt;
  };
  const url = arg('url', 'http://127.0.0.1:8321/');
  const width = Number(arg('width', 1440));
  const height = Number(arg('height', 900));
  const shot = arg('shot', '');
  const script = arg('eval', '');

  const fx = await startGecko({ width, height });
  try {
    await fx.goto(url);
    await sleep(Number(arg('wait', 1200)));
    if (script) console.log(JSON.stringify(await fx.eval(script)));
    if (shot) await fx.shot(shot);
  } finally {
    await fx.quit();
  }
}

if (import.meta.url === `file://${process.argv[1]}`) {
  main().catch((e) => {
    console.error('gecko:', e.message);
    process.exit(1);
  });
}

// frame runs the app inside a same-origin iframe of an exact CSS size. Firefox refuses
// a window narrower than ~500 px, and `layout.css.devPixelsPerPx` changes density but not
// the CSS viewport, so an iframe is the only honest way to ask Gecko to lay out at 360.
// Media queries, 100vw and position:fixed all resolve against the frame, exactly as they
// would on the phone.
export async function frameAt(fx, { url, width, height }) {
  await fx.goto(url);
  await fx.exec(`
    document.body.innerHTML = '';
    const f = document.createElement('iframe');
    f.id = 'probe-frame';
    f.style.cssText = 'position:fixed;inset:0 auto auto 0;border:0;width:${width}px;height:${height}px;';
    f.src = ${JSON.stringify(url)};
    document.body.append(f);
  `);
  await new Promise((r) => setTimeout(r, 2500));
  return {
    async evalIn(expression) {
      return fx.exec(`
        const w = document.getElementById('probe-frame').contentWindow;
        const d = w.document;
        const innerWidth = w.innerWidth, innerHeight = w.innerHeight;
        return (function () { with (w) { return (${expression}); } })();
      `);
    },
    async title() {
      return fx.exec(`return document.getElementById('probe-frame').contentDocument.title;`);
    },
    async shot(file) {
      return fx.shot(file);
    },
  };
}
