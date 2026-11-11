'use strict';

const PAGE_SIZE = 50;
const SEARCH_DELAY_MS = 250;
// The server returns at most 200 items; one is asked for beyond the page to learn whether more exist.
const MAX_RELOAD = 199;

const state = {
  user: null,
  view: 'list',
  tag: 'all',
  link: 'all',
  query: '',
  bookmarks: [],
  hasMore: false,
  tags: [],
  links: [],
  users: [],
  editing: null,
  confirming: null,
  confirmingUser: null,
  resettingUser: null,
  // Select mode: `selected` holds ids of loaded bookmarks only; `anchor` is where a Shift-click range starts.
  selecting: false,
  selected: new Set(),
  anchor: null,
  bulkPanel: null,
  bulkError: '',
  bulkBusy: false,
  // Set while links are being checked: { done, total, stop }.
  checking: null,
};
const MAX_TAG_CHARS = 50;

const $ = (id) => document.getElementById(id);

/* ---------- small helpers ---------- */

function el(tag, props = {}, ...children) {
  const node = document.createElement(tag);
  for (const [key, value] of Object.entries(props)) {
    if (value == null || value === false) continue;
    if (key === 'class') node.className = value;
    else if (key.startsWith('on')) node.addEventListener(key.slice(2), value);
    else node.setAttribute(key, value === true ? '' : value);
  }
  node.append(...children.flat().filter((c) => c != null));
  return node;
}

// A second click or Enter while the first request is still running would repeat the action.
function once(handler) {
  let running = false;
  return async (...args) => {
    // Without this, a repeated Enter would submit the form natively and put its fields in the URL.
    args[0]?.preventDefault?.();
    if (running) return;
    running = true;
    try { await handler(...args); } finally { running = false; }
  };
}

const MAX_TERMS = 8;
const MAX_TERM_CHARS = 100;
const SEARCH_FIELDS = ['title', 'url', 'note', 'tag', 'status'];
const isSpace = (character) => /^\p{White_Space}$/u.test(character);

// Reads `title:`-style prefixes, quotes and `-` exclusions exactly as src/search.rs does;
// tests/search_syntax.json holds the cases both parsers must agree on.
// Returns [{ field: 'any' | 'title' | 'url' | 'note' | 'tag' | 'status', text, negated }].
function parseQuery(query) {
  const chars = Array.from(query);
  const terms = [];
  let at = 0;

  function readField() {
    let end = at;
    while (end < chars.length && /^\p{Alphabetic}$/u.test(chars[end])) end += 1;
    const name = chars.slice(at, end).join('').toLowerCase();
    if (chars[end] !== ':' || !SEARCH_FIELDS.includes(name)) return 'any';
    at = end + 1;
    return name;
  }

  function readValue() {
    let value = '';
    if (chars[at] !== '"') {
      while (at < chars.length && !isSpace(chars[at])) value += chars[at++];
      return value;
    }
    at += 1;
    while (at < chars.length) {
      const character = chars[at++];
      if (character === '"') break;
      if (character === '\\' && chars[at] === '"') {
        value += '"';
        at += 1;
      } else {
        value += character;
      }
    }
    return value;
  }

  while (at < chars.length && terms.length < MAX_TERMS) {
    if (isSpace(chars[at])) {
      at += 1;
      continue;
    }
    const start = at;
    let negated = chars[at] === '-' && at + 1 < chars.length && !isSpace(chars[at + 1]);
    if (negated) at += 1;
    let field = readField();
    let text = readValue();
    const blank = Array.from(text).every(isSpace);
    if (blank && field === 'any') continue;
    if (blank) {
      // A prefix with nothing (or only spaces) after it is what the user typed, not a filter.
      field = 'any';
      negated = false;
      text = chars.slice(start, at).join('');
    }
    terms.push({ field, text: Array.from(text).slice(0, MAX_TERM_CHARS).join(''), negated });
  }
  return terms;
}

// What to highlight in one field: terms that must match there, never excluded ones.
function termsFor(terms, field) {
  return terms.filter((term) => !term.negated && (term.field === 'any' || term.field === field)).map((term) => term.text);
}

function highlight(text, terms) {
  if (!terms.length) return [text];
  const escaped = terms.map((term) => term.replace(/[.*+?^${}()|[\]\\]/g, '\\$&'));
  // The capture group makes split() keep the matched text at odd indexes.
  return text.split(new RegExp(`(${escaped.join('|')})`, 'giu')).map((part, i) => (i % 2 ? el('mark', {}, part) : part));
}

function domainOf(url) {
  try {
    return new URL(url).hostname.replace(/^www\./, '');
  } catch {
    return url;
  }
}

function plural(count, unit) {
  return `${count} ${unit}${count === 1 ? '' : 's'} ago`;
}

function timeAgo(unixSeconds) {
  const hours = Math.floor((Date.now() - unixSeconds * 1000) / 36e5);
  if (hours < 1) return 'just now';
  if (hours < 24) return plural(hours, 'hour');
  const days = Math.floor(hours / 24);
  if (days === 1) return 'yesterday';
  if (days < 7) return plural(days, 'day');
  if (days < 30) return plural(Math.floor(days / 7), 'week');
  return plural(Math.floor(days / 30), 'month');
}

// Language, date order and time zone are the browser's: `undefined` means "use the settings".
const exactTime = new Intl.DateTimeFormat(undefined, { dateStyle: 'medium', timeStyle: 'short' });

// The age as text, with the exact moment in the hover text.
function savedTime(unixSeconds) {
  const moment = new Date(unixSeconds * 1000);
  return el('time', { datetime: moment.toISOString(), title: exactTime.format(moment) }, timeAgo(unixSeconds));
}

function parseTags(text) {
  return [...new Set(text.split(',').map((t) => t.trim().toLowerCase()).filter(Boolean))];
}

function normalizeUrl(text) {
  try {
    const url = new URL(String(text).trim());
    return url.protocol === 'http:' || url.protocol === 'https:' ? url.href : null;
  } catch {
    return null;
  }
}

function showError(node, message) {
  node.textContent = message;
  node.hidden = !message;
}

let toastTimer;
function toast(message) {
  const node = $('toast');
  clearTimeout(toastTimer);
  // Clear first so screen readers announce repeated messages.
  node.textContent = '';
  requestAnimationFrame(() => { node.textContent = message; });
  toastTimer = setTimeout(() => { node.textContent = ''; }, 3500);
}

/* ---------- server calls ---------- */

class ApiError extends Error {
  constructor(status, message, retryAfter) {
    super(message);
    this.status = status;
    this.retryAfter = retryAfter;
  }
}

// A File is sent as it is, as HTML; anything else is JSON.
async function api(method, path, body) {
  const isFile = body instanceof Blob;
  const contentType = isFile ? 'text/html' : 'application/json';
  const response = await fetch(`/api${path}`, {
    method,
    headers: body === undefined ? {} : { 'Content-Type': contentType },
    body: body === undefined || isFile ? body : JSON.stringify(body),
    credentials: 'same-origin',
  });
  let data = null;
  if (response.status !== 204) {
    try { data = await response.json(); } catch { /* a proxy error page is not JSON */ }
  }
  if (response.ok) return data;
  throw refusal(response, data);
}

