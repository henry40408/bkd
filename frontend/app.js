'use strict';

const PAGE_SIZE = 50;
const SEARCH_DELAY_MS = 250;
// The server returns at most 200 items; one is asked for beyond the page to learn whether more exist.
const MAX_RELOAD = 199;

const state = {
  user: null,
  view: 'list',
  tag: 'all',
  query: '',
  bookmarks: [],
  hasMore: false,
  tags: [],
  users: [],
  editing: null,
  confirming: null,
  confirmingUser: null,
  resettingUser: null,
};

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
const SEARCH_FIELDS = ['title', 'url', 'note', 'tag'];
const isSpace = (character) => /^\p{White_Space}$/u.test(character);

// Reads `title:`-style prefixes, quotes and `-` exclusions exactly as src/search.rs does;
// tests/search_syntax.json holds the cases both parsers must agree on.
// Returns [{ field: 'any' | 'title' | 'url' | 'note' | 'tag', text, negated }].
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
  let fallback = 'The request was refused.';
  if (response.status >= 500) fallback = 'The server had a problem. Try again.';
  if (response.status === 413) fallback = 'That file is too large. The limit is 20 MB.';
  throw new ApiError(response.status, data?.error ?? fallback, response.headers.get('Retry-After'));
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
  const result = await attempt(() => api('GET', `/bookmarks?${params}`));
  // A newer request owns the list now; showing this answer would put old results on screen.
  if (!result || ticket !== listTicket || !state.user) return;
  // A refresh must not rebuild the list under an open edit or confirmation; a new filter replaces both anyway.
  if (mode !== 'first' && (state.editing || state.confirming)) return;
  const page = result.value.bookmarks.slice(0, wanted);
  state.bookmarks = append ? state.bookmarks.concat(page) : page;
  state.hasMore = result.value.bookmarks.length > wanted;
  renderList();
}

