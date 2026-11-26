#!/usr/bin/env python3
"""Synthesise the sound effects the CrispCards sampler does not cover.

Everything is generated from scratch with numpy (no samples, no third-party
audio), then encoded to Ogg Vorbis with ffmpeg. Output goes to
src/regicide/server/web/sfx/ as synth_<event>.ogg.

Usage: python3 tools/assets/synth_sfx.py
"""
from __future__ import annotations

import pathlib
import subprocess
import tempfile

import numpy as np

SR = 44100
OUT = pathlib.Path("src/regicide/server/web/sfx")


def env(n: int, attack: float, decay: float) -> np.ndarray:
    a = max(1, int(attack * SR))
    d = max(1, int(decay * SR))
    e = np.ones(n)
    e[:a] = np.linspace(0, 1, a)
    tail = np.exp(-np.linspace(0, 6, min(d, n)))
    e[-len(tail):] *= tail
    return e


def tone(freq: float, dur: float, shape: str = "sine", detune: float = 0.0) -> np.ndarray:
    t = np.arange(int(dur * SR)) / SR
    f = freq * (1 + detune)
    if shape == "sine":
        return np.sin(2 * np.pi * f * t)
    if shape == "tri":
        return 2 * np.abs(2 * ((t * f) % 1) - 1) - 1
    if shape == "saw":
        return 2 * ((t * f) % 1) - 1
    return np.sign(np.sin(2 * np.pi * f * t))


def noise(dur: float, seed: int) -> np.ndarray:
    rng = np.random.default_rng(seed)
    return rng.uniform(-1, 1, int(dur * SR))


def lowpass(x: np.ndarray, alpha: float) -> np.ndarray:
    y = np.empty_like(x)
    acc = 0.0
    for i, v in enumerate(x):
        acc += alpha * (v - acc)
        y[i] = acc
    return y


def highpass(x: np.ndarray, alpha: float) -> np.ndarray:
    return x - lowpass(x, alpha)


def mix(*parts: np.ndarray) -> np.ndarray:
    n = max(len(p) for p in parts)
    out = np.zeros(n)
    for p in parts:
        out[: len(p)] += p
    peak = float(np.max(np.abs(out))) or 1.0
    return out / peak * 0.85


def write(name: str, samples: np.ndarray) -> None:
    OUT.mkdir(parents=True, exist_ok=True)
    stereo = np.stack([samples, samples], axis=1)
    pcm = (np.clip(stereo, -1, 1) * 32767).astype(np.int16)
    with tempfile.NamedTemporaryFile(suffix=".wav", delete=False) as tmp:
        import wave

        with wave.open(tmp.name, "wb") as w:
            w.setnchannels(2)
            w.setsampwidth(2)
            w.setframerate(SR)
            w.writeframes(pcm.tobytes())
        target = OUT / f"synth_{name}.ogg"
        subprocess.run(
            ["ffmpeg", "-y", "-loglevel", "error", "-i", tmp.name, "-c:a", "libvorbis", "-q:a", "4", str(target)],
            check=True,
        )
    print(f"wrote {target} ({target.stat().st_size} bytes)")


def enemy_attack() -> np.ndarray:
    thud = tone(70, 0.35, "sine") * env(int(0.35 * SR), 0.005, 0.32)
    growl = lowpass(noise(0.3, 11), 0.02) * env(int(0.3 * SR), 0.01, 0.28)
    return mix(thud, growl * 0.8)


def defeat() -> np.ndarray:
    t = np.arange(int(0.9 * SR)) / SR
    sweep = np.sin(2 * np.pi * np.cumsum(np.linspace(320, 60, len(t))) / SR)
    body = lowpass(noise(0.9, 23), 0.01) * 0.5
    return mix(sweep * env(len(t), 0.01, 0.85), body)


def shield() -> np.ndarray:
    ping = tone(1450, 0.35, "sine") * env(int(0.35 * SR), 0.002, 0.33)
    ring = tone(2180, 0.35, "sine") * env(int(0.35 * SR), 0.002, 0.3) * 0.4
    return mix(ping, ring)


def combo() -> np.ndarray:
    notes = [523.25, 659.25, 783.99, 1046.5]
    parts = []
    for i, f in enumerate(notes):
        seg = tone(f, 0.12, "tri") * env(int(0.12 * SR), 0.004, 0.11)
        parts.append(np.concatenate([np.zeros(int(i * 0.05 * SR)), seg]))
    return mix(*parts)


def jester() -> np.ndarray:
    t = np.arange(int(0.45 * SR)) / SR
    wobble = np.sin(2 * np.pi * np.cumsum(400 + 260 * np.sin(2 * np.pi * 5 * t)) / SR)
    sparkle = highpass(noise(0.45, 7), 0.25) * 0.25
    return mix(wobble * env(len(t), 0.01, 0.42), sparkle)


def ui_click() -> np.ndarray:
    click = highpass(noise(0.05, 3), 0.5) * env(int(0.05 * SR), 0.001, 0.045)
    body = tone(880, 0.05, "sine") * env(int(0.05 * SR), 0.001, 0.045) * 0.5
    return mix(click, body)


def tier() -> np.ndarray:
    chord = [523.25, 659.25, 783.99, 1046.5]
    parts = [tone(f, 1.1, "sine") * env(int(1.1 * SR), 0.01, 1.0) for f in chord]
    return mix(*parts)


def main() -> None:
    write("enemy_attack", enemy_attack())
    write("defeat", defeat())
    write("shield", shield())
    write("combo", combo())
    write("jester", jester())
    write("ui_click", ui_click())
    write("tier", tier())


if __name__ == "__main__":
    main()