function refusal(response, data) {
  let fallback = 'The request was refused.';
  if (response.status >= 500) fallback = 'The server had a problem. Try again.';
  if (response.status === 413) fallback = 'That file is too large. The limit is 20 MB.';
  return new ApiError(response.status, data?.error ?? fallback, response.headers.get('Retry-After'));
}

// Calls `onEvent(name, data)` for each server-sent event of a POST. Stops quietly when `signal` aborts.
async function streamEvents(path, body, signal, onEvent) {
  let response;
  try {
    response = await fetch(`/api${path}`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body),
      credentials: 'same-origin',
      signal,
    });
  } catch (error) {
    if (error.name === 'AbortError') return;
    throw error;
  }
  if (!response.ok) {
    const data = await response.json().catch(() => null);
    throw refusal(response, data);
  }
  const reader = response.body.pipeThrough(new TextDecoderStream()).getReader();
  let buffered = '';
  let ended = false;
  try {
    for (;;) {
      const { value, done } = await reader.read();
      if (done) break;
      buffered += value;
      const events = buffered.split('\n\n');
      buffered = events.pop();
      for (const event of events) {
        const lines = event.split('\n');
        const name = lines.find((line) => line.startsWith('event: '))?.slice(7);
        const data = lines.filter((line) => line.startsWith('data: ')).map((line) => line.slice(6)).join('\n');
        if (name === 'done') ended = true;
        if (name) onEvent(name, data ? JSON.parse(data) : null);
      }
    }
  } catch (error) {
    if (error.name === 'AbortError') return;
    throw error;
  }
  // The server always ends with `done`; a stream that stops short was cut off.
  if (!ended && !signal.aborted) throw new TypeError('The connection was cut off.');
}

function describe(error) {
  if (!(error instanceof ApiError)) return 'Cannot reach the server. Check your connection and try again.';
  if (error.status === 429) {
    const seconds = Number(error.retryAfter);
    return seconds > 0 ? `Too many attempts. Try again in ${seconds} seconds.` : 'Too many attempts. Try again later.';
  }
  return error.message;
}

// Runs a server call. A failure goes to `onError` (a form's error line) or to a toast.
// Returns `{ value }` on success and `null` on failure, so a call that returns nothing still counts as done.
async function attempt(action, onError = toast) {
  const wasSignedIn = Boolean(state.user);
  try {
    return { value: await action() };
  } catch (error) {
    if (error instanceof ApiError && error.status === 401 && wasSignedIn) {
      // Several requests may fail together; only the first one ends the session.
      if (state.user) endSession('Your session ended. Sign in again.');
      return null;
    }
    onError(describe(error));
    return null;
  }
}

/* ---------- loading data ---------- */

let listTicket = 0;
// `first` starts a new list, `keep` refreshes what is loaded now, `append` adds the next page.
async function loadBookmarks({ mode = 'first' } = {}) {
  if (!state.user) return;
  const ticket = ++listTicket;
  const append = mode === 'append';
  const wanted = mode === 'keep' ? Math.min(MAX_RELOAD, Math.max(PAGE_SIZE, state.bookmarks.length)) : PAGE_SIZE;
  const params = new URLSearchParams({ limit: wanted + 1, offset: append ? state.bookmarks.length : 0 });
  if (state.query.trim()) params.set('q', state.query.trim());
  if (state.tag !== 'all') params.set('tag', state.tag);
  if (state.link !== 'all') params.set('status', state.link);
  const result = await attempt(() => api('GET', `/bookmarks?${params}`));
  // A newer request owns the list now; showing this answer would put old results on screen.
  if (!result || ticket !== listTicket || !state.user) return;
  // A refresh must not rebuild the list under an open edit or confirmation; a new filter replaces both anyway.
  if (mode !== 'first' && (state.editing || state.confirming)) return;
  const page = result.value.bookmarks.slice(0, wanted);
  state.bookmarks = append ? state.bookmarks.concat(page) : page;
  state.hasMore = result.value.bookmarks.length > wanted;
  pruneSelection();
  renderList();
}

async function loadTags() {
  if (!state.user) return;
  const result = await attempt(() => api('GET', '/tags'));
  if (!result || !state.user) return;
  state.tags = result.value.tags;
  state.links = result.value.links;
  // The filter's last bookmark is gone (deleted, retagged, or re-checked), so there is nothing left to show.
  const tagGone = state.tag !== 'all' && !state.tags.some((t) => t.tag === state.tag);
  const linkGone = state.link !== 'all' && !state.links.some((l) => l.status === state.link);
  if (tagGone || linkGone) {
    if (tagGone) state.tag = 'all';
    if (linkGone) state.link = 'all';
    syncHash();
    await loadBookmarks();
  }
  renderSide();
}

async function loadUsers() {
  if (!state.user) return;
  const result = await attempt(() => api('GET', '/users'));
  if (!result || !state.user) return;
  state.users = result.value.users;
  renderUsers();
}

async function refreshAll() {
  closePick();
  await Promise.all([loadBookmarks({ mode: 'keep' }), loadTags()]);
}

/* ---------- sidebar ---------- */

function filterLink({ label, count, current, choose }) {
  return el('li', {}, el('a', {
    href: '#',
    'aria-current': state.view === 'list' && current ? 'page' : null,
    onclick: (e) => { e.preventDefault(); choose(); },
  }, label, count == null ? null : el('span', { class: 'count' }, String(count))));
}

// Worst first: what needs a look comes before what is fine.
const LINK_FILTERS = [
  ['broken', 'Broken'], ['redirected', 'Moved'], ['unknown', 'Not confirmed'], ['ok', 'OK'], ['unchecked', 'Not checked'],
];

function linkFilterLinks() {
  const counts = new Map(state.links.map(({ status, count }) => [status, count]));
  return LINK_FILTERS.filter(([status]) => counts.has(status)).map(([status, label]) => filterLink({
    label, count: counts.get(status), current: state.link === status, choose: () => setLinkFilter(status),
  }));
}

function renderSide() {
  $('filters').replaceChildren(filterLink({
    label: 'All bookmarks', current: state.tag === 'all' && state.link === 'all', choose: () => setFilter('all'),
  }));
  $('link-list').replaceChildren(...linkFilterLinks());
  $('link-heading').hidden = !$('link-list').children.length;
  $('tag-list').replaceChildren(...state.tags.map(({ tag, count }) => filterLink({
    label: tag, count, current: state.link === 'all' && state.tag === tag, choose: () => setFilter(tag),
  })));

  const manageUsersLink = el('a', { href: '#', onclick: (e) => { e.preventDefault(); showUsers(); } }, 'Manage users');
  const accountLink = el('a', { href: '#', onclick: (e) => { e.preventDefault(); showAccount(); } }, 'Change password');
  const transferLink = el('a', { href: '#', onclick: (e) => { e.preventDefault(); showTransfer(); } }, 'Import and export');
  $('me').replaceChildren(...[
    el('span', {}, 'Signed in as ', el('strong', {}, state.user.username)),
    transferLink,
    accountLink,
    state.user.is_admin ? manageUsersLink : null,
    el('button', { class: 'link', onclick: signOut }, 'Sign out'),
  ].filter(Boolean));
}

