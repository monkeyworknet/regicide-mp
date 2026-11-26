#!/usr/bin/env node
// One Chromium tab driven over CDP, for the browser checks in scripts/.
//
// Everything here is deliberately small and explicit: one profile, one process, one
// page, screenshots, and a list of console/network errors.
//
//   import { Browser, sleep } from './browse.mjs';
//   const b = await Browser.start({ profile: '.../profile', width: 1440, height: 900 });
//   await b.open('http://127.0.0.1:8399/');
//   await b.click('#btn-start');
//   await b.waitFor('Boolean(document.querySelector("#hand .card"))');
//   await b.shot('.../shots/01-board.png');
//   console.log(b.errors());
//   await b.close();
//
// The caller must close its own browser: close() kills this process only. A relaunch on
// the same profile keeps localStorage (and therefore a seat token) for reconnect tests.
import { spawn } from 'node:child_process';
import { mkdirSync, mkdtempSync, readFileSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join } from 'node:path';

export const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

class Page {
  constructor(ws) {
    this.ws = ws;
    this.id = 0;
    this.pending = new Map();
    this.events = [];
  }
  send(method, params = {}) {
    const id = ++this.id;
    this.ws.send(JSON.stringify({ id, method, params }));
    return new Promise((resolve, reject) => this.pending.set(id, { resolve, reject }));
  }
  handle(msg) {
    if (msg.id) {
      const slot = this.pending.get(msg.id);
      if (!slot) return;
      this.pending.delete(msg.id);
      if (msg.error) slot.reject(new Error(JSON.stringify(msg.error)));
      else slot.resolve(msg.result);
      return;
    }
    const t = Date.now();
    if (msg.method === 'Runtime.exceptionThrown') {
      const d = msg.params.exceptionDetails || {};
      this.events.push({ kind: 'exception', text: d.exception?.description || d.text || 'exception', at: t });
    } else if (msg.method === 'Runtime.consoleAPICalled' && msg.params.type === 'error') {
      this.events.push({ kind: 'console', text: (msg.params.args || []).map((a) => a.value ?? a.description ?? '').join(' '), at: t });
    } else if (msg.method === 'Log.entryAdded' && msg.params.entry.level === 'error') {
      this.events.push({ kind: 'log', text: msg.params.entry.text, url: msg.params.entry.url, at: t });
    } else if (msg.method === 'Network.responseReceived') {
      const r = msg.params.response;
      if (r.status >= 400) this.events.push({ kind: 'http', status: r.status, url: r.url, at: t });
    }
  }
}

export class Browser {
  constructor(proc, page, profile, dir) {
    this.proc = proc;
    this.page = page;
    this.profile = profile;
    this.dir = dir;
  }

  // start launches headless Chromium with an isolated profile. A profile that already
  // exists is reused: that is how an agent "reconnects" to a room after closing the tab.
  static async start({ profile, width = 1440, height = 900, chromium = 'chromium' } = {}) {
    if (!profile) throw new Error('Browser.start needs a profile directory');
    mkdirSync(profile, { recursive: true });
    const dir = mkdtempSync(join(tmpdir(), 'p3-browse-'));
    const args = [
      '--headless=new', '--no-sandbox', '--disable-gpu', '--hide-scrollbars',
      '--disable-dev-shm-usage', '--remote-debugging-port=0',
      `--user-data-dir=${profile}`, 'about:blank',
    ];
    const proc = spawn(chromium, args, { stdio: ['ignore', 'ignore', 'pipe'], detached: true });
    const portFile = join(profile, 'DevToolsActivePort');
    let port = 0;
    let list = null;
    // A reused profile already contains yesterday's DevToolsActivePort. Read, fetch and
    // retry: trusting the first file value points at a dead port and the whole run dies.
    for (let i = 0; i < 200; i++) {
      await sleep(100);
      try {
        const txt = readFileSync(portFile, 'utf8').split('\n');
        if (!txt[0]) continue;
        const candidate = Number(txt[0]);
        const res = await fetch(`http://127.0.0.1:${candidate}/json/list`);
        if (!res.ok) continue;
        list = await res.json();
        port = candidate;
        break;
      } catch { /* not written yet, or stale */ }
    }
    if (!port || !list) throw new Error(`chromium did not open a devtools port in ${profile}`);
    const target = list.find((t) => t.type === 'page');
    if (!target) throw new Error('chromium has no page target');
    const ws = new WebSocket(target.webSocketDebuggerUrl);
    const page = new Page(ws);
    ws.addEventListener('message', (ev) => {
      try { page.handle(JSON.parse(ev.data)); } catch { /* ignore malformed */ }
    });
    await new Promise((res, rej) => {
      ws.addEventListener('open', res, { once: true });
      ws.addEventListener('error', rej, { once: true });
    });
    await page.send('Runtime.enable');
    await page.send('Page.enable');
    await page.send('Log.enable');
    await page.send('Network.enable');
    // Headless Chromium treats element.focus() and clipboard reads as unfocused unless
    // focus emulation is on. Without it a working focus return or a working Copy
    // button reads as a bug.
    try { await page.send('Emulation.setFocusEmulationEnabled', { enabled: true }); } catch { /* older chromium */ }
    const b = new Browser(proc, page, profile, dir);
    await b.viewport(width, height);
    return b;
  }

