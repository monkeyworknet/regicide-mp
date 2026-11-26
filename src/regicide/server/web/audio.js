// AudioManager: Web Audio buffers, gesture unlock, mute/volume persistence.
// No dependencies; decodes Ogg Vorbis once at boot and plays from memory.

const PREFS_KEY = 'regicide.audio';

export class AudioManager {
  constructor() {
    this.ctx = null;
    this.gain = null;
    this.buffers = new Map(); // url -> AudioBuffer
    this.manifest = {};
    this.enabled = true;
    this.volume = 0.6;
    this.unlocked = false;
    this.voices = 0;
    this.maxVoices = 6;
    this.loadPrefs();
  }

  loadPrefs() {
    try {
      const raw = localStorage.getItem(PREFS_KEY);
      if (raw) {
        const p = JSON.parse(raw);
        this.enabled = p.enabled !== false;
        this.volume = typeof p.volume === 'number' ? Math.min(1, Math.max(0, p.volume)) : 0.6;
      }
    } catch {
      /* ignore corrupt prefs */
    }
  }

  savePrefs() {
    try {
      localStorage.setItem(PREFS_KEY, JSON.stringify({ enabled: this.enabled, volume: this.volume }));
    } catch {
      /* storage may be unavailable (private mode) */
    }
  }

  async init() {
    try {
      const res = await fetch('/data/audio.json');
      if (res.ok) {
        const json = await res.json();
        this.manifest = json.files || {};
      }
    } catch {
      this.manifest = {};
    }
    await this.preload();
    this.installUnlock();
  }

  uniqueFiles() {
    const set = new Set();
    for (const list of Object.values(this.manifest)) {
      for (const f of list) set.add(f);
    }
    return [...set];
  }

  async preload() {
    const files = this.uniqueFiles();
    await Promise.all(
      files.map(async (file) => {
        const url = `/sfx/${file}`;
        if (this.buffers.has(url)) return;
        try {
          const res = await fetch(url);
          if (!res.ok) return;
          const raw = await res.arrayBuffer();
          this.buffers.set(url, raw); // decoded lazily once the context exists
        } catch {
          /* a missing sound must never break the game */
        }
      }),
    );
  }

  installUnlock() {
    const unlock = async () => {
      await this.ensureContext();
      if (this.ctx && this.ctx.state === 'suspended') {
        try {
          await this.ctx.resume();
        } catch {
          /* ignore */
        }
      }
      this.unlocked = this.ctx !== null;
      window.removeEventListener('pointerdown', unlock);
      window.removeEventListener('keydown', unlock);
      window.removeEventListener('touchstart', unlock);
    };
    window.addEventListener('pointerdown', unlock, { passive: true });
    window.addEventListener('keydown', unlock);
    window.addEventListener('touchstart', unlock, { passive: true });
  }

  async ensureContext() {
    if (this.ctx) return this.ctx;
    const Ctx = window.AudioContext || window.webkitAudioContext;
    if (!Ctx) return null;
    this.ctx = new Ctx();
    this.gain = this.ctx.createGain();
    this.gain.gain.value = this.enabled ? this.volume : 0;
    const limiter = this.ctx.createDynamicsCompressor();
    limiter.threshold.value = -12;
    limiter.ratio.value = 6;
    this.gain.connect(limiter).connect(this.ctx.destination);

    // Decode in the background; play() falls back to silence until ready.
    for (const [url, raw] of this.buffers) {
      if (raw instanceof ArrayBuffer) {
        this.ctx
          .decodeAudioData(raw.slice(0))
          .then((buf) => this.buffers.set(url, buf))
          .catch(() => this.buffers.delete(url));
      }
    }
    return this.ctx;
  }

  setEnabled(on) {
    this.enabled = Boolean(on);
    this.savePrefs();
    if (this.gain) this.gain.gain.value = this.enabled ? this.volume : 0;
  }

  setVolume(v) {
    this.volume = Math.min(1, Math.max(0, Number(v)));
    this.savePrefs();
    if (this.gain) this.gain.gain.value = this.enabled ? this.volume : 0;
  }

  // play(event) picks a random take for the event and fires it.
  async play(event) {
    if (!this.enabled || this.voices >= this.maxVoices) return;
    const files = this.manifest[event];
    if (!files || files.length === 0) return;
    const file = files[Math.floor(Math.random() * files.length)];
    const url = `/sfx/${file}`;
    let buffer = this.buffers.get(url);
    if (!(buffer instanceof AudioBuffer)) {
      const ctx = await this.ensureContext();
      if (!ctx) return;
      const raw = this.buffers.get(url);
      if (!(raw instanceof ArrayBuffer)) return;
      try {
        buffer = await ctx.decodeAudioData(raw.slice(0));
        this.buffers.set(url, buffer);
      } catch {
        return;
      }
    }
    this.start(buffer);
  }

  start(buffer) {
    if (!this.ctx) return;
    const src = this.ctx.createBufferSource();
    src.buffer = buffer;
    src.connect(this.gain);
    this.voices += 1;
    src.onended = () => {
      this.voices = Math.max(0, this.voices - 1);
    };
    src.start();
  }

  // playMany fires a sequence of events with a small gap (used for animations).
  async sequence(events, gapMs = 120) {
    for (const event of events) {
      this.play(event);
      await new Promise((r) => setTimeout(r, gapMs));
    }
  }
}