/* ---------- bookmark list ---------- */

function actionsFor(b) {
  if (state.confirming === b.id) {
    return el('div', { class: 'confirm' },
      el('span', { class: 'confirm-text' }, 'Delete this bookmark?'),
      el('button', { class: 'quiet danger', onclick: once(() => removeBookmark(b)) }, 'Delete'),
      el('button', { class: 'quiet', 'data-focus': true, onclick: () => { state.confirming = null; renderList(); } }, 'Keep'),
    );
  }
  return el('div', { class: 'actions' },
    el('button', { class: 'quiet', 'aria-label': `Edit ${b.title}`, onclick: () => { state.editing = b.id; renderList(); } }, 'Edit'),
    el('button', { class: 'quiet danger', 'aria-label': `Delete ${b.title}`, onclick: () => { state.confirming = b.id; renderList(); } }, 'Delete'),
  );
}

// Static markup, never built from data. `?` is plain text.
const LINK_ICONS = {
  ok: '<svg viewBox="0 0 16 16"><path d="M3 8.5l3.2 3.2L13 4.5"/></svg>',
  redirected: '<svg viewBox="0 0 16 16"><path d="M2.5 8h10.5M9 4l4 4-4 4"/></svg>',
  broken: '<svg viewBox="0 0 16 16"><path d="M4 4l8 8M12 4l-8 8"/></svg>',
  unknown: '?',
};

// What the last link check found. A bookmark never checked shows nothing. The label is for
// screen readers: the icon and colour alone do not say what the line means.
const LINK_NOTES = {
  ok: { label: 'Link OK', text: (_detail, moment) => `checked ${exactTime.format(moment)}` },
  redirected: { label: 'Link moved', text: (detail) => `now at ${detail}` },
  broken: { label: 'Broken link', text: (detail) => detail },
  unknown: { label: 'Link not confirmed', text: (detail) => detail },
};

function linkNote(b) {
  const note = b.link && LINK_NOTES[b.link.status];
  if (!note) return null;
  const moment = new Date(b.link.checked_at * 1000);
  const glyph = el('span', { class: 'glyph', 'aria-hidden': 'true' });
  glyph.innerHTML = LINK_ICONS[b.link.status];
  return el('p', { class: `link-note ${b.link.status}`, title: `Checked ${exactTime.format(moment)}` },
    glyph, el('span', { class: 'sr-only' }, `${note.label}: `), note.text(b.link.detail, moment));
}

function itemView(b) {
  if (state.editing === b.id) return editView(b);
  const terms = parseQuery(state.query.trim());
  // A `tag:` term matches a whole tag, so only that tag is highlighted.
  const tagTerms = (tag) => [
    ...termsFor(terms, 'any'),
    ...terms.filter((term) => !term.negated && term.field === 'tag' && term.text.trim().toLowerCase() === tag).map((term) => term.text.trim()),
  ];
  const isChosen = state.selected.has(b.id);
  const box = state.selecting ? el('label', { class: 'pick-box' },
    el('input', {
      type: 'checkbox', 'data-key': `row-${b.id}`, 'aria-label': `Select ${b.title}`, checked: isChosen,
      // `click`, not `change`: only a click event tells whether Shift was held.
      onclick: (e) => toggleRow(b, e.target.checked, e.shiftKey),
    })) : null;
  return el('li', { class: `item${isChosen ? ' chosen' : ''}${b.link ? ` link-${b.link.status}` : ''}`, 'data-id': b.id },
    box,
    el('span', { class: 'domain' }, highlight(domainOf(b.url), termsFor(terms, 'url'))),
    el('div', {},
      el('a', { class: 'title', href: normalizeUrl(b.url) || '#', target: '_blank', rel: 'noopener noreferrer' }, highlight(b.title, termsFor(terms, 'title'))),
      b.note ? el('p', { class: 'note' }, highlight(b.note, termsFor(terms, 'note'))) : null,
      linkNote(b),
      b.tags.length ? el('div', { class: 'tags' }, b.tags.map((t) => el('button', { class: 'tag', onclick: () => setFilter(t) }, highlight(t, tagTerms(t))))) : null,
    ),
    el('div', { class: 'when' }, state.confirming === b.id ? null : savedTime(b.created_at), actionsFor(b)),
  );
}

function editView(b) {
  const error = el('p', { class: 'err', role: 'alert', hidden: true });
  const form = el('form', { class: 'stack', novalidate: true, onsubmit: once(async (e) => {
    e.preventDefault();
    const data = new FormData(form);
    const url = normalizeUrl(data.get('url'));
    if (!url) return showError(error, 'Enter a full URL, such as https://example.com.');
    const body = { url, title: data.get('title').trim() || domainOf(url), note: data.get('note').trim(), tags: parseTags(data.get('tags')) };
    const result = await attempt(() => api('PUT', `/bookmarks/${b.id}`, body), (message) => showError(error, message));
    if (!result) return;
    state.editing = null;
    toast('Saved changes');
    await refreshAll();
  }) },
    el('label', {}, 'URL', el('input', { type: 'url', name: 'url', value: b.url, 'data-focus': true })),
    el('label', {}, 'Title', el('input', { type: 'text', name: 'title', value: b.title })),
    el('label', {}, 'Note', el('textarea', { name: 'note', rows: '2' }, b.note)),
    el('label', {}, 'Tags, separated by commas', el('input', { type: 'text', name: 'tags', value: b.tags.join(', ') })),
    error,
    el('div', {}, el('button', { type: 'submit' }, 'Save changes'), ' ',
      el('button', { type: 'button', class: 'quiet', onclick: () => { state.editing = null; renderList(); } }, 'Cancel')),
  );
  return el('li', { class: 'item editing' }, form);
}

function emptyMessage() {
  const query = state.query.trim();
  const linkLabel = LINK_FILTERS.find(([status]) => status === state.link)?.[1];
  if (query && linkLabel) return [`No “${linkLabel}” bookmarks match “${query}”. `, el('button', { class: 'link', onclick: clearSearch }, 'Clear the search'), '.'];
  if (query && state.tag !== 'all') return [`No bookmarks tagged “${state.tag}” match “${query}”. `, el('button', { class: 'link', onclick: clearSearch }, 'Clear the search'), '.'];
  if (query) return [`No bookmarks match “${query}”. `, el('button', { class: 'link', onclick: clearSearch }, 'Clear the search'), ' or add a new bookmark.'];
  if (linkLabel) return [`No “${linkLabel}” bookmarks.`];
  if (state.tag !== 'all') return [`No bookmarks tagged “${state.tag}”.`];
  return ['No bookmarks here yet. Add one with the form above.'];
}