async function loadTags() {
  if (!state.user) return;
  const result = await attempt(() => api('GET', '/tags'));
  if (!result || !state.user) return;
  state.tags = result.value.tags;
  if (state.tag !== 'all' && !state.tags.some((t) => t.tag === state.tag)) {
    state.tag = 'all';
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
  await Promise.all([loadBookmarks({ mode: 'keep' }), loadTags()]);
}

/* ---------- sidebar ---------- */

function filterLink(label, count, key) {
  const current = state.view === 'list' && state.tag === key;
  return el('li', {}, el('a', {
    href: '#',
    'aria-current': current ? 'page' : null,
    onclick: (e) => { e.preventDefault(); setFilter(key); },
  }, label, count == null ? null : el('span', { class: 'count' }, String(count))));
}

function renderSide() {
  $('filters').replaceChildren(filterLink('All bookmarks', null, 'all'));
  $('tag-list').replaceChildren(...state.tags.map(({ tag, count }) => filterLink(tag, count, tag)));

  const manageUsersLink = el('a', { href: '#', onclick: (e) => { e.preventDefault(); showUsers(); } }, 'Manage users');
  const transferLink = el('a', { href: '#', onclick: (e) => { e.preventDefault(); showTransfer(); } }, 'Import and export');
  $('me').replaceChildren(...[
    el('span', {}, 'Signed in as ', el('strong', {}, state.user.username)),
    transferLink,
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

function itemView(b) {
  if (state.editing === b.id) return editView(b);
  const terms = parseQuery(state.query.trim());
  // A `tag:` term matches a whole tag, so only that tag is highlighted.
  const tagTerms = (tag) => [
    ...termsFor(terms, 'any'),
    ...terms.filter((term) => !term.negated && term.field === 'tag' && term.text.trim().toLowerCase() === tag).map((term) => term.text.trim()),
  ];
  return el('li', { class: 'item' },
    el('span', { class: 'domain' }, highlight(domainOf(b.url), termsFor(terms, 'url'))),
    el('div', {},
      el('a', { class: 'title', href: normalizeUrl(b.url) || '#', target: '_blank', rel: 'noopener noreferrer' }, highlight(b.title, termsFor(terms, 'title'))),
      b.note ? el('p', { class: 'note' }, highlight(b.note, termsFor(terms, 'note'))) : null,
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
  if (query && state.tag !== 'all') return [`No bookmarks tagged “${state.tag}” match “${query}”. `, el('button', { class: 'link', onclick: clearSearch }, 'Clear the search'), '.'];
  if (query) return [`No bookmarks match “${query}”. `, el('button', { class: 'link', onclick: clearSearch }, 'Clear the search'), ' or add a new bookmark.'];
  if (state.tag !== 'all') return [`No bookmarks tagged “${state.tag}”.`];
  return ['No bookmarks here yet. Add one with the form above.'];
}

function renderList() {
  $('list').replaceChildren(...state.bookmarks.map(itemView));
  $('empty').replaceChildren(...emptyMessage());
  $('empty').hidden = state.bookmarks.length > 0;
  $('more').hidden = !state.hasMore;
  const count = state.bookmarks.length;
  $('summary').textContent = state.hasMore ? `Showing the newest ${count}` : `${count} ${count === 1 ? 'bookmark' : 'bookmarks'}`;
  // Focus after the next paint so the form shows up first; the item the user clicked is already in view.
  requestAnimationFrame(() => keepFocus($('list')));
}

// After a rebuild the control the user was on is gone. Focus the form or confirmation that just
// opened, or else the page's main area, so keyboard users do not restart from the top of the page.
function keepFocus(container) {
  container.querySelector('[data-focus]')?.focus({ preventScroll: true });
  if (!document.activeElement || document.activeElement === document.body) {
    $('main').focus({ preventScroll: true });
  }
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

function setFilter(key) {
  setMenu(false);
  state.view = 'list';
  state.tag = key;
  state.editing = null;
  state.confirming = null;
  showView();
  loadBookmarks();
}

function showUsers() {
  setMenu(false);
  state.view = 'users';
  state.confirmingUser = null;
  state.resettingUser = null;
  showView();
  loadUsers();
}

function showTransfer() {
  setMenu(false);
  state.view = 'transfer';
  showError($('import-error'), '');
  $('import-result').hidden = true;
  showView();
}

function showView() {
  $('bookmarks-view').hidden = state.view !== 'list';
  $('users-view').hidden = state.view !== 'users';
  $('transfer-view').hidden = state.view !== 'transfer';
  renderSide();
}

function clearSearch() {
  state.query = '';
  $('search').value = '';
  loadBookmarks();
}

/* ---------- actions ---------- */

async function removeBookmark(b) {
  const result = await attempt(() => api('DELETE', `/bookmarks/${b.id}`));
  state.confirming = null;
  if (result) toast(`Deleted “${b.title}”`);
  await refreshAll();
}

function resetForms() {
  for (const id of ['add-form', 'user-form', 'login-form', 'setup-form', 'import-form']) $(id).reset();
  $('search').value = '';
  $('add').open = false;
  for (const id of ['add-error', 'user-error', 'login-error', 'setup-error', 'import-error']) showError($(id), '');
}

function endSession(message) {
  listTicket++;
  setMenu(false);
  Object.assign(state, {
    user: null, view: 'list', tag: 'all', query: '', bookmarks: [], hasMore: false, tags: [], users: [],
    editing: null, confirming: null, confirmingUser: null, resettingUser: null,
  });
  resetForms();
  showScreen('login');
  if (message) showError($('login-error'), message);
}

async function signOut() {
  await attempt(() => api('POST', '/logout'));
  endSession('');
}

// Shows exactly one of: the app, the login form, the first-run setup form.
function showScreen(name) {
  $('app').hidden = name !== 'app';
  $('login').hidden = name !== 'login';
  $('setup').hidden = name !== 'setup';
}

async function enterApp(user) {
  state.user = user;
  state.view = 'list';
  showScreen('app');
  showView();
  await refreshAll();
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
document.addEventListener('keydown', (e) => { if (e.key === 'Escape') setMenu(false); });
// The drawer exists only in the narrow layout; widening the window must not leave the page inert.
window.matchMedia('(max-width: 760px)').addEventListener('change', (e) => { if (!e.matches) setMenu(false); });
$('search-form').addEventListener('submit', (e) => e.preventDefault());

let searchTimer;
$('search').addEventListener('input', (e) => {
  // An input event that changes nothing (an IME commit, for example) must not rebuild the list under a tap.
  const unchanged = e.target.value.trim() === state.query.trim();
  state.query = e.target.value;
  if (unchanged) return;
  // Rebuilding the list would discard unsaved edits, so close any open edit or confirmation first.
  Object.assign(state, { editing: null, confirming: null, hasMore: false });
  $('more').hidden = true;
  clearTimeout(searchTimer);
  searchTimer = setTimeout(loadBookmarks, SEARCH_DELAY_MS);
});

$('more').addEventListener('click', () => loadBookmarks({ mode: 'append' }));

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
  Object.assign(state, { tag: 'all', query: '', editing: null, confirming: null });
  $('search').value = '';
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
