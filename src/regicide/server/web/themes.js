// Theme registry. The server publishes /data/themes.json, generated from the art
// that is actually on disk (tools/assets/build_theme_registry.py), so the browser
// never offers a card set whose faces are missing.
//
// A theme carries: id, label, dir (image prefix), hasText (the art has a blank
// middle for the rule text), defaultBack / defaultJoker (what the set brings with
// it) and optionally pixelated (low-resolution art that must be scaled with
// nearest-neighbour).

const FALLBACK = {
  version: 1,
  default: 'official',
  defaultBack: 'black',
  themes: [{ id: 'official', label: 'Official', dir: '/img/official', defaultJokers: ['official', 'official'] }],
  backs: [{ id: 'black', label: 'Black', path: '/img/backs/black.webp' }],
  defaultJoker: 'official',
  jokers: [{ id: 'official', label: 'Original', path: '/img/jokers/official.webp' }],
  furniture: {},
};

let registry = FALLBACK;
let loading = null;

export async function loadThemes() {
  if (loading) return loading;
  loading = fetch('/data/themes.json')
    .then((r) => (r.ok ? r.json() : FALLBACK))
    .then((j) => {
      if (!j || !Array.isArray(j.themes) || j.themes.length === 0) return FALLBACK;
      registry = {
        ...FALLBACK,
        ...j,
        furniture: j.furniture || {},
        backs: j.backs && j.backs.length ? j.backs : FALLBACK.backs,
        jokers: j.jokers && j.jokers.length ? j.jokers : FALLBACK.jokers,
      };
      return registry;
    })
    .catch(() => registry);
  return loading;
}

export function themes() {
  return registry.themes;
}

export function backs() {
  return registry.backs;
}

export function themeInfo(id) {
  return registry.themes.find((t) => t.id === id) || registry.themes[0];
}

export function backInfo(id) {
  return registry.backs.find((b) => b.id === id) || registry.backs[0];
}

export function jokers() {
  return registry.jokers;
}

export function jokerInfo(id) {
  const list = registry.jokers && registry.jokers.length ? registry.jokers : FALLBACK.jokers;
  return list.find((j) => j.id === id) || list.find((j) => j.id === registryDefaultJoker()) || list[0];
}

// resolveJokerId is the one answer to "which face does this run show?": the pick if
// it still exists, otherwise the set's own face. The preview and the board both use
// it, so a retired id can never show two different faces.
export function resolveJokerId(themeId, jokerId) {
  const list = registry.jokers && registry.jokers.length ? registry.jokers : FALLBACK.jokers;
  if (jokerId && list.some((j) => j.id === jokerId)) return jokerId;
  return themeJoker(themeId);
}

export function registryDefaultJoker() {
  return registry.defaultJoker || FALLBACK.defaultJoker;
}

// themeJoker is the first face a set brings with it (compatibility for a single
// slot). A set with no fixed pair falls back to the shared default, never to a
// random face: a render must be stable.
export function themeJoker(themeId) {
  const pair = themeJokers(themeId);
  if (pair) return pair[0];
  return registryDefaultJoker();
}

// themeJokers is the pair a set brings with it, or null when the set asks the client
// to choose two (Simple and Standard).
export function themeJokers(themeId) {
  const pair = themeInfo(themeId).defaultJokers;
  const list = registry.jokers && registry.jokers.length ? registry.jokers : FALLBACK.jokers;
  if (!Array.isArray(pair) || pair.length === 0) return null;
  const valid = pair.filter((id) => list.some((j) => j.id === id));
  if (valid.length === 0) return null;
  return [valid[0], valid[1] || valid[0]];
}

// randomJokerPair picks two faces for a set with no fixed pair. The pool excludes the
// set-specific faces (Original, Ditto, Porygon) and the shared Jester, so Simple and
// Standard are dealt from the historical sheet.
export function randomJokerPair() {
  const list = registry.jokers && registry.jokers.length ? registry.jokers : FALLBACK.jokers;
  const fixed = new Set(['official', 'jester', 'ditto', 'porygon']);
  const pool = list.map((j) => j.id).filter((id) => !fixed.has(id));
  if (pool.length === 0) return [registryDefaultJoker(), registryDefaultJoker()];
  const first = pool[Math.floor(Math.random() * pool.length)];
  const rest = pool.filter((id) => id !== first);
  const second = rest.length ? rest[Math.floor(Math.random() * rest.length)] : first;
  return [first, second];
}

// jokerPairFor is "what should this set show right now?": its fixed pair, or a fresh
// random pair for a set that asks for one.
export function jokerPairFor(themeId) {
  return themeJokers(themeId) || randomJokerPair();
}

export function furniture(name) {
  return registry.furniture[name] || null;
}

// artUrl resolves one card face for a theme, falling back to the default theme so
// a stale preference can never leave the board blank.
export function artUrl(themeId, cardId) {
  const theme = themeInfo(themeId);
  return `${theme.dir}/${String(cardId).toLowerCase()}.webp`;
}

export function registryDefaultBack() {
  return registry.defaultBack || backInfo(FALLBACK.defaultBack).id;
}

export function registryDefaultTheme() {
  return registry.default || themeInfo(FALLBACK.default).id;
}

export function backUrl(backId) {
  return backInfo(backId).path;
}

// jesterArt returns the two Jester faces, in order. A single id is allowed and is
// used twice; no id falls back to the set's own pair.
export function jesterArt(themeId, jokers) {
  const list = registry.jokers && registry.jokers.length ? registry.jokers : FALLBACK.jokers;
  const pair = Array.isArray(jokers) ? jokers : jokers ? [jokers, jokers] : null;
  const chosen = pair && pair.length
    ? pair
    : (() => {
      const own = themeJokers(themeId);
      return own || [themeJoker(themeId), themeJoker(themeId)];
    })();
  return [0, 1].map((i) => {
    const id = chosen[i] || chosen[0];
    return (list.find((j) => j.id === id) || jokerInfo(registryDefaultJoker())).path;
  });
}