function renderList() {
  const focusedKey = $('list').contains(document.activeElement) ? document.activeElement.dataset.key : null;
  $('list').className = `list${state.selecting ? ' selecting' : ''}`;
  $('list').replaceChildren(...state.bookmarks.map(itemView));
  // Without this the checkbox the user was on disappears and focus falls back to the page.
  if (focusedKey) $('list').querySelector(`[data-key="${focusedKey}"]`)?.focus({ preventScroll: true });
  renderBulk();
  $('empty').replaceChildren(...emptyMessage());
  $('empty').hidden = state.bookmarks.length > 0;
  $('more').hidden = !state.hasMore;
  const count = state.bookmarks.length;
  $('summary').textContent = state.hasMore ? `Showing the newest ${count}` : `${count} ${count === 1 ? 'bookmark' : 'bookmarks'}`;
  // Focus after the next paint so the form shows up first; the item the user clicked is already in view.
  requestAnimationFrame(() => keepFocus($('list')));
}

/* ---------- random pick ---------- */

let pickTicket = 0;

function closePick() {
  pickTicket++;
  $('pick').hidden = true;
  $('pick').replaceChildren();
}

// The Close button is gone once the pick closes; focus the button that opens a pick.
function dismissPick() {
  closePick();
  $('random').focus({ preventScroll: true });
}

function renderPick(b) {
  const parts = [
    el('span', { class: 'domain' }, domainOf(b.url)),
    el('a', { class: 'title', href: normalizeUrl(b.url) || '#', target: '_blank', rel: 'noopener noreferrer' }, b.title),
    b.note ? el('p', { class: 'note' }, b.note) : null,
    b.tags.length ? el('div', { class: 'tags' }, b.tags.map((t) => el('button', { class: 'tag', onclick: () => setFilter(t) }, t))) : null,
    el('div', { class: 'actions' },
      el('button', { id: 'pick-another', class: 'quiet', onclick: once(pickRandom) }, 'Another'),
      el('button', { class: 'quiet', onclick: dismissPick }, 'Close')),
  ];
  // replaceChildren() would print a null part as the text "null"; el() drops them.
  $('pick').replaceChildren(...parts.filter(Boolean));
  $('pick').hidden = false;
}

// Picks from what the search box and the tag filter show now, so "random" means "random of these".
async function pickRandom() {
  const ticket = ++pickTicket;
  const params = new URLSearchParams();
  if (state.query.trim()) params.set('q', state.query.trim());
  if (state.tag !== 'all') params.set('tag', state.tag);
  if (state.link !== 'all') params.set('status', state.link);
  // A 404 only means nothing matches; it is not a failure worth a server-error message.
  const result = await attempt(() => api('GET', `/bookmarks/random?${params}`)
    .catch((error) => { if (error instanceof ApiError && error.status === 404) return null; throw error; }));
  if (!result || ticket !== pickTicket || !state.user) return;
  if (!result.value) {
    closePick();
    return toast('No bookmarks to pick from. Clear the search or the filter first.');
  }
  renderPick(result.value);
  // The old button is gone after a rebuild. Focusing the title also makes a screen reader read what was picked.
  $('pick').querySelector('.title').focus({ preventScroll: true });
}

// After a rebuild the control the user was on is gone. Focus the form or confirmation that just
// opened, or else the page's main area, so keyboard users do not restart from the top of the page.
function keepFocus(container) {
  container.querySelector('[data-focus]')?.focus({ preventScroll: true });
  if (!document.activeElement || document.activeElement === document.body) {
    $('main').focus({ preventScroll: true });
  }
}

/* ---------- bulk actions ---------- */

const bookmarksOf = (count) => `${count} bookmark${count === 1 ? '' : 's'}`;

// The server accepts at most this many ids per request.
const MAX_BULK_IDS = 200;

function clearSelection() {
  state.selected.clear();
  state.anchor = null;
  state.bulkPanel = null;
  state.bulkError = '';
  // The ticks and the bar must not outlive the selection while a new list is loading.
  syncSelection();
}

// Select mode belongs to the bookmark list, so whatever replaces the list also ends the mode.
function leaveSelectMode() {
  state.selecting = false;
  if (state.checking) {
    state.checking.left = true;
    stopChecking();
  }
  clearSelection();
}

// A refresh may no longer contain a ticked bookmark; acting on it would touch a row the user cannot see.
function pruneSelection() {
  const loaded = new Set(state.bookmarks.map((b) => b.id));
  for (const id of state.selected) if (!loaded.has(id)) state.selected.delete(id);
}

function setSelecting(on) {
  closePick();
  state.selecting = on;
  clearSelection();
  if (on) Object.assign(state, { editing: null, confirming: null });
  renderList();
  $('select-toggle').focus({ preventScroll: true });
}

function rangeIds(fromId, toId) {
  const ids = state.bookmarks.map((b) => b.id);
  const from = ids.indexOf(fromId);
  const to = ids.indexOf(toId);
  if (from < 0 || to < 0) return [toId];
  return ids.slice(Math.min(from, to), Math.max(from, to) + 1);
}

function toggleRow(b, checked, shiftKey) {
  if (state.bulkBusy) return syncSelection();
  const ids = shiftKey && state.anchor != null ? rangeIds(state.anchor, b.id) : [b.id];
  for (const id of ids) {
    if (checked) state.selected.add(id); else state.selected.delete(id);
  }
  state.anchor = b.id;
  syncSelection();
}

// "All" means the loaded rows, but never more than one request can carry.
const selectable = () => state.bookmarks.slice(0, MAX_BULK_IDS);

function toggleAll() {
  if (state.bulkBusy) return;
  const allChosen = selectable().every((b) => state.selected.has(b.id));
  if (allChosen) state.selected.clear(); else selectable().forEach((b) => state.selected.add(b.id));
  state.anchor = null;
  syncSelection();
}

// Updates the rows in place; rebuilding them would drop the focus from the checkbox the user is on.
function syncSelection() {
  for (const row of $('list').children) {
    if (!row.dataset.id) continue;
    const isChosen = state.selected.has(Number(row.dataset.id));
    row.classList.toggle('chosen', isChosen);
    const box = row.querySelector('.pick-box input');
    if (box) box.checked = isChosen;
  }
  renderBulk();
}

function openBulkPanel(name) {
  if (state.bulkPanel === name) return closeBulkPanel();
  state.bulkPanel = name;
  state.bulkError = '';
  renderBulk();
  $('bulk').querySelector('[data-focus]')?.focus({ preventScroll: true });
}

function closeBulkPanel() {
  const opener = state.bulkPanel;
  state.bulkPanel = null;
  state.bulkError = '';
  renderBulk();
  $('bulk').querySelector(`[data-key="open-${opener}"]`)?.focus({ preventScroll: true });
}

function showBulkError(message) {
  state.bulkError = message;
  renderBulk();
  $('bulk').querySelector('[data-focus]')?.focus({ preventScroll: true });
}

function chosenTagCounts() {
  const counts = new Map();
  for (const b of state.bookmarks) {
    if (state.selected.has(b.id)) for (const t of b.tags) counts.set(t, (counts.get(t) || 0) + 1);
  }
  return counts;
}