  async viewport(width, height, mobile = width < 600) {
    await this.page.send('Emulation.setDeviceMetricsOverride', {
      width, height, deviceScaleFactor: 1, mobile,
    });
  }

  async eval(expression) {
    const r = await this.page.send('Runtime.evaluate', {
      expression, awaitPromise: true, returnByValue: true, userGesture: true,
    });
    if (r.exceptionDetails) {
      throw new Error(r.exceptionDetails.exception?.description || r.exceptionDetails.text || 'evaluate failed');
    }
    return r.result.value;
  }

  async open(url, { wait = 'document.readyState === "complete"', timeoutMs = 20000 } = {}) {
    await this.page.send('Page.navigate', { url });
    await this.waitFor(wait, timeoutMs);
    return this;
  }

  async reload(options = {}) {
    const url = await this.eval('location.href');
    return this.open(url, options);
  }

  async waitFor(expression, timeoutMs = 10000, intervalMs = 100) {
    const end = Date.now() + timeoutMs;
    let last = null;
    while (Date.now() < end) {
      try {
        last = await this.eval(`Boolean(${expression})`);
        if (last) return true;
      } catch (err) {
        last = err.message;
      }
      await sleep(intervalMs);
    }
    throw new Error(`waitFor timed out: ${expression}${last ? ` (last: ${last})` : ''}`);
  }

  async click(selector) {
    const ok = await this.eval(`(() => {
      const el = document.querySelector(${JSON.stringify(selector)});
      if (!el) return false;
      el.click();
      return true;
    })()`);
    if (!ok) throw new Error(`click: no element for ${selector}`);
    return this;
  }

  // type writes a value and fires the events a real keyboard would: the setup and lobby
  // inputs listen for input/change, not just a value assignment.
  async type(selector, text) {
    const ok = await this.eval(`(() => {
      const el = document.querySelector(${JSON.stringify(selector)});
      if (!el) return false;
      el.focus();
      el.value = ${JSON.stringify(String(text))};
      el.dispatchEvent(new Event('input', { bubbles: true }));
      el.dispatchEvent(new Event('change', { bubbles: true }));
      return true;
    })()`);
    if (!ok) throw new Error(`type: no element for ${selector}`);
    return this;
  }

  async press(key, selector = null) {
    return this.eval(`(() => {
      const target = ${selector ? `document.querySelector(${JSON.stringify(selector)})` : 'document.activeElement || document.body'};
      if (!target) return false;
      target.dispatchEvent(new KeyboardEvent('keydown', { key: ${JSON.stringify(key)}, bubbles: true, cancelable: true }));
      return true;
    })()`);
  }

  async text(selector) {
    return this.eval(`(() => {
      const el = document.querySelector(${JSON.stringify(selector)});
      return el ? el.textContent.trim() : null;
    })()`);
  }

  async visible(selector) {
    return this.eval(`(() => {
      const el = document.querySelector(${JSON.stringify(selector)});
      if (!el) return false;
      const r = el.getBoundingClientRect();
      const s = getComputedStyle(el);
      return r.width > 0 && r.height > 0 && s.display !== 'none' && s.visibility !== 'hidden';
    })()`);
  }

  // box returns the bounding box and display of a selector, or null.
  async box(selector) {
    return this.eval(`(() => {
      const el = document.querySelector(${JSON.stringify(selector)});
      if (!el) return null;
      const r = el.getBoundingClientRect();
      const s = getComputedStyle(el);
      return { x: r.x, y: r.y, w: r.width, h: r.height, right: r.right, bottom: r.bottom, display: s.display, justify: s.justifyContent };
    })()`);
  }

  async shot(file) {
    const r = await this.page.send('Page.captureScreenshot', { format: 'png' });
    mkdirSync(dirname(file), { recursive: true });
    writeFileSync(file, Buffer.from(r.data, 'base64'));
    return file;
  }

  errors() {
    return this.page.events;
  }

  // writeErrors writes the console/network log beside a run, so a failure can be
  // read without replaying it.
  writeErrors(file) {
    writeFileSync(file, JSON.stringify(this.page.events, null, 2) + '\n');
  }

  async close() {
    try { process.kill(-this.proc.pid, 'SIGTERM'); } catch { /* already gone */ }
    await sleep(300);
    try { process.kill(-this.proc.pid, 'SIGKILL'); } catch { /* already gone */ }
  }
}

// CLI smoke for the kit itself: opens a URL, prints the title, optionally a screenshot.
if (import.meta.url === `file://${process.argv[1]}`) {
  const args = process.argv.slice(2);
  const arg = (name, dflt) => {
    const i = args.indexOf(`--${name}`);
    return i >= 0 ? args[i + 1] : dflt;
  };
  const url = arg('url', 'http://127.0.0.1:8399/');
  const profile = arg('profile', join(tmpdir(), `p3-browse-cli-${Date.now()}`));
  const shot = arg('shot', null);
  const b = await Browser.start({ profile });
  try {
    await b.open(url);
    console.log(JSON.stringify({ title: await b.eval('document.title'), errors: b.errors(), shot: shot ? await b.shot(shot) : null }, null, 2));
  } finally {
    await b.close();
  }
}