function deletePanel(count) {
  return el('div', { class: 'bulk-panel', id: 'bulk-panel' },
    el('span', { class: 'ask' }, `Delete ${bookmarksOf(count)}? This cannot be undone.`),
    state.bulkError ? el('span', { class: 'err', role: 'alert' }, state.bulkError) : null,
    el('button', { type: 'button', class: 'quiet danger', 'data-key': 'confirm-delete', disabled: state.bulkBusy, onclick: once(runBulkDelete) }, `Delete ${count}`),
    el('button', { type: 'button', class: 'quiet', 'data-key': 'keep', 'data-focus': true, onclick: closeBulkPanel }, 'Keep'));
}

function tagsPanel(count, typed) {
  const adding = state.bulkPanel === 'add';
  const suggestions = adding
    ? state.tags.map(({ tag }) => el('option', { value: tag }))
    : [...chosenTagCounts()].map(([tag, n]) => el('option', { value: tag }, `${n} of ${count}`));
  return el('form', { class: 'bulk-panel', id: 'bulk-panel', 'data-panel': state.bulkPanel, novalidate: true, onsubmit: once(runBulkTags) },
    el('input', {
      type: 'text', name: 'tags', value: typed, list: 'bulk-tags', autocomplete: 'off', 'data-key': 'bulk-tags', 'data-focus': true,
      placeholder: 'Tags, separated by commas', 'aria-label': adding ? 'Tags to add' : 'Tags to remove',
    }),
    el('datalist', { id: 'bulk-tags' }, suggestions),
    state.bulkError ? el('span', { class: 'err', role: 'alert' }, state.bulkError) : null,
    el('button', { type: 'submit', disabled: state.bulkBusy }, adding ? `Add to ${bookmarksOf(count)}` : `Remove from ${bookmarksOf(count)}`),
    el('button', { type: 'button', class: 'quiet', onclick: closeBulkPanel }, 'Cancel'));
}

function bulkPanel(count, typed) {
  if (state.checking) return checkingPanel();
  if (state.bulkPanel === 'delete') return deletePanel(count);
  if (state.bulkPanel === 'add' || state.bulkPanel === 'remove') return tagsPanel(count, typed);
  return null;
}

function renderBulk() {
  const toggle = $('select-toggle');
  toggle.textContent = state.selecting ? 'Done' : 'Select';
  toggle.classList.toggle('on', state.selecting);
  const bar = $('bulk');
  bar.hidden = !state.selecting;
  if (!state.selecting) {
    bar.replaceChildren();
    $('bulk-live').textContent = '';
    return;
  }
  const count = state.selected.size;
  // A panel for zero bookmarks would act on nothing.
  if (count === 0) Object.assign(state, { bulkPanel: null, bulkError: '' });
  const focusedKey = bar.contains(document.activeElement) ? document.activeElement.dataset.key : null;
  const old = bar.querySelector('form.bulk-panel');
  const typed = old?.dataset.panel === state.bulkPanel ? old.elements.tags.value : '';
  const allChosen = selectable().length > 0 && selectable().every((b) => state.selected.has(b.id));
  const allLabel = state.bookmarks.length > MAX_BULK_IDS ? `Select first ${MAX_BULK_IDS}` : `Select all ${state.bookmarks.length} shown`;
  const opener = (name, label, extra = '') => el('button', {
    type: 'button', class: `quiet${extra}`, disabled: count === 0 || state.bulkBusy, 'data-key': `open-${name}`,
    'aria-expanded': String(state.bulkPanel === name), 'aria-controls': state.bulkPanel === name ? 'bulk-panel' : null, onclick: () => openBulkPanel(name),
  }, label);
  bar.replaceChildren(...[
    el('div', { class: 'bulk-row' },
      el('span', { class: 'bulk-count' }, count ? `${count} selected` : 'Select bookmarks'),
      el('button', { type: 'button', class: 'quiet', 'data-key': 'select-all', disabled: state.bookmarks.length === 0 || state.bulkBusy, onclick: toggleAll },
        allChosen ? 'Clear selection' : allLabel),
      el('div', { class: 'bulk-actions' },
        opener('add', 'Add tag'), opener('remove', 'Remove tag'),
        el('button', { type: 'button', class: 'quiet', 'data-key': 'check-links', disabled: count === 0 || state.bulkBusy, onclick: once(runLinkCheck) }, 'Check links'),
        opener('delete', 'Delete', ' danger'))),
    bulkPanel(count, typed),
    state.bulkPanel || state.checking ? null : el('p', { class: 'hint' }, 'Shift-click to tick a range. Esc to leave select mode.'),
  ].filter(Boolean));
  $('bulk-live').textContent = count ? `${count} selected` : '';
  if (!focusedKey) return;
  // The button may now be disabled (nothing ticked); then the next best place is "select all".
  const target = bar.querySelector(`[data-key="${focusedKey}"]:not(:disabled)`) ?? bar.querySelector('[data-key="select-all"]');
  target?.focus({ preventScroll: true });
}

// One request, one transaction on the server: it changes every selected bookmark or none.
async function sendBulk(body) {
  const request = { ids: [...state.selected], ...body };
  if (request.ids.length > MAX_BULK_IDS) return showBulkError(`You can change at most ${MAX_BULK_IDS} bookmarks at once. Untick some.`);
  let stale = false;
  // While the request runs the buttons are disabled; a second click would send the same batch again.
  state.bulkBusy = true;
  renderBulk();
  const result = await attempt(() => api('POST', '/bookmarks/bulk', request).catch((error) => {
    if (!(error instanceof ApiError && error.status === 404)) throw error;
    stale = true;
    throw new ApiError(404, 'Some of the selected bookmarks no longer exist. The list was refreshed.');
  }), (message) => { state.bulkBusy = false; showBulkError(message); });
  state.bulkBusy = false;
  if (stale) await loadBookmarks({ mode: 'keep' });
  return result;
}

// The ticks have done their job; staying in select mode would invite a second, accidental action.
async function finishBulk(message) {
  leaveSelectMode();
  renderList();
  $('select-toggle').focus({ preventScroll: true });
  toast(message);
  await refreshAll();
}

async function runBulkDelete() {
  const result = await sendBulk({ action: 'delete' });
  if (result) await finishBulk(`Deleted ${bookmarksOf(result.value.changed)}`);
}

async function runBulkTags(e) {
  const tags = parseTags(new FormData(e.target).get('tags'));
  if (!tags.length) return showBulkError('Type a tag first.');
  if (tags.some((t) => [...t].length > MAX_TAG_CHARS)) return showBulkError(`A tag can have at most ${MAX_TAG_CHARS} characters.`);
  const adding = state.bulkPanel === 'add';
  const result = await sendBulk({ action: adding ? 'add_tags' : 'remove_tags', tags });
  if (!result) return;
  const { changed } = result.value;
  const names = tags.map((t) => `“${t}”`).join(', ');
  if (!adding && changed === 0) return showBulkError('None of the selected bookmarks has that tag.');
  if (adding && changed === 0) return finishBulk(`Every selected bookmark already has ${names}`);
  await finishBulk(adding ? `Added ${names} to ${bookmarksOf(changed)}` : `Removed ${names} from ${bookmarksOf(changed)}`);
}

// Aborting the request makes the server stop; what it already found stays saved.
function stopChecking() {
  state.checking.stop = true;
  state.checking.abort.abort();
}

function checkingPanel() {
  const { done, total, stop } = state.checking;
  return el('div', { class: 'bulk-panel', id: 'bulk-panel' },
    el('span', { class: 'ask' }, `Checking links: ${done} of ${total}`),
    el('button', {
      // Not `disabled`: a disabled button would drop the keyboard focus.
      type: 'button', class: 'quiet', 'data-key': 'stop-check', 'aria-disabled': String(stop),
      onclick: () => { stopChecking(); renderBulk(); },
    }, stop ? 'Stopping' : 'Stop'));
}

function checkSummary(tally, { done, total, stop }) {
  const checked = Object.values(tally).reduce((sum, n) => sum + n, 0);
  const found = [
    tally.broken && `${tally.broken} broken`,
    tally.redirected && `${tally.redirected} redirected`,
    tally.unknown && `${tally.unknown} not confirmed`,
  ].filter(Boolean);
  const stopped = stop && done < total ? `Stopped after ${done} of ${total}. ` : '';
  return `${stopped}Checked ${bookmarksOf(checked)}: ${found.length ? found.join(', ') : 'all fine'}`;
}

// The server sends each result as soon as it has it, so the list fills in one link at a time.
async function runLinkCheck() {
  const ids = state.bookmarks.filter((b) => state.selected.has(b.id)).map((b) => b.id);
  if (!ids.length || state.bulkBusy) return;
  if (ids.length > MAX_BULK_IDS) return showBulkError(`You can check at most ${MAX_BULK_IDS} bookmarks at once. Untick some.`);
  const progress = { done: 0, total: ids.length, stop: false, abort: new AbortController() };
  Object.assign(state, { bulkBusy: true, bulkPanel: null, bulkError: '', checking: progress });
  renderBulk();
  $('bulk').querySelector('[data-key="stop-check"]')?.focus({ preventScroll: true });
  const tally = { ok: 0, redirected: 0, broken: 0, unknown: 0 };
  const onEvent = (name, data) => {
    if (name === 'error') throw new ApiError(500, 'The server had a problem. Try again.');
    if (name !== 'result') return;
    const found = state.bookmarks.find((b) => b.id === data.id);
    if (found) found.link = data.link;
    tally[data.link.status] += 1;
    progress.done += 1;
    renderList();
  };
  let finished;
  try {
    finished = await attempt(() => streamEvents('/bookmarks/check', { ids }, progress.abort.signal, onEvent));
  } finally {
    progress.abort.abort();
    Object.assign(state, { bulkBusy: false, checking: null });
  }
  if (!state.user) return;
  if (!finished) {
    renderBulk();
    return refreshAll();
  }
  // The user left select mode during the check; their new state must not be reset or have focus taken.
  if (progress.left) {
    loadTags();
    renderList();
    return toast(checkSummary(tally, progress));
  }
  await finishBulk(checkSummary(tally, progress));
}

// Esc backs out one step at a time: the open panel first, then select mode.
function escapeSelectMode(e) {
  if (!state.selecting || e.target === $('search')) return;
  if (state.bulkPanel) return closeBulkPanel();
  setSelecting(false);
}

/* ---------- users (admin) ---------- */

const isActive = (u) => !u.is_disabled;
const otherActiveAdmins = (u) => state.users.filter((x) => x.id !== u.id && x.is_admin && isActive(x));

// The server refuses the same changes inside one transaction, which is what counts when two
// admins act at once. This only keeps the buttons honest: it hides what cannot work.
function blockedReason(target, action) {
  const isSelf = target.id === state.user.id;
  if (isSelf && ['demote', 'disable', 'delete'].includes(action)) return 'You cannot change your own access.';
  const removesAdminPower = target.is_admin && isActive(target) && ['demote', 'disable', 'delete'].includes(action);
  if (removesAdminPower && otherActiveAdmins(target).length === 0) return 'The last active admin cannot be changed.';
  return null;
}

function usernameError(name) {
  if (name.length < 3 || name.length > 32) return 'Use 3 to 32 characters for the username.';
  if (!/^[a-z0-9][a-z0-9._-]*[a-z0-9]$/.test(name)) {
    return 'Use only lowercase letters, numbers, dots, underscores, and hyphens. Start and end with a letter or number.';
  }
  return null;
}

const USER_REQUESTS = {
  promote: (u) => api('PUT', `/users/${u.id}/role`, { is_admin: true }),
  demote: (u) => api('PUT', `/users/${u.id}/role`, { is_admin: false }),
  disable: (u) => api('PUT', `/users/${u.id}/disabled`, { is_disabled: true }),
  enable: (u) => api('PUT', `/users/${u.id}/disabled`, { is_disabled: false }),
  delete: (u) => api('DELETE', `/users/${u.id}`),
};
const USER_VERBS = { promote: 'Promoted', demote: 'Demoted', disable: 'Disabled', enable: 'Enabled', delete: 'Deleted' };

async function changeUser(u, action) {
  const reason = blockedReason(u, action);
  if (reason) return toast(reason);
  const result = await attempt(() => USER_REQUESTS[action](u));
  state.confirmingUser = null;
  if (result) toast(`${USER_VERBS[action]} ${u.username}`);
  // Reload even after a refusal: another admin may have changed things first.
  await loadUsers();
}

function resetForm(u) {
  const error = el('p', { class: 'err', role: 'alert', hidden: true });
  const form = el('form', { class: 'user-reset', novalidate: true, onsubmit: once(async (e) => {
    e.preventDefault();
    const password = new FormData(form).get('password');
    if (!password) return showError(error, 'Enter a new password.');
    const result = await attempt(() => api('PUT', `/users/${u.id}/password`, { password }), (message) => showError(error, message));
    if (!result) return;
    state.resettingUser = null;
    toast(`Reset the password of ${u.username}. They are signed out everywhere.`);
    renderUsers();
  }) },
    el('input', { type: 'password', name: 'password', autocomplete: 'new-password', 'aria-label': `New password for ${u.username}`, 'data-focus': true }),
    el('button', { type: 'submit', class: 'quiet' }, 'Save password'),
    el('button', { type: 'button', class: 'quiet', onclick: () => { state.resettingUser = null; renderUsers(); } }, 'Cancel'),
    error,
  );
  return form;
}

function userActions(u) {
  if (state.confirmingUser === u.id) {
    return el('span', { class: 'user-confirm' },
      `Delete ${u.username}?`,
      el('button', { class: 'quiet danger', onclick: once(() => changeUser(u, 'delete')) }, 'Delete'),
      el('button', { class: 'quiet', 'data-focus': true, onclick: () => { state.confirmingUser = null; renderUsers(); } }, 'Keep'),
    );
  }
  const actions = [
    [u.is_admin ? 'demote' : 'promote', u.is_admin ? 'Demote' : 'Promote'],
    [isActive(u) ? 'disable' : 'enable', isActive(u) ? 'Disable' : 'Enable'],
    ['delete', 'Delete'],
  ].filter(([action]) => !blockedReason(u, action));
  const buttons = actions.map(([action, label]) => el('button', {
    class: `quiet${action === 'delete' ? ' danger' : ''}`,
    'aria-label': `${label} ${u.username}`,
    onclick: () => {
      if (action === 'delete') { state.confirmingUser = u.id; renderUsers(); } else changeUser(u, action);
    },
  }, label));
  // Resetting a password ends all of that account's sessions, so it is offered for other people only.
  if (u.id !== state.user.id) {
    buttons.push(el('button', {
      class: 'quiet',
      'aria-label': `Reset password of ${u.username}`,
      onclick: () => { state.resettingUser = u.id; renderUsers(); },
    }, 'Reset password'));
  }
  return el('span', { class: 'user-actions' }, buttons);
}

function renderUsers() {
  $('user-list').replaceChildren(...state.users.map((u) => {
    const labels = [u.is_admin ? 'Admin' : 'Member', isActive(u) ? null : 'disabled', u.id === state.user.id ? 'you' : null];
    return el('div', { class: `user-row${isActive(u) ? '' : ' off'}` },
      el('span', { class: 'name' }, u.username),
      el('span', { class: 'meta' }, labels.filter(Boolean).join(', ')),
      state.resettingUser === u.id ? resetForm(u) : userActions(u),
    );
  }));
  keepFocus($('user-list'));
}

/* ---------- navigation ---------- */

function setMenu(open) {
  const sidebar = $('sidebar');
  const wasOpen = sidebar.classList.contains('open');
  // Keep focus out of the hidden drawer so it does not fall back to the page body.
  if (wasOpen && !open && sidebar.contains(document.activeElement)) $('menu-button').focus();
  document.querySelector('main').inert = open;
  sidebar.classList.toggle('open', open);
  $('backdrop').classList.toggle('open', open);
  document.body.classList.toggle('menu-open', open);
  $('menu-button').setAttribute('aria-expanded', String(open));
  $('menu-button').setAttribute('aria-label', open ? 'Close menu' : 'Open menu');
  if (open && !wasOpen) sidebar.querySelector('a')?.focus();
}

// A tag and a link result are separate lists in the sidebar, so choosing one clears the other.
function setFilter(key) {
  showFilter({ tag: key, link: 'all' });
}

function setLinkFilter(status) {
  showFilter({ tag: 'all', link: status });
}

function showFilter({ tag, link }) {
  closePick();
  setMenu(false);
  state.view = 'list';
  state.tag = tag;
  state.link = link;
  state.editing = null;
  state.confirming = null;
  // A different list is coming; what was ticked will not be on screen.
  clearSelection();
  syncHash({ push: true });
  showView();
  loadBookmarks();
}

function showUsers() {
  setMenu(false);
  leaveSelectMode();
  renderBulk();
  state.view = 'users';
  state.confirmingUser = null;
  state.resettingUser = null;
  syncHash({ push: true });
  showView();
  loadUsers();
}

function showTransfer() {
  setMenu(false);
  leaveSelectMode();
  renderBulk();
  state.view = 'transfer';
  showError($('import-error'), '');
  $('import-result').hidden = true;
  syncHash({ push: true });
  showView();
}

function showAccount() {
  setMenu(false);
  leaveSelectMode();
  renderBulk();
  state.view = 'account';
  $('password-form').reset();
  showError($('password-error'), '');
  syncHash({ push: true });
  showView();
}

function showView() {
  $('bookmarks-view').hidden = state.view !== 'list';
  $('users-view').hidden = state.view !== 'users';
  $('transfer-view').hidden = state.view !== 'transfer';
  $('account-view').hidden = state.view !== 'account';
  // Typed passwords must not wait in a hidden form, or come back with Back.
  if (state.view !== 'account') {
    $('password-form').reset();
    showError($('password-error'), '');
  }
  renderSide();
}

function clearSearch() {
  closePick();
  clearSelection();
  state.query = '';
  $('search').value = '';
  syncHash();
  loadBookmarks();
}

/* ---------- address bar ---------- */

// The view, the filter and the search live in the URL hash, so a reload, the back button and a
// shared link bring them back: `#/users`, `#/transfer`, `#/account`, or `#/?q=rust&tag=web&status=broken`.
function hashForState() {
  if (state.view !== 'list') return `#/${state.view}`;
  const params = new URLSearchParams();
  if (state.query.trim()) params.set('q', state.query.trim());
  if (state.tag !== 'all') params.set('tag', state.tag);
  if (state.link !== 'all') params.set('status', state.link);
  const text = params.toString();
  return text ? `#/?${text}` : '';
}

// A change of view or filter is a new history entry; typing in the search box only replaces the
// current one, so back does not step through every keystroke.
function syncHash({ push = false } = {}) {
  const hash = hashForState();
  if (hash === location.hash) return;
  history[push ? 'pushState' : 'replaceState'](null, '', hash || location.pathname + location.search);
}

// Anything the page does not know (a typo, a tag deleted since, a view only admins have) falls back to the list.
function stateFromHash() {
  const rest = location.hash.replace(/^#\/?/, '');
  const cut = rest.indexOf('?');
  const name = cut === -1 ? rest : rest.slice(0, cut);
  const params = new URLSearchParams(cut === -1 ? '' : rest.slice(cut + 1));
  const link = params.get('status');
  const tag = params.get('tag');
  const view = name === 'transfer' || name === 'account' || (name === 'users' && state.user.is_admin) ? name : 'list';
  return {
    view,
    query: params.get('q') ?? '',
    tag: tag || 'all',
    link: LINK_FILTERS.some(([status]) => status === link) ? link : 'all',
  };
}

// Back, forward and an edited address all end up here.
function restoreFromHash() {
  closePick();
  setMenu(false);
  leaveSelectMode();
  renderBulk();
  Object.assign(state, stateFromHash(), { editing: null, confirming: null, confirmingUser: null, resettingUser: null });
  $('search').value = state.query;
  syncHash();
  showView();
  if (state.view === 'users') loadUsers();
  if (state.view !== 'list') return;
  // The tag list may be old (a tag made in another tab); loadTags drops a tag that is not there.
  loadBookmarks();
  loadTags();
}

/* ---------- actions ---------- */

async function removeBookmark(b) {
  const result = await attempt(() => api('DELETE', `/bookmarks/${b.id}`));
  state.confirming = null;
  if (result) toast(`Deleted “${b.title}”`);
  await refreshAll();
}

function resetForms() {
  for (const id of ['add-form', 'user-form', 'login-form', 'setup-form', 'import-form', 'password-form']) $(id).reset();
  $('search').value = '';
  $('add').open = false;
  for (const id of ['add-error', 'user-error', 'login-error', 'setup-error', 'import-error', 'password-error']) showError($(id), '');
}

function endSession(message) {
  listTicket++;
  closePick();
  setMenu(false);
  Object.assign(state, {
    user: null, view: 'list', tag: 'all', link: 'all', query: '', bookmarks: [], hasMore: false, tags: [], links: [], users: [],
    editing: null, confirming: null, confirmingUser: null, resettingUser: null,
  });
  leaveSelectMode();
  renderBulk();
  resetForms();
  showScreen('login');
  if (message) showError($('login-error'), message);
}

async function signOut() {
  await attempt(() => api('POST', '/logout'));
  endSession('');
  syncHash();
}

// Shows exactly one of: the app, the login form, the first-run setup form.
function showScreen(name) {
  $('app').hidden = name !== 'app';
  $('login').hidden = name !== 'login';
  $('setup').hidden = name !== 'setup';
}

async function enterApp(user) {
  state.user = user;
  Object.assign(state, stateFromHash());
  $('search').value = state.query;
  syncHash();
  showScreen('app');
  showView();
  await refreshAll();
  if (state.view === 'users') loadUsers();
}

async function start() {
  const result = await attempt(() => api('GET', '/state'), (message) => {
    showScreen('login');
    showError($('login-error'), message);
  });
  if (!result) return;
  const { setup_required: setupRequired, user } = result.value;
  if (user) return enterApp(user);
  showScreen(setupRequired ? 'setup' : 'login');
}

/* ---------- wiring ---------- */

$('menu-button').addEventListener('click', () => setMenu($('menu-button').getAttribute('aria-expanded') !== 'true'));
$('backdrop').addEventListener('click', () => setMenu(false));
document.addEventListener('keydown', (e) => {
  // Esc also cancels an IME composition; that must not close the panel.
  if (e.key !== 'Escape' || e.isComposing) return;
  // With the drawer open the page behind it is inert, so Esc only closes the drawer.
  if ($('sidebar').classList.contains('open')) return setMenu(false);
  escapeSelectMode(e);
});
// The drawer exists only in the narrow layout; widening the window must not leave the page inert.
window.matchMedia('(max-width: 760px)').addEventListener('change', (e) => { if (!e.matches) setMenu(false); });
$('search-form').addEventListener('submit', (e) => e.preventDefault());
window.addEventListener('popstate', () => { if (state.user) restoreFromHash(); });

let searchTimer;
$('search').addEventListener('input', (e) => {
  // An input event that changes nothing (an IME commit, for example) must not rebuild the list under a tap.
  const unchanged = e.target.value.trim() === state.query.trim();
  state.query = e.target.value;
  if (unchanged) return;
  // Rebuilding the list would discard unsaved edits, so close any open edit or confirmation first.
  closePick();
  clearSelection();
  Object.assign(state, { editing: null, confirming: null, hasMore: false });
  $('more').hidden = true;
  clearTimeout(searchTimer);
  // Some browsers refuse a page that rewrites the address many times a second, so it waits with the load.
  searchTimer = setTimeout(() => { syncHash(); loadBookmarks(); }, SEARCH_DELAY_MS);
});

$('more').addEventListener('click', () => loadBookmarks({ mode: 'append' }));
$('random').addEventListener('click', once(pickRandom));
$('select-toggle').addEventListener('click', () => setSelecting(!state.selecting));

$('add-form').addEventListener('submit', once(async (e) => {
  e.preventDefault();
  const form = e.target;
  const data = new FormData(form);
  const url = normalizeUrl(data.get('url'));
  if (!url) return showError($('add-error'), 'Enter a full URL, such as https://example.com.');
  const title = data.get('title').trim() || domainOf(url);
  const body = { url, title, note: data.get('note').trim(), tags: parseTags(data.get('tags')) };
  const result = await attempt(() => api('POST', '/bookmarks', body), (message) => showError($('add-error'), message));
  if (!result) return;
  form.reset();
  showError($('add-error'), '');
  $('add').open = false;
  Object.assign(state, { tag: 'all', link: 'all', query: '', editing: null, confirming: null });
  leaveSelectMode();
  $('search').value = '';
  syncHash({ push: true });
  toast(`Saved “${title}”`);
  showView();
  await refreshAll();
}));

$('user-form').addEventListener('submit', once(async (e) => {
  e.preventDefault();
  const form = e.target;
  const data = new FormData(form);
  const name = data.get('name').trim().toLowerCase();
  const password = data.get('password');
  const error = $('user-error');
  if (!name || !password) return showError(error, 'Enter a username and a temporary password.');
  const invalid = usernameError(name);
  if (invalid) return showError(error, invalid);
  const body = { username: name, password, is_admin: data.get('admin') === 'on' };
  const result = await attempt(() => api('POST', '/users', body), (message) => showError(error, message));
  if (!result) return;
  form.reset();
  showError(error, '');
  toast(`Created user ${name}`);
  await loadUsers();
}));

function showImportResult({ added, duplicates, invalid, invalid_examples: examples }) {
  const box = $('import-result');
  const lines = [el('p', {}, el('strong', {}, `Added ${added} bookmark${added === 1 ? '' : 's'}.`))];
  if (duplicates) lines.push(el('p', {}, `Skipped ${duplicates} you already had.`));
  if (invalid) {
    lines.push(el('p', {}, `Skipped ${invalid} that are not web links:`));
    lines.push(el('ul', {}, examples.map((item) => el('li', {}, item.url || '(no address)', ` — ${item.reason}`))));
    if (invalid > examples.length) lines.push(el('p', { class: 'summary' }, `and ${invalid - examples.length} more.`));
  }
  box.replaceChildren(...lines);
  box.hidden = false;
}

$('import-form').addEventListener('submit', once(async (e) => {
  e.preventDefault();
  const error = $('import-error');
  const file = $('import-file').files[0];
  $('import-result').hidden = true;
  if (!file) return showError(error, 'Choose a bookmarks file first.');
  showError(error, '');
  const result = await attempt(() => api('POST', '/import', file), (message) => showError(error, message));
  if (!result) return;
  e.target.reset();
  showImportResult(result.value);
  await refreshAll();
}));

$('password-form').addEventListener('submit', once(async (e) => {
  e.preventDefault();
  const error = $('password-error');
  const data = new FormData(e.target);
  const body = { current: data.get('current'), new: data.get('new') };
  if (!body.current || !body.new) return showError(error, 'Enter your current and a new password.');
  if (body.new !== data.get('again')) return showError(error, 'The new passwords do not match.');
  showError(error, '');
  const result = await attempt(() => api('PUT', '/account/password', body), (message) => showError(error, message));
  if (!result) return;
  e.target.reset();
  toast('Password changed. You are signed out everywhere else.');
}));

$('login-form').addEventListener('submit', once(async (e) => {
  e.preventDefault();
  const data = new FormData(e.target);
  const body = { username: data.get('name').trim(), password: data.get('password') };
  const result = await attempt(() => api('POST', '/login', body), (message) => showError($('login-error'), message));
  if (!result) return;
  e.target.reset();
  showError($('login-error'), '');
  await start();
}));

$('setup-form').addEventListener('submit', once(async (e) => {
  e.preventDefault();
  const data = new FormData(e.target);
  const body = { token: data.get('token').trim(), username: data.get('name').trim(), password: data.get('password') };
  const result = await attempt(() => api('POST', '/setup', body), (message) => showError($('setup-error'), message));
  if (!result) return;
  e.target.reset();
  showError($('setup-error'), '');
  await start();
}));

start();
