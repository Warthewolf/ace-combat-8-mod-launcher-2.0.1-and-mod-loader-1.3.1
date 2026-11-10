'use strict';
// ACE8 Mod Launcher window.

const $ = s => document.querySelector(s);
const $$ = s => Array.from(document.querySelectorAll(s));
const TOKEN = new URLSearchParams(location.search).get('t') || '';
const CLIENT = 'w' + Math.random().toString(36).slice(2, 10) + Date.now().toString(36);
let STATE = null;
let STATUS = null;
let busyLocal = false;   // a switch or setup started from this window is running
let setupRunning = false;
let setupDone = Promise.resolve(); // settles when the setup started from this window is done
let playing = false;     // a Play request is on its way
let switchNote = null;   // {text, mode}: why the last switch failed; kept until the next switch or a mode change

// ---------------------------------------------------------------- server
async function api(path, body) {
  const opt = { method: body === undefined ? 'GET' : 'POST', headers: { 'X-Token': TOKEN, 'X-Client': CLIENT } };
  if (body !== undefined) { opt.headers['Content-Type'] = 'application/json'; opt.body = JSON.stringify(body); }
  const r = await fetch('/api/' + path, opt);
  if (!r.ok) throw new Error('The launcher did not answer (' + r.status + '). Close this window and start the launcher again.');
  return r.json();
}

// mutating call: shows the error, refreshes the state
let lastError = '';
async function act(path, body, opts = {}) {
  // a load order change waiting to be saved goes first, so this action's answer can't undo it
  if (path !== 'order' && orderUnsaved()) await flushOrder();
  try {
    const r = await api(path, body || {});
    if (r.state) { STATE = r.state; STATUS = r.state.status; render(); }
    if (!r.ok) {
      lastError = r.error || 'That did not work.';
      if (!r.state) loadState(); // show what the launcher has now
      if (!opts.quiet) toast(lastError, 'bad');
      return null;
    }
    return r.result === undefined || r.result === null ? true : r.result;
  } catch (e) { lastError = e.message; toast(e.message, 'bad'); return null; }
}

async function loadState() {
  try { STATE = await api('state'); STATUS = STATE.status; } catch (e) { return false; /* the next poll tries again */ }
  render();
  return true;
}

// Show and Open buttons: say so when nothing could be opened
async function reveal(body) {
  try {
    const r = await api('reveal', body);
    if (!r.ok) toast(r.error || 'That folder could not be opened.', 'bad');
  } catch (e) { toast(e.message, 'bad'); }
}
async function openLink(url) {
  try {
    const r = await api('open', { url });
    if (!r.ok) toast(r.error || 'The link could not be opened.', 'bad');
  } catch (e) { toast(e.message, 'bad'); }
}

// A button whose action is running looks switched off (with a label saying so) until it is done;
// pressing it again meanwhile does nothing. It keeps keyboard focus (aria-disabled, not disabled).
// A list rebuilt meanwhile marks the button's new copy too (markBusy); both are cleared at the end.
const inFlight = new Set();
async function withBusy(btn, key, label, fn) {
  if (inFlight.has(key)) return undefined;
  inFlight.add(key);
  if (btn) markBusy(btn, key, label);
  try { return await fn(); } finally {
    inFlight.delete(key);
    for (const b of new Set([btn, ...$$('[data-busy-key]').filter(x => x.dataset.busyKey === key)])) if (b) unmarkBusy(b);
  }
}
function markBusy(btn, key, label) {
  if (!inFlight.has(key)) return;
  btn.dataset.busyKey = key;
  if (label && btn.dataset.busyText === undefined) btn.dataset.busyText = btn.textContent;
  btn.setAttribute('aria-disabled', 'true');
  btn.classList.add('busy');
  if (label) btn.textContent = label;
}
function unmarkBusy(btn) {
  if (btn.dataset.busyKey === undefined) return;
  btn.removeAttribute('aria-disabled');
  btn.classList.remove('busy');
  if (btn.dataset.busyText !== undefined) btn.textContent = btn.dataset.busyText;
  delete btn.dataset.busyKey;
  delete btn.dataset.busyText;
}

// ---------------------------------------------------------------- helpers
const esc = s => String(s ?? '').replace(/[&<>"']/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
function fmtSize(n) {
  if (!n) return '0 KB';
  if (n < 1024 * 1024) return Math.max(1, Math.round(n / 1024)) + ' KB';
  if (n < 1024 * 1024 * 1024) return (n / 1048576).toFixed(n < 10 * 1048576 ? 1 : 0) + ' MB';
  return (n / 1073741824).toFixed(1) + ' GB';
}
function fmtWhen(s) {
  const d = new Date(s); if (isNaN(d) || d.getFullYear() < 2000) return '';
  const now = new Date();
  const t = d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
  if (d.toDateString() === now.toDateString()) return 'today at ' + t;
  const y = new Date(now); y.setDate(now.getDate() - 1);
  if (d.toDateString() === y.toDateString()) return 'yesterday at ' + t;
  return d.toLocaleDateString([], { day: 'numeric', month: 'short' }) + ' at ' + t;
}
// "today", "yesterday", "6 Nov", "6 Nov 2025"
function fmtDay(s) {
  const d = new Date(s); if (isNaN(d) || d.getFullYear() < 2000) return '';
  const now = new Date();
  if (d.toDateString() === now.toDateString()) return 'today';
  const y = new Date(now); y.setDate(now.getDate() - 1);
  if (d.toDateString() === y.toDateString()) return 'yesterday';
  return d.toLocaleDateString(undefined, d.getFullYear() === now.getFullYear() ? { day: 'numeric', month: 'short' } : { day: 'numeric', month: 'short', year: 'numeric' });
}
function el(html) { const t = document.createElement('template'); t.innerHTML = html.trim(); return t.content.firstElementChild; }
function plural(n, one, many) { return n + ' ' + (n === 1 ? one : (many || one + 's')); }
function lsGet(k) { try { return localStorage.getItem(k); } catch (e) { return null; } }
function lsSet(k, v) { try { localStorage.setItem(k, v); } catch (e) {} }
const baseName = p => String(p || '').split(/[\\/]/).filter(Boolean).pop() || String(p || '');
const shortKey = k => String(k).replace(/^~mods\\/, '');
// when a mod was last put in or changed: the launcher's own dates first, then the file time
const changedAt = m => m.updated || m.added || m.mtime || 0;
const changedAfter = (m, when) => new Date(changedAt(m)) > new Date(when);
const gameRunningNow = () => { const st = STATUS || (STATE && STATE.status); return !!(st && st.gameRunning); };
const GAME_RUNNING = 'The game is running. Close it to add or remove mods.';
const fmWord = () => isLinux() ? 'a folder window' : 'Explorer';

// ---------------------------------------------------------------- toasts & dialogs
function toast(text, kind = 'ok', opts = {}) {
  if (opts.id) for (const old of $$('#toasts .toast')) if (old.dataset.id === opts.id) old.remove();
  const t = el(`<div class="toast ${kind}" role="${kind === 'bad' ? 'alert' : 'status'}"><div class="toast-text">${esc(text)}</div></div>`);
  if (opts.id) t.dataset.id = opts.id;
  if (opts.list && opts.list.length) t.appendChild(el(`<ul>${opts.list.map(x => `<li>${esc(x)}</li>`).join('')}</ul>`));
  if (opts.action) {
    const b = el(`<button class="link toast-act" type="button">${esc(opts.action.label)}</button>`);
    b.onclick = () => { t.remove(); opts.action.fn(); };
    t.appendChild(b);
  }
  const x = el('<button class="toast-x" type="button" aria-label="Close this message" title="Close">×</button>');
  x.onclick = () => t.remove();
  t.appendChild(x);
  const box = $('#toasts');
  box.appendChild(t);
  // keep the pile short: the oldest messages go first
  const all = $$('#toasts .toast:not(.progress)');
  for (let i = 0; i < all.length - 5; i++) all[i].remove();
  // problems stay until closed; the rest go by themselves, but not while the pointer is on them
  if (kind !== 'bad' && !opts.sticky) {
    const ms = (opts.action ? 10000 : 6000) + (opts.list ? opts.list.length * 1500 : 0);
    let timer = setTimeout(() => t.remove(), ms);
    t.addEventListener('mouseenter', () => clearTimeout(timer));
    t.addEventListener('focusin', () => clearTimeout(timer));
    t.addEventListener('mouseleave', () => { clearTimeout(timer); timer = setTimeout(() => t.remove(), 3000); });
  }
  return t;
}
function closeToast(id) { for (const t of $$('#toasts .toast')) if (t.dataset.id === id) t.remove(); }

// something that takes a moment: "Adding NewSkin.zip…"
function toastProgress(text, opts = {}) {
  const t = el(`<div class="toast progress" role="status"><div class="toast-text"><span class="dot" aria-hidden="true"></span><span class="pt">${esc(text)}</span></div></div>`);
  if (opts.cancel) {
    const b = el('<button class="link toast-act" type="button">Cancel</button>');
    b.onclick = opts.cancel;
    t.appendChild(b);
  }
  t.setText = s => { t.querySelector('.pt').textContent = s; };
  $('#toasts').appendChild(t);
  return t;
}

// One dialog at a time: a second one waits until the first is closed.
// onOpen runs once the dialog shows, with its body.
let dlgQueue = Promise.resolve();
function dialog({ title, html, buttons, wide, onOpen }) {
  const show = () => new Promise(resolve => {
    const d = $('#dlg');
    d.classList.toggle('wide', !!wide);
    $('#dlgTitle').textContent = title;
    $('#dlgBody').innerHTML = html;
    const bar = $('#dlgButtons'); bar.innerHTML = '';
    for (const b of buttons) {
      const btn = el(`<button class="btn ${b.kind || ''}" value="${esc(b.value)}">${esc(b.label)}</button>`);
      bar.appendChild(btn);
    }
    d.onclose = () => { d.onclose = null; resolve(d.returnValue || 'cancel'); };
    d.returnValue = '';
    d.showModal();
    const first = bar.querySelector('.primary') || bar.querySelector('button');
    if (first) first.focus();
    if (onOpen) onOpen($('#dlgBody'));
  });
  const p = dlgQueue.then(show);
  dlgQueue = p.catch(() => {});
  return p;
}
// Enter in a text box or on a choice means the main button, not the first one in the form (Cancel)
$('#dlgForm').addEventListener('keydown', e => {
  if (e.key === 'Enter' && e.target.tagName === 'INPUT') {
    e.preventDefault();
    const p = $('#dlgButtons .primary');
    if (p) p.click();
  }
});

// a failure nobody expected still shows, instead of nothing happening
window.addEventListener('unhandledrejection', e => {
  const m = e.reason && e.reason.message ? e.reason.message : String(e.reason || '');
  toast('Something went wrong in this window: ' + m + ' Try again, or copy a support report in Settings.', 'bad', { id: 'unexpected' });
});

// ---------------------------------------------------------------- navigation & theme
function go(page) {
  for (const b of $$('.nav button')) { if (b.dataset.page === page) b.setAttribute('aria-current', 'page'); else b.removeAttribute('aria-current'); }
  for (const p of $$('.page')) p.hidden = p.id !== 'page-' + page;
  $('#scroll').scrollTop = 0;
  lsSet('ace8ml-page', page);
  if (page !== 'mods' && newKeys) { newKeys = null; if (STATE) renderMods(); }
}
for (const b of $$('.nav button')) b.onclick = () => go(b.dataset.page);

function applyTheme(t) {
  if (t === 'light' || t === 'dark') document.documentElement.dataset.theme = t; else delete document.documentElement.dataset.theme;
  for (const b of $$('.seg button')) b.setAttribute('aria-pressed', String(b.dataset.theme === (t || 'system')));
  lsSet('ace8-theme', t === 'system' ? '' : t);
}
for (const b of $$('.seg button')) b.onclick = () => applyTheme(b.dataset.theme);
applyTheme(lsGet('ace8-theme') || 'system');

// ---------------------------------------------------------------- focus across re-renders
// Lists are rebuilt on every change. The control that had focus gets it back in its new copy,
// without scrolling the page.
function rememberFocus() {
  const a = document.activeElement;
  if (!a || a === document.body || !a.closest) return null;
  const li = a.closest('.list > li.item');
  if (li && li.parentElement.id) return { list: li.parentElement.id, key: li.dataset.key, ctl: a === li ? '' : (a.dataset.ctl || '') };
  if (a.dataset.fid) return { fid: a.dataset.fid };
  if (a.id) return { id: a.id };
  return null;
}
function restoreFocus(f, scroll) {
  if (!f) return;
  const a = document.activeElement;
  if (a && a !== document.body && a.isConnected) return; // it kept its focus
  let t = null;
  if (f.list) {
    const li = $$('#' + f.list + ' > li.item').find(x => x.dataset.key === f.key);
    if (li) { t = f.ctl ? li.querySelector(`[data-ctl="${f.ctl}"]`) : li; if (!t || t.disabled) t = li; }
  } else if (f.fid) t = $$('[data-fid]').find(x => x.dataset.fid === f.fid);
  else if (f.id) t = document.getElementById(f.id);
  if (!t || t.disabled || t.closest('[hidden]')) return;
  t.focus({ preventScroll: true });
  if (scroll) t.scrollIntoView({ block: 'nearest' });
}

// ---------------------------------------------------------------- render
const isLinux = () => !!STATE && STATE.platform === 'linux';
let osApplied = '';
function applyOS() {
  const os = (STATE && STATE.platform) || 'windows';
  if (os === osApplied) return;
  osApplied = os;
  document.documentElement.dataset.os = os;
  if (os === 'linux') {
    for (const b of $$('[data-fm]')) b.textContent = 'Open folder';
    // Windows paths get forward slashes; home-folder paths (~/...) are Linux paths already
    for (const p of $$('#page-help .path')) if (!p.textContent.startsWith('~')) p.textContent = p.textContent.replace(/\\/g, '/');
    // on Linux, how the game is started is a step everyone needs, not an advanced detail
    $('#startBlock').open = true;
  }
}

function render() {
  if (!STATE) return;
  const f = rememberFocus();
  applyOS();
  mergePendingOrder();
  $('#contact').textContent = STATE.contact;
  $('#ver').textContent = 'Version ' + STATE.version;
  renderHud();
  renderPlay();
  renderMods();
  renderSettings();
  $('#btnTourRail').hidden = !STATE.game.found;
  restoreFocus(f);
  tourMaybeStart();
}

function currentMode() { return STATE.mode.mode; }
function modsOnCount() {
  return STATE.pakMods.filter(m => m.enabled).length + STATE.scriptMods.filter(m => m.enabled).length;
}

function renderHud() {
  const s = STATE, st = STATUS || s.status, cells = [];
  const cell = (k, v, cls, go, title) => cells.push(`<button class="cell" ${go ? `data-go="${go}" data-fid="hud:${k}"` : 'tabindex="-1"'} ${title ? `title="${esc(title)}"` : ''}><span class="k">${k}</span><span class="v ${cls || ''}">${esc(v)}</span></button>`);
  if (!s.game.found) { cell('Game', 'Not found', 'bad', 'play'); $('#hud').innerHTML = cells.join(''); wireHud(); return; }
  cell('Game', st.gameRunning ? 'Running' : 'Ready', st.gameRunning ? 'good' : '', 'settings', s.game.root);
  const m = currentMode();
  const modeText = { modded: ['Modded', 'hot'], unmodded: ['Unmodded', 'blue'], mixed: ['Half switched', 'bad'], none: ['No mods installed', ''] }[m];
  cell('Mode', modeText[0], modeText[1], 'play');
  const su = s.setup, l = su.loader;
  const ver = l && l !== 'unknown' ? 'Mod Loader ' + l : 'The Mod Loader';
  if (!l) cell('Mod Loader', 'Not set up', 'bad', 'play', 'Modding isn\'t set up yet. The Play page shows how.');
  else if (m === 'unmodded') cell('Mod Loader', 'Switched off', 'blue', 'settings', ver + ' waits in the mod storage folder while Unmodded.');
  else if (!su.loaderOn) cell('Mod Loader', 'Off', 'warn', 'settings', ver + ' is switched off, so no pak mods load.');
  else if (su.canUpdate) cell('Mod Loader', 'Update available', 'warn', 'settings', ver + ' is installed; ' + su.bundled + ' is available.');
  else cell('Mod Loader', 'Ready', '', 'settings', ver);
  const all = s.pakMods.length + s.scriptMods.length;
  const pk = s.pakMods.filter(x => x.enabled).length, sc = s.scriptMods.filter(x => x.enabled).length;
  if (m === 'unmodded') cell('Mods', 'Switched off', 'blue', 'mods', all ? plural(all, 'mod is', 'mods are') + ' waiting in the mod storage folder.' : 'No mods yet.');
  else if (!all) cell('Mods', 'None yet', '', 'mods', 'No mods added yet.');
  else cell('Mods', (pk + sc) + ' on', '', 'mods', plural(pk, 'mod') + (sc ? ' + ' + plural(sc, 'script mod') : '') + ' on' + (all > pk + sc ? ', ' + (all - pk - sc) + ' off.' : '.'));
  if (s.run && m === 'modded') {
    const r = s.run;
    const short = { ok: [r.mounted + ' of ' + r.total + ' loaded', 'good'], problems: [r.mounted + ' of ' + r.total + ' loaded', 'warn'], unsupported: ['Game version not supported', 'bad'], otherloader: ['Two mod loaders', 'bad'], nothing: [s.scriptMods.some(x => x.enabled) ? 'No pak mods on' : 'No mods on', ''], waiting: ['Did not finish', 'warn'] }[r.state] || ['', ''];
    cell('Last game start', short[0], short[1], 'last', fmtWhen(r.when));
  }
  $('#hud').innerHTML = cells.join('');
  wireHud();
}
function wireHud() {
  for (const c of $$('#hud .cell[data-go]')) c.onclick = () => {
    if (c.dataset.go === 'last') { go('play'); $('#lastRun').scrollIntoView({ behavior: 'smooth' }); } else go(c.dataset.go);
  };
}

// ---------------------------------------------------------------- play page
// a Play was pressed a moment ago and the game hasn't shown up yet
function launchStarting() {
  const st = STATUS || STATE.status, L = st.launch;
  return !!(L && !L.sawGame && !L.gameEnded && !L.gaveUp && !st.gameRunning && Date.now() - new Date(L.started) < 90000);
}

// The game folder the launcher knew can't be found (a drive that isn't
// connected): say so, and offer a copy of the game found somewhere else.
function renderFindGame() {
  const g = STATE.game, miss = $('#findGameMissing'), use = $('#btnUseElsewhere');
  $('#findGameTitle').textContent = g.missing ? 'Game folder not found' : 'Game not found';
  miss.hidden = !g.missing;
  miss.innerHTML = g.missing ? `<span class="path">${esc(g.missing)}</span> can't be found. If it is on a drive that isn't connected, connect it and the launcher finds it again. Or choose the game folder:` : '';
  use.hidden = !g.elsewhere;
  use.textContent = g.elsewhere ? 'Use the copy at ' + g.elsewhere : '';
  use.title = g.elsewhere || '';
}

function renderPlay() {
  const s = STATE, st = STATUS || s.status;
  $('#findGame').hidden = s.game.found;
  $('#playBody').hidden = !s.game.found;
  if (!s.game.found) { renderFindGame(); return; }

  const m = currentMode();
  const sw = $('#modeSwitch');
  sw.classList.toggle('mixed', m === 'mixed');
  $('#optModded').setAttribute('aria-checked', String(m === 'modded'));
  $('#optUnmodded').setAttribute('aria-checked', String(m === 'unmodded' || m === 'none'));
  const n = modsOnCount(), total = s.pakMods.length + s.scriptMods.length;
  const desc = $('#descModded');
  desc.textContent = m === 'none' ? 'Press to set up modding' : !n ? 'No mods on yet' : n === total ? 'Your ' + plural(n, 'mod') : `${n} of your ${plural(total, 'mod')} on`;
  desc.classList.toggle('action', m === 'none');
  if (switchNote && switchNote.mode !== m) switchNote = null;
  const note = $('#modeNote');
  if (!busyLocal) {
    note.classList.remove('bad');
    if (switchNote) { note.textContent = switchNote.text; note.classList.add('bad'); }
    else if (st.gameRunning) note.textContent = 'Close the game to switch.';
    else if (m === 'mixed') { note.textContent = 'The last switch didn\'t finish. Pick Modded or Unmodded to finish it.'; note.classList.add('bad'); }
    else if (m === 'unmodded') note.textContent = 'Mods are switched off and kept in the mod storage folder.';
    else note.textContent = '';
  }
  renderPlayBar(true);
  renderTodo();
  renderLastRun();
  tourPlaceSoon();
}

// The launch bar and the line under it. The First steps list copies the locks of Play and the mode
// switch: withSteps draws it too; otherwise it is redrawn only when those locks changed (a start
// that is still pending unlocks them after a while, with no news from the launcher).
let fsLock = '';
function renderPlayBar(withSteps) {
  if (!STATE || !STATE.game.found) return;
  const st = STATUS || STATE.status, m = currentMode();
  const play = $('#btnPlay');
  const modded = m === 'modded', starting = launchStarting();
  play.classList.toggle('unmodded', !modded);
  // before modding is set up, the setup card is the first thing to do; Play stays, but quieter
  play.classList.toggle('quiet', m === 'none');
  $('#playLabel').textContent = st.gameRunning ? 'Game running' : playing || starting ? 'Starting…' : m === 'mixed' ? 'Play' : modded ? 'Play modded' : 'Play unmodded';
  play.disabled = !!(st.gameRunning || st.busy || busyLocal || m === 'mixed' || playing || starting);
  for (const b of $$('.mode-opt')) b.setAttribute('aria-disabled', String(!!(st.gameRunning || busyLocal || st.busy || playing || starting)));
  $('#playHow').hidden = m === 'mixed';
  $('#playHow').textContent = modded ? (isLinux() ? 'Starts through Steam with anti-cheat off. Offline and single player only.' : 'Anti-cheat off. Offline and single player only.') : 'Starts through Steam with anti-cheat on.';
  renderLive();
  const lock = play.disabled + '|' + $('#optModded').getAttribute('aria-disabled');
  if (withSteps || lock !== fsLock) {
    fsLock = lock;
    const f = withSteps ? null : rememberFocus();
    renderFirstSteps();
    restoreFocus(f);
  }
}

function renderLive() {
  if (playing) return; // the Play button's own messages show until the launcher answers
  const s = STATE, st = STATUS || s.status;
  const L = st.launch;
  if (st.steamStartMissed) {
    setLive('<span class="bad-text">The game started without the launcher\'s line in Steam\'s launch options, so anti-cheat is on and the mods won\'t load. Close it and see Settings, Starting the game.</span>');
  } else if (st.gameRunning) {
    let t = '<span class="dot"></span>Game running';
    if (L && st.logFresh && s.run && L.mode === 'modded') t += ': ' + esc(s.run.summary);
    setLive(t);
  } else if (launchStarting()) {
    setLive(L.how === 'steam' ? 'Steam is starting the game…' : 'Starting the game…');
  } else if (L && L.gameEnded) {
    setLive('Game closed.');
  } else if (L && !L.sawGame && L.gaveUp) {
    setLive(L.how === 'steam' ? 'The game didn&#39;t start. Check Steam for a message.' : 'The game didn&#39;t start. Press Play to try again.');
  } else if (L && !L.sawGame) {
    setLive(L.how === 'steam' ? 'The game hasn&#39;t started yet. Check Steam for a message.' : 'The game hasn&#39;t started yet. If nothing happens, press Play again.');
  } else setLive('');
}
// The line under Play is read out when it changes, so it is only replaced when it says something new.
function setLive(html) {
  const live = $('#playLive');
  if (live.dataset.html === html) return;
  live.dataset.html = html;
  live.innerHTML = html;
}

function card(kind, title, html, buttons = []) {
  const c = el(`<div class="card ${kind}"><h3>${esc(title)}</h3>${html}</div>`);
  if (buttons.length) {
    const row = el('<div class="row"></div>');
    for (const b of buttons) {
      const btn = el(`<button class="btn ${b.kind || ''}" data-fid="${esc('card:' + title + ':' + b.label)}">${esc(b.busy && b.busyLabel ? b.busyLabel : b.label)}</button>`);
      if (b.disabled) btn.disabled = true;
      btn.onclick = () => b.fn(btn);
      row.appendChild(btn);
    }
    c.appendChild(row);
  }
  return c;
}

const steamSteps = `<p>In Steam: right-click <b>ACE COMBAT 8</b>, <b>Properties</b>, <b>General</b>, then clear <b>Launch options</b>.</p>
  <p class="fine">Steam saves it when it closes, so this can stay until Steam restarts.</p>`;
const manualSetupHtml = '<p>Copy the <b>Game</b> folder from the Mod Loader download into the ACE COMBAT 8 folder and let it merge the folders. The launcher notices by itself.</p>';

function renderTodo() {
  const s = STATE, m = currentMode(), cards = [];
  const su = s.setup;
  // before modding works, the cards are the first thing on the page
  const top = s.game.storageMissing || m === 'mixed' || !!s.unfinishedSwitch || m === 'none';
  const place = () => {
    const rank = { bad: 0, warn: 1, info: 2 };
    cards.sort((a, b) => rank[cardKind(a)] - rank[cardKind(b)]);
    $('#todoTop').replaceChildren(...(top ? cards : []));
    $('#todo').replaceChildren(...(top ? [] : cards));
  };
  if (s.game.storageMissing) {
    cards.push(card('bad', 'Mod storage folder not found', `<p><span class="path">${esc(s.game.storage)}</span> can't be found. Connect the drive it is on, or choose the folder again.</p>`,
      [{ label: 'Choose folder…', kind: 'primary', fn: pickStorage }, { label: 'Use the default', fn: defaultStorage }]));
    place();
    return;
  }
  if (m === 'mixed' || s.unfinishedSwitch) {
    cards.push(card('bad', 'Finish switching', '<p>The last switch stopped halfway: some mod files are in the game and some in storage. Nothing is lost: pick one to finish, and the files from both places are put together.</p>',
      [{ label: 'Make it Modded', fn: () => doSwitch('modded'), disabled: busyLocal }, { label: 'Make it Unmodded', fn: () => doSwitch('unmodded'), disabled: busyLocal }]));
  }
  if (setupMissingIn(s)) {
    const title = m === 'none' ? 'Step 1: Set up modding' : 'Set up modding';
    // while the First steps list shows, its first step is the setup: no second card for it
    const c = firstStepsShown() ? null
      : su.bundleFound ? card('warn', title, `<p>Adds what mods need (UE4SS and the Mod Loader ${esc(su.bundled)}) to the game folder. The game's own files aren't changed, and Unmodded takes it all out again.</p>`,
        [{ label: 'Set up modding', busyLabel: 'Setting up…', busy: setupRunning, kind: 'primary', fn: () => doSetup(), disabled: busyLocal }])
      : card('warn', title, '<p>Mods need UE4SS and the Mod Loader in the game.</p>' + manualSetupHtml,
        [{ label: 'Open the game folder', kind: 'primary', fn: () => reveal({ what: 'game' }) }]);
    if (c) { c.dataset.card = 'setup'; cards.push(c); }
  } else if (m === 'modded' && !su.loaderOn) {
    cards.push(card('warn', 'Mod Loader is off', '<p>No pak mods will load.</p>', [{ label: 'Switch it on', busyLabel: 'Switching on…', busy: setupRunning, kind: 'primary', fn: () => doSetup(), disabled: busyLocal }]));
  } else if (su.canUpdate && m === 'modded') {
    cards.push(card('info', 'Mod Loader update', `<p>${esc(su.loader)} installed, ${esc(su.bundled)} available. Your mods and load order stay.</p>`, [{ label: 'Update', busyLabel: 'Updating…', busy: setupRunning, kind: 'primary', fn: () => doSetup(), disabled: busyLocal }]));
  }
  if (m === 'modded' && su.ue4ss && !su.vcRuntime) {
    cards.push(card('warn', 'One Microsoft install needed', '<p>Mods need Microsoft\'s free Visual C++ runtime. Without it the game may not start while Modded. Download it, open the file (VC_redist.x64.exe), press <b>Install</b>, then come back and press Play.</p>',
      [{ label: 'Download from Microsoft', fn: () => openLink('https://aka.ms/vs/17/release/vc_redist.x64.exe') }]));
  }
  if (m === 'modded' && s.run && (s.run.state === 'unsupported' || s.run.state === 'otherloader')) {
    cards.push(card('bad', s.run.state === 'unsupported' ? 'Game version not supported' : 'Two mod loaders installed', `<p>${esc(s.run.summary)}</p>`));
  }
  if (s.steamOffline && (m === 'unmodded' || m === 'none')) {
    cards.push(card('warn', 'Steam still turns anti-cheat off', `<p>Steam's launch options still have the old anti-cheat line, so online play won't work. The launcher doesn't need it.</p>${steamSteps}`));
  } else if (s.steamOffline && m === 'modded' && lsGet('ace8ml-hide-steamtip') !== '1') {
    cards.push(card('info', 'Clear Steam\'s launch options', `<p>Not needed any more, and they keep anti-cheat off for Unmodded too.</p>${steamSteps}`,
      [{ label: 'Hide', kind: 'ghost', fn: () => { lsSet('ace8ml-hide-steamtip', '1'); renderTodo(); } }]));
  }
  if (isLinux() && s.linux && !s.linux.set && (m === 'modded' || m === 'none')) {
    const c = card('warn', 'One step in Steam', `<p>For Modded, Steam needs the launcher's line in the game's launch options. You only do this once.</p>${linuxSteps(s.linux)}`, linuxButtons(s.linux, true));
    c.dataset.card = 'steam-line';
    cards.push(c);
  }
  const attention = needsALook();
  if (attention.length && m !== 'mixed') {
    cards.push(card('warn', plural(attention.length, 'mod needs', 'mods need') + ' a look',
      `<ul class="attn">${attention.map(x => `<li><b>${esc(shortKey(x.key))}</b>: ${esc(lookReason(x))}</li>`).join('')}</ul>`, [{ label: 'Open Mods', fn: () => go('mods') }]));
  }
  place();
}
const cardKind = c => c.classList.contains('bad') ? 'bad' : c.classList.contains('warn') ? 'warn' : 'info';

// switched-on pak mods with a problem: a warning note, or a failure at the last game start
function lastRunFailed(x) {
  return !!(x.lastRun && x.lastRun.failed && STATE.run && !changedAfter(x, STATE.run.when));
}
function needsALook() {
  return STATE.pakMods.filter(x => x.enabled && ((x.notes || []).some(n => n.kind === 'warn') || lastRunFailed(x)));
}
function lookReason(x) {
  const w = (x.notes || []).find(n => n.kind === 'warn');
  if (w) return w.text;
  return 'It failed to load at the last game start. ' + ((x.lastRun && x.lastRun.messages) || []).join(' ');
}

function renderLastRun() {
  const s = STATE, r = s.run, sr = s.scriptRun, box = $('#lastRun');
  box.hidden = !(r || sr) || currentMode() !== 'modded';
  if (!(r || sr)) return;
  const when = r ? r.when : sr.when;
  const scriptsOn = s.scriptMods.filter(x => x.enabled);
  let summary = r ? r.summary : 'The Mod Loader wrote no log.';
  if (sr && scriptsOn.length) {
    const started = scriptsOn.filter(x => sr.started[x.name.toLowerCase()]).length;
    summary += ` UE4SS started ${started} of ${plural(scriptsOn.length, 'script mod')}.`;
  }
  $('#lastSummary').textContent = (fmtWhen(when) ? 'Started ' + fmtWhen(when) + '. ' : '') + summary;
  const newer = (m, t) => changedAfter(m, t) ? (m.updated && new Date(m.updated) > new Date(t) ? 'Updated after that start.' : 'Added after that start.') : '';
  const rows = [];
  for (const m of r ? s.pakMods : []) {
    if (!m.enabled) continue;
    const lr = m.lastRun;
    if (lr && lr.failed) rows.push(`<li><span class="st no">Failed</span><span>${esc(m.key)} <span class="msg">${esc(lr.messages.join(' '))}</span></span></li>`);
    else if (lr && lr.loaded) rows.push(`<li><span class="st ok">Loaded</span><span>${esc(m.key)}</span></li>`);
    else rows.push(`<li><span class="st off">Not loaded</span><span>${esc(m.key)} <span class="msg">${esc(newer(m, r.when) || ((m.notes || []).find(n => n.kind === 'warn') || {}).text || '')}</span></span></li>`);
  }
  if (sr) {
    for (const m of scriptsOn) {
      if (sr.started[m.name.toLowerCase()]) rows.push(`<li><span class="st ok">Started</span><span>${esc(m.name)} <span class="msg">Script mod</span></span></li>`);
      else rows.push(`<li><span class="st off">Not started</span><span>${esc(m.name)} <span class="msg">${esc(newer(m, sr.when) || 'UE4SS didn\'t start it. Check that the mod\'s folder has Scripts\\main.lua or dlls\\main.dll, or look in the UE4SS log.')}</span></span></li>`);
    }
    for (const p of sr.problems || []) rows.push(`<li><span class="st no">UE4SS</span><span><span class="msg">${esc(p)}</span></span></li>`);
  }
  $('#lastMods').innerHTML = rows.join('');
}

// ---------------------------------------------------------------- switching & playing
async function doSwitch(to) {
  if (busyLocal) return;
  if (gameRunningNow()) { $('#modeNote').textContent = 'Close the game first, then switch.'; $('#modeNote').classList.add('bad'); return; }
  if (playing || launchStarting()) { $('#modeNote').textContent = 'One moment: the game is starting.'; return; }
  busyLocal = true;
  switchNote = null;
  const sw = $('#modeSwitch'); sw.classList.add('working');
  $('#modeNote').classList.remove('bad');
  $('#modeNote').textContent = to === 'modded' ? 'Moving your mods into the game…' : 'Moving your mods to storage…';
  renderPlay();
  let r = null;
  try { r = await act('switch', { to }); } finally { busyLocal = false; sw.classList.remove('working'); }
  if (r) {
    toast(to === 'modded' ? 'Switched to Modded. Your mods are back in the game.' : 'Switched to Unmodded. Your mods are in the mod storage folder.', 'ok',
      r.aside && r.aside.length ? { list: r.aside.map(a => 'A copy that was in the way is kept as ' + baseName(a)) } : {});
  } else {
    // the launcher's reason stays under the switch until the next switch or a mode change
    await loadState();
    switchNote = { text: lastError || 'The switch did not work.', mode: currentMode() };
  }
  render();
}

for (const b of $$('.mode-opt')) b.onclick = () => {
  const m = currentMode(), want = b.dataset.mode;
  if (b.getAttribute('aria-disabled') === 'true') {
    const st = STATUS || STATE.status;
    if (st.gameRunning) { $('#modeNote').textContent = 'Close the game first, then switch.'; $('#modeNote').classList.add('bad'); }
    else if (st.busy && !busyLocal) { $('#modeNote').textContent = 'One moment: the launcher is ' + st.busy + '.'; }
    else if (playing || launchStarting()) { $('#modeNote').textContent = 'One moment: the game is starting.'; }
    return;
  }
  if (m === want && m !== 'mixed') return;
  if (m === 'none') {
    if (want === 'modded') { if (STATE.setup.bundleFound) doSetup(); else showManualSetup(); }
    else $('#modeNote').textContent = 'This is already the normal game: no mod files are installed.';
    return;
  }
  doSwitch(want);
};
// arrow keys move between the two positions, like radio buttons
$('#modeSwitch').addEventListener('keydown', e => {
  if (['ArrowLeft', 'ArrowRight', 'ArrowUp', 'ArrowDown'].includes(e.key)) {
    e.preventDefault();
    const other = document.activeElement === $('#optModded') ? $('#optUnmodded') : $('#optModded');
    other.focus();
  }
});

$('#btnPlay').onclick = async () => {
  if (isLinux()) return playLinux();
  if (playing) return;
  const st = STATUS || STATE.status, modded = currentMode() === 'modded';
  playing = true;
  renderPlayBar();
  setLive(modded && !st.steamRunning ? 'Opening Steam first…' : 'Starting…');
  // Modded waits for Steam to be signed in before the game starts
  const slow = modded ? setTimeout(() => { setLive('Waiting for Steam to sign in…'); }, 6000) : 0;
  let r = null;
  try { r = await act('play', {}); } finally { clearTimeout(slow); playing = false; }
  if (!r) setLive('');
  else if (r.note) toast(r.note, 'warn');
  renderPlayBar();
  pollSoon();
};

// ---------------------------------------------------------------- Linux: Steam starts the game
function linuxSteps(L) {
  return `<p>In Steam: right-click <b>ACE COMBAT 8</b>, <b>Properties</b>, <b>General</b>, <b>Launch Options</b>, and paste this:</p><code class="cmd">${esc(L.suggested)}</code>`
    + (L.stale ? '<p class="fine">Steam has the line from an older place of the launcher\'s script; this one replaces it.</p>' : '');
}
function linuxButtons(L, withHow) {
  return [{ label: 'Copy the line', kind: 'primary', fn: () => copyText(L.suggested, 'Copied. Paste it into Steam\'s launch options for ACE COMBAT 8.') },
    { label: 'Set it for me', fn: btn => withBusy(btn, 'steam-options', 'Setting…', setLaunchOptions) }]
    .concat(withHow ? [{ label: 'More', kind: 'ghost', fn: () => { go('settings'); $('#startBlock').open = true; document.getElementById('h-start').scrollIntoView({ behavior: 'smooth' }); } }] : []);
}
async function setLaunchOptions() {
  const r = await act('steam-launch-options', {});
  if (r) toast('Steam\'s launch options are set. Start Steam again whenever you like.', 'ok');
}
async function playLinux() {
  const L = STATE.linux, modded = currentMode() === 'modded';
  if (playing) return;
  let force = false;
  if (modded && L && !L.set) {
    const v = await dialog({ title: 'One step in Steam first', wide: true,
      html: `<p>For Modded, Steam needs the launcher's line in the game's launch options. Without it the game starts with anti-cheat on and no mods.</p>${linuxSteps(L)}<p class="fine">Steam may only save launch options when it closes. If you've just pasted the line, choose <b>It's in, play</b>.</p>`,
      buttons: [{ label: 'Cancel', value: 'cancel', kind: 'ghost' }, { label: 'Copy the line', value: 'copy' }, { label: 'Set it for me', value: 'set' }, { label: 'It\'s in, play', value: 'force', kind: 'primary' }] });
    if (v === 'copy') { copyText(L.suggested, 'Copied. Paste it into Steam\'s launch options for ACE COMBAT 8, then press Play.'); return; }
    if (v === 'set') { await setLaunchOptions(); return; }
    if (v !== 'force') return;
    force = true;
  }
  playing = true;
  renderPlayBar();
  setLive('Asking Steam to start the game…');
  let r = null;
  try { r = await act('play', { force }); } finally { playing = false; }
  if (!r) setLive('');
  renderPlayBar();
  pollSoon();
}

function renderLinuxStart() {
  const s = STATE, L = s.linux, box = $('#linuxOpts');
  if (!L) { box.innerHTML = ''; return; }
  const parts = [];
  if (L.set) {
    parts.push(`<div class="banner">Steam's launch options have the launcher's line.<code class="cmd">${esc(L.current)}</code></div>`);
  } else {
    parts.push(`<div class="banner warn">${L.current ? 'Steam\'s launch options don\'t have the launcher\'s line yet.' : 'Steam has no launch options for the game yet.'} ${linuxSteps(L)}</div>`);
  }
  if (L.lastStart && L.lastStart.when) {
    const mode = { modded: 'Modded, anti-cheat off', unmodded: 'Unmodded', unknown: 'not recognised: ' + (L.lastStart.text || '') }[L.lastStart.mode] || L.lastStart.mode;
    parts.push(`<p class="fine">Last start from Steam: ${esc(fmtWhen(L.lastStart.when) || '')}, ${esc(mode)}.</p>`);
  }
  box.innerHTML = parts.join('');
  const row = el('<div class="row"></div>');
  for (const bt of linuxButtons(L, false).slice(0, L.set ? 1 : 2)) {
    const btn = el(`<button class="btn ${L.set ? '' : bt.kind || ''}" data-fid="${esc('linux:' + bt.label)}">${esc(bt.label)}</button>`);
    btn.onclick = () => bt.fn(btn); row.appendChild(btn);
  }
  box.appendChild(row);
  if (!L.set) box.appendChild(el('<p class="fine">Set it for me changes Steam\'s settings directly, keeping any launch options you already have. Steam has to be closed for that: Steam menu, Exit. A copy of the old settings is kept next to them.</p>'));
}

// When the computer has no file chooser (zenity or kdialog), the path is typed in.
async function askPath(title, label, okLabel) {
  let inp = null;
  const v = await dialog({ title, wide: true, html: `<label class="field"><span>${esc(label)}</span><input type="text" id="pathIn" spellcheck="false" autocomplete="off"></label>`,
    buttons: [{ label: 'Cancel', value: 'cancel', kind: 'ghost' }, { label: okLabel || 'Use this folder', value: 'ok', kind: 'primary' }],
    onOpen: body => { inp = body.querySelector('#pathIn'); inp.focus(); } });
  // a pasted path may come with quotes around it
  const p = inp ? inp.value.trim().replace(/^(["'])(.*)\1$/, '$2').trim() : '';
  return v === 'ok' && p ? p : null;
}

// Installs or updates UE4SS and the Mod Loader. opts.next: suggest adding a mod afterwards.
async function doSetup(opts = {}) {
  if (busyLocal) return false;
  const su = STATE.setup;
  const fresh = !su.ue4ss || !su.loader;
  const doing = fresh ? 'Setting up modding…' : su.canUpdate ? 'Updating the Mod Loader…' : !su.loaderOn ? 'Switching the Mod Loader on…' : 'Repairing UE4SS and the Mod Loader…';
  busyLocal = true; setupRunning = true;
  let ended;
  setupDone = new Promise(res => { ended = res; });
  const sw = $('#modeSwitch'); sw.classList.add('working');
  const note = $('#modeNote'); note.classList.remove('bad'); note.textContent = doing;
  render();
  const t = toastProgress(doing);
  let r = null;
  try { r = await act('setup', {}); } finally { t.remove(); busyLocal = false; setupRunning = false; sw.classList.remove('working'); ended(); }
  render();
  if (!r) return false;
  const kept = r.kept && r.kept.length ? ['Kept your own: ' + r.kept.map(baseName).join(', ')] : [];
  const nowSu = STATE.setup;
  const done = fresh ? 'Modding is set up.' : su.canUpdate && !nowSu.canUpdate ? 'The Mod Loader is up to date' + (nowSu.loader && nowSu.loader !== 'unknown' ? ' (' + nowSu.loader + ').' : '.') : !su.loaderOn ? 'The Mod Loader is on.' : 'UE4SS and the Mod Loader are repaired.';
  const hasMods = STATE.pakMods.length + STATE.scriptMods.length > 0;
  if (fresh && !hasMods && opts.next !== false) toast('Modding is set up. Next: add a mod.', 'ok', { list: kept, action: { label: 'Add a mod…', fn: () => { go('mods'); chooseAndAdd(false); } } });
  else toast(done, 'ok', { list: kept });
  return true;
}

// Without the install files the launcher can't set up by itself: say how to do it by hand.
async function showManualSetup() {
  const v = await dialog({ title: 'Set up modding first', html: '<p>Mods need UE4SS and the Mod Loader in the game, and the launcher didn\'t find their install files next to it.</p>' + manualSetupHtml + '<p>Then add your mod again.</p>',
    buttons: [{ label: 'Close', value: 'cancel', kind: 'ghost' }, { label: 'Open the game folder', value: 'open', kind: 'primary' }] });
  if (v === 'open') reveal({ what: 'game' });
}

// Adding needs the Mod Loader. Before setup, offer to set up first, then carry on with the same files.
// A setup already running from this window is waited for, then the add carries on.
async function readyToAdd() {
  const s = STATE;
  if (!s || !s.game.found) { toast('Choose the game folder first.', 'bad'); go('play'); return false; }
  if (gameRunningNow()) { toast(GAME_RUNNING, 'warn'); return false; }
  const m = currentMode();
  if (s.setup.loader || (m !== 'none' && m !== 'modded')) return true;
  if (setupRunning) { await setupDone; return readyToAdd(); }
  if (!s.setup.bundleFound) { await showManualSetup(); return false; }
  const v = await dialog({ title: 'Set up modding first', html: '<p>Mods need UE4SS and the Mod Loader in the game. The launcher can add them now, then add your mod.</p>',
    buttons: [{ label: 'Cancel', value: 'cancel', kind: 'ghost' }, { label: 'Set up and add', value: 'go', kind: 'primary' }] });
  if (v !== 'go') return false;
  // while the question was open, a setup may have started or finished
  if (setupRunning) { await setupDone; return readyToAdd(); }
  if (STATE.setup.loader) return true;
  if (busyLocal) { toast('One moment: the launcher is busy. Add the mod again when it\'s done.', 'warn'); return false; }
  return doSetup({ next: false });
}

$('#btnShowLog').onclick = $('#btnShowLog2').onclick = showLoaderLog;
$('#btnShowUe4ssLog').onclick = $('#btnShowUe4ssLog2').onclick = showUe4ssLog;
async function readLog(path) {
  try {
    const r = await api(path);
    if (r.ok) return typeof r.text === 'string' ? r.text : '';
    toast(r.error || 'The log could not be read.', 'bad');
  } catch (e) { toast(e.message, 'bad'); }
  return null;
}
async function showUe4ssLog() {
  const text = await readLog('ue4ss-log');
  if (text === null) return;
  dialog({ title: 'UE4SS log', wide: true, html: text ? `<p class="fine">UE4SS starts the script mods; this is its log from the last game start (the last 400 lines).</p><pre>${esc(text)}</pre>` : '<p>There is no UE4SS log yet. UE4SS writes it each time the game starts while Modded.</p>',
    buttons: [{ label: 'Close', value: 'close', kind: 'primary' }] });
}
async function showLoaderLog() {
  const text = await readLog('loader-log');
  if (text === null) return;
  const v = await dialog({ title: 'Mod Loader log', wide: true, html: text ? `<pre>${esc(text)}</pre>` : '<p>There is no log yet. The Mod Loader writes it each time the game starts while Modded.</p>',
    buttons: (text ? [{ label: 'Open the folder', value: 'open', kind: 'ghost' }] : []).concat([{ label: 'Close', value: 'close', kind: 'primary' }]) });
  if (v === 'open') reveal({ what: 'loaderlog' });
}

// ---------------------------------------------------------------- mods page
let order = [];          // [{key, enabled}] as shown
let pendingOrder = null; // changes made here that the launcher hasn't saved yet; they win over incoming state
let orderVer = 0, sentVer = 0, savingOrder = null, saveTimer = null;
let selectedKey = null;
let newKeys = null;      // mods just added or updated: marked until you leave the Mods page
const openFiles = new Set(); // "Changes N game files" lists that are open

// Puts this window's unsaved order over a state that came from the launcher:
// known mods keep their place and on/off, new ones go at the end, gone ones drop out.
function mergePendingOrder() {
  if (!pendingOrder || !STATE) return;
  const byKey = new Map(STATE.pakMods.map(x => [x.key, x]));
  const merged = [];
  for (const o of pendingOrder) {
    const x = byKey.get(o.key);
    if (x) { x.enabled = o.enabled; merged.push(x); byKey.delete(o.key); }
  }
  for (const x of byKey.values()) merged.push(x);
  STATE.pakMods = merged.filter(x => !x.skinList).concat(merged.filter(x => x.skinList));
}

function isNew(keyOrName) { return !!(newKeys && newKeys.has(keyOrName)); }

function renderMods() {
  const s = STATE;
  const m = currentMode(), running = gameRunningNow();
  const banner = $('#modsBanner');
  banner.innerHTML = '';
  if (running) banner.appendChild(el('<div class="banner warn">The game is running. Close it to add or remove mods. You can still switch mods on or off and change the order; that applies the next time the game starts.</div>'));
  if (m === 'unmodded') banner.appendChild(el('<div class="banner">Unmodded: your mods are in storage. Changes here count when you switch back to Modded.</div>'));
  if (s.game.found && !s.setup.loader && m !== 'unmodded') {
    const b = el(`<div class="banner warn"><p>Modding isn't set up yet. ${s.setup.bundleFound ? 'Mods need UE4SS and the Mod Loader in the game; the launcher adds them in one step.' : 'Mods need UE4SS and the Mod Loader in the game.'}</p>${s.setup.bundleFound ? '' : manualSetupHtml}<div class="row"></div></div>`);
    const btn = s.setup.bundleFound
      ? el(`<button class="btn primary" data-fid="mods:setup">${setupRunning ? 'Setting up…' : 'Set up modding'}</button>`)
      : el('<button class="btn primary" data-fid="mods:opengame">Open the game folder</button>');
    if (s.setup.bundleFound) { btn.disabled = busyLocal; btn.onclick = () => doSetup(); } else btn.onclick = () => reveal({ what: 'game' });
    b.querySelector('.row').appendChild(btn);
    banner.appendChild(b);
  }
  // adding and removing need the game closed; on/off and the order don't
  for (const b of [$('#btnAddFiles'), $('#btnAddFolder')]) b.setAttribute('aria-disabled', String(running));
  $('#btnAddFiles').title = running ? 'Close the game first' : 'Choose a mod\'s .zip, or its .utoc, .ucas and .pak files';
  $('#btnAddFolder').title = running ? 'Close the game first' : 'Choose a mod\'s folder, for a mod you have already unzipped';
  $('#btnOpenLoader').title = m === 'unmodded' ? 'Open the folder your mods wait in while Unmodded' : 'Open the folder the Mod Loader loads pak mods from';

  order = s.pakMods.map(x => ({ key: x.key, enabled: x.enabled }));
  const list = $('#pakList');
  list.innerHTML = '';
  $('#pakEmpty').hidden = s.pakMods.length > 0;
  $('#orderHint').hidden = s.pakMods.length < 2;
  const on = s.pakMods.filter(x => x.enabled).length;
  $('#orderSub').textContent = s.pakMods.length ? `${on} of ${s.pakMods.length} on` : '';
  s.pakMods.forEach((mod, i) => list.appendChild(pakRow(mod, i)));

  const warn = needsALook().length;
  $('#navModsBadge').hidden = !warn; $('#navModsBadge').textContent = warn;
  $('#navModsBadge').title = warn ? plural(warn, 'mod needs', 'mods need') + ' a look' : '';

  const sl = $('#scriptList'); sl.innerHTML = '';
  $('#scriptEmpty').hidden = s.scriptMods.length > 0;
  for (const sm of s.scriptMods) sl.appendChild(scriptRow(sm));

  const rb = $('#removedBlock'), rl = $('#removedList');
  rb.hidden = !s.removed.length;
  $('#removedSub').textContent = s.removed.length ? plural(s.removed.length, 'mod') : '';
  rl.innerHTML = '';
  for (const r of s.removed) rl.appendChild(removedRow(r));
  tourPlaceSoon();
}

const gripSVG = '<svg viewBox="0 0 12 16" aria-hidden="true"><circle cx="3" cy="3" r="1.4"/><circle cx="9" cy="3" r="1.4"/><circle cx="3" cy="8" r="1.4"/><circle cx="9" cy="8" r="1.4"/><circle cx="3" cy="13" r="1.4"/><circle cx="9" cy="13" r="1.4"/></svg>';
const lockSVG = '<svg viewBox="0 0 12 16" aria-hidden="true"><path d="M3 7V5a3 3 0 0 1 6 0v2h1v7H2V7zm1.5 0h3V5a1.5 1.5 0 0 0-3 0z"/></svg>';
const TOGGLE_TIP = 'On: loads in the game. Off: stays installed but doesn\'t load.';
const REMOVE_TIP = 'Take it out of the game. It\'s kept under Removed mods, so you can put it back.';

// "Updated 6 Nov" / "Added today": when the launcher last put the mod in
function modDateText(m, fallback) {
  if (m.updated && fmtDay(m.updated)) return ['Updated ' + esc(fmtDay(m.updated))];
  if (m.added && fmtDay(m.added)) return ['Added ' + esc(fmtDay(m.added))];
  const w = fallback ? fmtDay(fallback) : '';
  return w ? ['Added ' + esc(w)] : [];
}

// Remove and Restore need the game closed: the buttons say why instead of failing
function gameLock(btn) {
  if (!gameRunningNow()) return;
  btn.setAttribute('aria-disabled', 'true');
  btn.title = 'Close the game first';
}

function pakRow(m, i) {
  const pinned = m.skinList, fresh = isNew(m.key);
  const li = el(`<li class="item ${m.enabled ? '' : 'off'} ${m.key === selectedKey || fresh ? 'selected' : ''} ${fresh ? 'new' : ''}" tabindex="0" data-key="${esc(m.key)}" aria-label="${esc(shortKey(m.key))}, position ${i + 1}, ${m.enabled ? 'on' : 'off'}"></li>`);
  const handle = el(`<button class="handle ${pinned ? 'pinned' : ''}" title="${pinned ? 'Skin Studio\'s skin list always loads last, so every new skin is in it' : 'Drag to change the order. With the keyboard: select a mod and press Alt + ↑ or ↓.'}" aria-label="${pinned ? 'Always last' : 'Move ' + esc(m.key)}" tabindex="-1">${pinned ? lockSVG : gripSVG}</button>`);
  const tog = el(`<button class="toggle" role="switch" data-ctl="toggle" aria-checked="${m.enabled}" aria-label="Load ${esc(m.key)}" title="${TOGGLE_TIP}"></button>`);
  tog.onclick = e => { e.stopPropagation(); selectedKey = m.key; setEnabled(m.key, !(tog.getAttribute('aria-checked') === 'true')); };
  const tags = [];
  if (fresh) tags.push('<span class="tag ok new-tag">New</span>');
  if (m.skin) tags.push(`<span class="tag skin">Skin Studio skin${m.skin.aircraft && m.skin.aircraft.length ? ': ' + esc(m.skin.aircraft.join(', ')) : ''}</span>`);
  if (pinned) tags.push('<span class="tag skin">Skin Studio skin list</span>');
  if (m.containers.some(c => /_p\.pak$/i.test(c.name))) tags.push('<span class="tag" title="A .pak mod (text, fonts and other files kept in .pak files)">.pak</span>');
  if (m.where === 'tilde') tags.push('<span class="tag" title="In the game\'s Content\\Paks\\~mods folder. The game reads that folder by itself, so switching a mod there off may not stop it; Remove does.">~mods</span>');
  const failed = m.enabled && lastRunFailed(m);
  if (m.enabled && m.lastRun && !changedAfter(m, STATE.run ? STATE.run.when : 0)) tags.push(m.lastRun.failed ? '<span class="tag no">Failed</span>' : '<span class="tag ok">Loaded</span>');
  const files = [].concat(...m.containers.map(c => c.files || []));
  const meta = [];
  if (m.skin && m.skin.title && m.skin.title !== m.name) meta.push(esc(m.skin.title));
  meta.push(fmtSize(m.size));
  if (m.containers.length > 1) meta.push(m.containers.length + ' mod files');
  meta.push(...modDateText(m, m.mtime));
  const mainCol = el(`<div class="main-col"><div class="name">${esc(shortKey(m.key))}</div><div class="meta">${tags.join('')}<span>${meta.join('</span><span>')}</span></div></div>`);
  const actions = el(`<div class="actions"></div>`);
  const up = el(`<button class="btn small ghost mv" data-ctl="up" title="Move up: loads earlier" aria-label="Move ${esc(m.key)} up">▲</button>`);
  const dn = el(`<button class="btn small ghost mv" data-ctl="down" title="Move down: loads later, and wins when two mods change the same thing" aria-label="Move ${esc(m.key)} down">▼</button>`);
  up.onclick = e => { e.stopPropagation(); move(m.key, -1); };
  dn.onclick = e => { e.stopPropagation(); move(m.key, 1); };
  if (pinned) { up.disabled = dn.disabled = true; }
  const show = el(`<button class="btn small ghost" data-ctl="files" title="Show this mod's files in ${fmWord()}">Files</button>`);
  show.onclick = e => { e.stopPropagation(); reveal({ path: m.path }); };
  const rm = el(`<button class="btn small ghost" data-ctl="remove" title="${REMOVE_TIP}">Remove</button>`);
  rm.onclick = e => { e.stopPropagation(); removeMod('pak', m.key, rm); };
  gameLock(rm);
  markBusy(rm, 'remove:pak:' + m.key, 'Removing…');
  actions.append(up, dn, show, rm);
  li.append(handle, tog, mainCol, actions);
  const notes = (m.notes || []).map(n => `<div class="note ${n.kind}">${esc(n.text)}</div>`);
  if (failed && !(m.notes || []).some(n => n.kind === 'warn')) notes.push(`<div class="note warn">${esc(lookReason(m))}</div>`);
  if (notes.length) li.appendChild(el(`<div class="notes">${notes.join('')}</div>`));
  if (files.length) {
    const d = el(`<details class="files" ${openFiles.has(m.key) ? 'open' : ''}><summary data-ctl="files-list">Changes ${plural(files.length, 'game file')}</summary><ul>${files.map(f => `<li>${esc(f)}</li>`).join('')}</ul></details>`);
    d.addEventListener('toggle', () => { if (d.open) openFiles.add(m.key); else openFiles.delete(m.key); });
    li.appendChild(d);
  }
  li.onclick = () => selectRow(m.key);
  li.onkeydown = e => {
    if (e.altKey && (e.key === 'ArrowUp' || e.key === 'ArrowDown')) { e.preventDefault(); selectedKey = m.key; move(m.key, e.key === 'ArrowUp' ? -1 : 1); }
    else if (e.key === ' ' && e.target === li) { e.preventDefault(); tog.click(); }
    else if ((e.key === 'ArrowUp' || e.key === 'ArrowDown') && e.target === li) {
      e.preventDefault();
      const sib = e.key === 'ArrowUp' ? li.previousElementSibling : li.nextElementSibling;
      if (sib) { sib.focus(); selectRow(sib.dataset.key); }
    }
  };
  if (!pinned) wireDrag(handle, li);
  return li;
}

function selectRow(key) {
  selectedKey = key;
  for (const x of $$('#pakList .item')) x.classList.toggle('selected', x.dataset.key === selectedKey || isNew(x.dataset.key));
}

function lastMovable() {
  let n = order.length;
  while (n > 0 && STATE.pakMods.find(x => x.key === order[n - 1].key)?.skinList) n--;
  return n - 1;
}

// shows a changed order at once and saves it a moment later
function applyOrder(newOrder, opts = {}) {
  // the skin list stays last
  const pins = newOrder.filter(o => STATE.pakMods.find(x => x.key === o.key)?.skinList);
  order = newOrder.filter(o => !pins.includes(o)).concat(pins);
  pendingOrder = order.map(o => ({ key: o.key, enabled: o.enabled }));
  orderVer++;
  const f = rememberFocus();
  mergePendingOrder();
  renderMods();
  restoreFocus(f, opts.moved);
  renderHud();
  scheduleSave();
}

function move(key, d) {
  const i = order.findIndex(o => o.key === key), j = i + d;
  if (i < 0 || j < 0 || j > lastMovable()) return;
  const o = order.slice(); [o[i], o[j]] = [o[j], o[i]];
  selectedKey = key;
  applyOrder(o, { moved: true });
}

function setEnabled(key, on) {
  const o = order.map(x => x.key === key ? { key, enabled: on } : x);
  applyOrder(o);
}

// One save at a time; changes made while one is on its way are sent as soon as it's back.
function orderUnsaved() { return !!(saveTimer || savingOrder || (pendingOrder && orderVer !== sentVer)); }
function scheduleSave() {
  clearTimeout(saveTimer);
  saveTimer = setTimeout(() => { saveTimer = null; flushOrder(); }, 450);
}
async function flushOrder() {
  clearTimeout(saveTimer); saveTimer = null;
  while (savingOrder) await savingOrder;
  if (!pendingOrder || orderVer === sentVer) return;
  const ver = orderVer, mods = pendingOrder.map(o => ({ key: o.key, enabled: o.enabled }));
  sentVer = ver;
  savingOrder = (async () => {
    const r = await act('order', { mods });
    if (!r) {
      // refused: show the order the launcher really has (the red message says why)
      pendingOrder = null; sentVer = orderVer;
      await loadState();
      return;
    }
    if (orderVer === ver) {
      pendingOrder = null;
      toast(gameRunningNow() ? 'Saved. Restart the game to use it.' : 'Saved. It applies the next time the game starts.', 'ok', { id: 'order' });
    }
  })();
  try { await savingOrder; } finally { savingOrder = null; }
  if (pendingOrder && orderVer !== sentVer) await flushOrder();
}

// dragging a mod by its handle
function wireDrag(handle, li) {
  handle.addEventListener('pointerdown', e => {
    if (e.button !== 0) return;
    e.preventDefault();
    handle.setPointerCapture(e.pointerId);
    li.classList.add('dragging');
    const items = $$('#pakList .item');
    let target = null, after = false;
    const onMove = ev => {
      for (const x of items) x.classList.remove('drop-before', 'drop-after');
      target = null;
      for (const x of items) {
        const r = x.getBoundingClientRect();
        if (ev.clientY >= r.top && ev.clientY <= r.bottom) { target = x; after = ev.clientY > r.top + r.height / 2; break; }
      }
      if (target && target !== li) target.classList.add(after ? 'drop-after' : 'drop-before');
    };
    const onUp = () => {
      handle.removeEventListener('pointermove', onMove);
      handle.removeEventListener('pointerup', onUp);
      handle.removeEventListener('pointercancel', onUp);
      for (const x of items) x.classList.remove('drop-before', 'drop-after', 'dragging');
      if (!target || target === li) return;
      const key = li.dataset.key, tkey = target.dataset.key;
      const o = order.filter(x => x.key !== key);
      const j = o.findIndex(x => x.key === tkey) + (after ? 1 : 0);
      o.splice(j, 0, order.find(x => x.key === key));
      selectedKey = key;
      applyOrder(o);
    };
    handle.addEventListener('pointermove', onMove);
    handle.addEventListener('pointerup', onUp);
    handle.addEventListener('pointercancel', onUp);
  });
}

function scriptRow(sm) {
  const fresh = isNew(sm.name);
  const li = el(`<li class="item ${sm.enabled ? '' : 'off'} ${fresh ? 'selected new' : ''}" data-key="${esc(sm.name)}"></li>`);
  const tog = el(`<button class="toggle" role="switch" data-ctl="toggle" aria-checked="${sm.enabled}" aria-label="Load ${esc(sm.name)}" title="${TOGGLE_TIP}"></button>`);
  tog.onclick = () => withBusy(tog, 'script:' + sm.name, '', async () => {
    const r = await act('script', { name: sm.name, enabled: !sm.enabled });
    if (r) toast(`${sm.name} is ${!sm.enabled ? 'on' : 'off'}. ${gameRunningNow() ? 'Restart the game to use it.' : 'It applies the next time the game starts.'}`, 'ok', { id: 'script' });
  });
  markBusy(tog, 'script:' + sm.name, '');
  const dates = modDateText(sm).map(x => `<span>${x}</span>`).join('');
  const main = el(`<div class="main-col"><div class="name">${esc(sm.name)}</div><div class="meta">${fresh ? '<span class="tag ok new-tag">New</span>' : ''}<span class="tag">${esc(sm.kind)}</span>${sm.description ? `<span>${esc(sm.description)}</span>` : ''}${dates}</div></div>`);
  const actions = el('<div class="actions"></div>');
  const show = el(`<button class="btn small ghost" data-ctl="files" title="Show this mod's files in ${fmWord()}">Files</button>`);
  show.onclick = () => reveal({ path: sm.path });
  const rm = el(`<button class="btn small ghost" data-ctl="remove" title="${REMOVE_TIP}">Remove</button>`);
  rm.onclick = () => removeMod('script', sm.name, rm);
  gameLock(rm);
  markBusy(rm, 'remove:script:' + sm.name, 'Removing…');
  actions.append(show, rm);
  // two empty cells, so the switches line up with the pak mods' above
  li.append(el('<span></span>'), el('<span></span>'), tog, main, actions);
  return li;
}

function removedRow(r) {
  const li = el(`<li class="item" data-key="${esc(r.id)}"></li>`);
  const kind = { pak: 'Pak mod', tilde: '~mods', script: 'Script mod', logic: 'LogicMods', modloader: 'Old Mod Loader', shared: 'Replaced shared files' }[r.kind] || 'Mod';
  li.appendChild(el(`<div class="main-col"><div class="name">${esc(shortKey(r.name))}</div><div class="meta"><span class="tag">${esc(kind)}</span><span>${fmtSize(r.size)}</span><span>Removed ${esc(fmtWhen(r.removed))}</span></div></div>`));
  const actions = el('<div class="actions"></div>');
  if (r.kind !== 'modloader') {
    const rs = el('<button class="btn small" data-ctl="restore" title="Put it back in the game">Restore</button>');
    rs.onclick = () => {
      if (gameRunningNow()) { toast(GAME_RUNNING, 'warn'); return; }
      withBusy(rs, 'restore:' + r.id, 'Restoring…', async () => { if (await act('restore', { id: r.id })) toast(`${shortKey(r.name)} is back.`); });
    };
    gameLock(rs);
    markBusy(rs, 'restore:' + r.id, 'Restoring…');
    actions.appendChild(rs);
  }
  const del = el('<button class="btn small danger" data-ctl="delete" title="Delete its files for good">Delete</button>');
  del.onclick = async () => {
    if (inFlight.has('delete:' + r.id)) return;
    const v = await dialog({ title: 'Delete ' + shortKey(r.name) + '?', html: '<p>Its files are deleted from the mod storage folder. This can\'t be undone.</p>', buttons: [{ label: 'Keep it', value: 'no', kind: 'ghost' }, { label: 'Delete', value: 'yes', kind: 'danger' }] });
    if (v !== 'yes') return;
    await withBusy(del, 'delete:' + r.id, 'Deleting…', async () => { if (await act('delete-removed', { id: r.id })) toast(shortKey(r.name) + ' was deleted.'); });
  };
  markBusy(del, 'delete:' + r.id, 'Deleting…');
  actions.appendChild(del);
  li.appendChild(actions);
  return li;
}

async function removeMod(kind, key, btn) {
  if (gameRunningNow()) { toast(GAME_RUNNING, 'warn'); return; }
  await withBusy(btn, 'remove:' + kind + ':' + key, 'Removing…', async () => {
    const before = order.map(o => ({ ...o }));
    // the row goes away: keyboard focus moves to the row after it (or before it)
    const li = btn && btn.closest('li.item'), hadFocus = !!(li && li.contains(document.activeElement));
    const list = li && li.parentElement.id, near = li && (li.nextElementSibling || li.previousElementSibling);
    const nearKey = near && near.dataset.key;
    const dir = await act('remove', { kind, key });
    if (!dir) return;
    if (hadFocus && nearKey && (!document.activeElement || document.activeElement === document.body)) restoreFocus({ list, key: nearKey, ctl: 'remove' });
    const id = baseName(dir);
    toast(`${shortKey(key)} moved to Removed mods.`, 'ok', { action: { label: 'Undo', fn: async () => {
      if (!await act('restore', { id })) return;
      toast(shortKey(key) + ' is back.');
      if (kind === 'pak') putBack(key, before);
    } } });
  });
}

// Undo: the mod goes back next to the mod it was after; everything changed since stays as it is now.
function putBack(key, before) {
  const i = before.findIndex(o => o.key === key);
  if (i < 0 || !order.some(o => o.key === key)) return;
  const o = order.filter(x => x.key !== key);
  let at = 0;
  for (let k = i - 1; k >= 0; k--) {
    const j = o.findIndex(x => x.key === before[k].key);
    if (j >= 0) { at = j + 1; break; }
  }
  o.splice(at, 0, { key, enabled: before[i].enabled });
  if (o.map(x => x.key).join('\n') !== order.map(x => x.key).join('\n') || o[at].enabled !== order.find(x => x.key === key).enabled) applyOrder(o);
}

// ---------------------------------------------------------------- adding mods
$('#btnAddFiles').onclick = () => chooseAndAdd(false);
$('#btnAddFolder').onclick = () => chooseAndAdd(true);
$('#btnOpenLoader').onclick = () => reveal({ what: 'loader' });

async function chooseAndAdd(folder) {
  if (!STATE || !STATE.game.found) { toast('Choose the game folder first.', 'bad'); go('play'); return; }
  if (gameRunningNow()) { toast(GAME_RUNNING, 'warn'); return; }
  if (!STATE.hasPicker) {
    // no file chooser on this computer: the path is typed in
    const p = await askPath(folder ? 'Add an unzipped folder' : 'Add a mod',
      folder ? 'Type or paste the mod\'s folder, for example /home/deck/Downloads/NewSkin' : 'Type or paste the mod\'s .zip or folder, for example /home/deck/Downloads/NewSkin.zip', 'Add');
    if (p) addPaths([p]);
    return;
  }
  // the file chooser is open: both add buttons wait for it
  if (inFlight.has('pick-mods')) return;
  inFlight.add('pick-mods');
  const btns = [$('#btnAddFiles'), $('#btnAddFolder')];
  for (const b of btns) b.disabled = true;
  let paths = null;
  try { paths = await act('pick-mods', { folder }); } finally { inFlight.delete('pick-mods'); for (const b of btns) b.disabled = false; }
  if (paths && paths.length) addPaths(paths);
}

// key -> when it was last changed, to find what an add put in
function modsSnapshot() {
  const snap = new Map();
  for (const m of STATE.pakMods) snap.set(m.key, String(m.updated || m.added || m.mtime || ''));
  for (const m of STATE.scriptMods) snap.set(m.name, String(m.updated || m.added || ''));
  return snap;
}

async function addPaths(paths, upload, pick, label) {
  if (!await readyToAdd()) return;
  const name = label || (paths && paths.length ? baseName(paths[0]) + (paths.length > 1 ? ` and ${paths.length - 1} more` : '') : 'the mod');
  const before = modsSnapshot();
  const t = toastProgress('Adding ' + name + '…');
  let r = null;
  try { r = await act('add', { paths, upload, pick }); } finally { t.remove(); }
  if (!r) return;
  await showAddResult(r, paths, upload, before, name);
}

async function showAddResult(r, paths, upload, before, name) {
  if (r.skinStudio) {
    await dialog({ title: 'Skin Studio skin', html: '<p>Add this one in ACE8 Skin Studio: <b>Skin slots</b>, then <b>Import</b>. That gives it a slot of its own in the skin list.</p>', buttons: [{ label: 'OK', value: 'ok', kind: 'primary' }] });
    return;
  }
  if (r.choices && r.choices.length) {
    // one group of versions per download, each with its own pick
    const groups = new Map();
    for (const c of r.choices) { const k = c.source || ''; if (!groups.has(k)) groups.set(k, []); groups.get(k).push(c); }
    const many = groups.size > 1;
    let html = many ? '<p>These downloads each have versions that change the same game files. Only one of each can show in the game, so pick one per download:</p>'
      : `<p>This download has ${r.choices.length} versions that change the same game files. Only one can show in the game:</p>`;
    let g = 0;
    for (const [src, cs] of groups) {
      html += `<fieldset class="picks">${many || src ? `<legend>${esc(src || 'Download')}</legend>` : ''}` +
        cs.map((c, i) => `<label class="pick"><input type="radio" name="pick${g}" value="${esc(c.id)}" ${i === 0 ? 'checked' : ''}><span><b>${esc(c.name)}</b><small>${esc((c.files || []).join(', '))}</small></span></label>`).join('') + '</fieldset>';
      g++;
    }
    if (!many && !r.choices.some(c => c.source) && paths && paths.length > 1) html += '<p class="fine">If these versions come from different downloads, add those downloads one at a time.</p>';
    const v = await dialog({ title: 'Which version?', html, buttons: [{ label: 'Cancel', value: 'cancel', kind: 'ghost' }, { label: 'Add', value: 'add', kind: 'primary' }] });
    if (v !== 'add') return;
    const pick = $$('#dlgBody input[type=radio]:checked').map(x => x.value);
    if (!pick.length) return;
    return addPaths(paths, upload, pick, name);
  }
  // what changed, compared with before the add
  const fresh = new Set();
  const now = modsSnapshot();
  for (const [k, v] of now) if (!before.has(k) || before.get(k) !== v) fresh.add(k);
  for (const x of (r.added || []).concat(r.updated || [])) fresh.add(x.replace(/ \(script mod\)$/, ''));
  const added = (r.added || []).map(x => 'Added ' + x), updated = (r.updated || []).map(x => 'Updated ' + x);
  const lines = added.concat(updated);
  const warnings = r.warnings || [];
  const later = currentMode() === 'unmodded' ? 'when you switch back to Modded' : 'the next time you play Modded';
  const freshMods = STATE.pakMods.filter(m => fresh.has(m.key)).concat(STATE.scriptMods.filter(m => fresh.has(m.name)));
  const allOn = freshMods.length > 0 && freshMods.every(m => m.enabled);
  let head;
  if (!lines.length) head = 'Nothing was added.';
  else if (warnings.length) head = lines.length === 1 ? `${lines[0]}, but it won't load yet.` : `${plural(lines.length, 'mod')} added or updated, but some won't load yet.`;
  else if (lines.length === 1) head = added.length ? `${lines[0]}. ${allOn ? 'It\'s on, and loads' : 'It loads'} ${later}.` : `${lines[0]}. The new version loads ${later}.`;
  else head = `${plural(lines.length, 'mod')} added or updated. They load ${later}.`;
  const list = (lines.length > 1 ? lines : []).concat(warnings, r.notes || []);
  if (warnings.length) toast(head, 'warn', { list, sticky: true });
  else toast(head, lines.length ? 'ok' : 'warn', { list });
  if (!lines.length) return;
  newKeys = fresh;
  // the tour keeps its page and focus; the message says what was added, and the Mods page marks it
  if (tour) { renderMods(); return; }
  go('mods');
  renderMods();
  // bring the new mod into view and give it focus
  const first = $$('#pakList .item.new, #scriptList .item.new')[0];
  if (first) {
    if (first.tabIndex >= 0) first.focus({ preventScroll: true });
    first.scrollIntoView({ block: 'center' });
  }
}

// drop files anywhere in the window
let dragDepth = 0, dragWatch = 0;
const hasFiles = e => e.dataTransfer && Array.from(e.dataTransfer.types || []).includes('Files');
function showDrop() { $('#drop').hidden = false; document.body.classList.add('dragging-files'); }
function hideDrop() { dragDepth = 0; clearTimeout(dragWatch); $('#drop').hidden = true; document.body.classList.remove('dragging-files'); }
// while files are dragged over the window, dragover keeps coming; when it stops, the drag has gone
function armDragWatch() { clearTimeout(dragWatch); dragWatch = setTimeout(hideDrop, 1500); }
window.addEventListener('dragenter', e => { if (!hasFiles(e)) return; e.preventDefault(); dragDepth++; showDrop(); armDragWatch(); });
window.addEventListener('dragover', e => { if (!hasFiles(e)) return; e.preventDefault(); e.dataTransfer.dropEffect = 'copy'; showDrop(); armDragWatch(); });
window.addEventListener('dragleave', e => { if (!hasFiles(e)) return; dragDepth = Math.max(0, dragDepth - 1); if (!dragDepth) hideDrop(); });
window.addEventListener('dragend', hideDrop);
$('#drop').addEventListener('click', hideDrop);
window.addEventListener('keydown', e => { if (e.key === 'Escape' && !$('#drop').hidden) hideDrop(); });
window.addEventListener('drop', async e => {
  if (!hasFiles(e)) return;
  e.preventDefault(); hideDrop();
  if (!STATE || !STATE.game.found) { toast('Choose the game folder first.', 'bad'); return; }
  if (gameRunningNow()) { toast(GAME_RUNNING + ' Then drop the mod again.', 'warn'); return; }
  const entries = Array.from(e.dataTransfer.items || []).map(i => i.webkitGetAsEntry && i.webkitGetAsEntry()).filter(Boolean);
  const plain = Array.from(e.dataTransfer.files || []);
  const names = entries.length ? entries.map(en => en.name) : plain.map(f => f.name);
  let files = [];
  if (entries.length) {
    for (const en of entries) files = files.concat(await readEntry(en, ''));
  } else files = plain.map(f => ({ file: f, path: f.name }));
  if (!files.length) { toast('There was nothing to add in what you dropped.', 'warn'); return; }
  dropFiles(files, names[0] + (names.length > 1 ? ` and ${names.length - 1} more` : ''));
});

function readEntry(entry, prefix) {
  return new Promise(resolve => {
    if (entry.isFile) entry.file(f => resolve([{ file: f, path: prefix + entry.name }]), () => resolve([]));
    else if (entry.isDirectory) {
      const reader = entry.createReader(), all = [];
      const next = () => reader.readEntries(async batch => {
        if (!batch.length) {
          let out = [];
          for (const b of all) out = out.concat(await readEntry(b, prefix + entry.name + '/'));
          resolve(out);
        } else { all.push(...batch); next(); }
      }, () => resolve([]));
      next();
    } else resolve([]);
  });
}

// files only the game itself has: a drop with these is the game folder (or a copy), not a mod
const isGameFile = p => /(^|\/)(AceCombat8\.exe|start_protected_game\.exe|global\.utoc|global\.ucas)$/i.test(p);

async function dropFiles(files, label) {
  if (files.some(f => isGameFile(f.path))) {
    toast('That\'s the game\'s own folder. Drop only the mod you downloaded.', 'bad');
    return;
  }
  const total = files.reduce((a, f) => a + f.file.size, 0);
  if (total > 16 * 1073741824) {
    toast(`${label} is ${fmtSize(total)}, too big to drop. Use Add a mod… or Add unzipped folder… on the Mods page instead.`, 'bad');
    return;
  }
  if (total > 4 * 1073741824 || files.length > 5000) {
    const v = await dialog({ title: 'That\'s a lot of files', html: `<p>${esc(label)} is ${fmtSize(total)} in ${plural(files.length, 'file')}, much more than a mod usually is. Did you mean to drop it?</p>`,
      buttons: [{ label: 'Cancel', value: 'cancel', kind: 'ghost' }, { label: 'Add it anyway', value: 'go', kind: 'primary' }] });
    if (v !== 'go') return;
  }
  if (!await readyToAdd()) return;
  uploadFiles(files, label);
}

function uploadFiles(files, label) {
  const total = files.reduce((a, f) => a + f.file.size, 0);
  const xhr = new XMLHttpRequest();
  const t = toastProgress('Adding ' + label + '…', { cancel: () => xhr.abort() });
  const fd = new FormData();
  for (const f of files) { fd.append('path', f.path); fd.append('file', f.file, f.file.name); }
  xhr.open('POST', '/api/upload');
  xhr.setRequestHeader('X-Token', TOKEN);
  xhr.setRequestHeader('X-Client', CLIENT);
  xhr.upload.onprogress = ev => { if (ev.lengthComputable && total > 4e6) t.setText('Adding ' + label + '… ' + Math.round(ev.loaded / ev.total * 100) + '%'); };
  xhr.onload = () => {
    t.remove();
    let r = null; try { r = JSON.parse(xhr.responseText); } catch (e) {}
    if (!r || !r.ok) { toast(r && r.error ? r.error : 'The files could not be received (' + xhr.status + '). Try again, or use Add a mod….', 'bad'); return; }
    addPaths(null, r.result, null, label);
  };
  xhr.onerror = () => { t.remove(); toast('The files could not be received. Try again, or use Add a mod….', 'bad'); };
  xhr.onabort = () => { t.remove(); toast('Stopped. Nothing was added.', 'ok'); };
  xhr.send(fd);
}

// ---------------------------------------------------------------- settings page
function renderSettings() {
  const s = STATE;
  $('#setGamePath').textContent = s.game.found ? s.game.root : 'Not found yet';
  const ea = $('#extraArgs');
  if (document.activeElement !== ea) ea.value = s.settings.extraArgs || '';
  showDirectCmd();
  if (isLinux()) renderLinuxStart();
  const note = $('#steamOptsNote');
  note.innerHTML = '';
  if (s.steamOptions && s.steamOptions.length) {
    note.appendChild(el(`<div class="banner ${s.steamOffline ? 'warn' : ''}">Steam's launch options right now: <code class="cmd">${esc(s.steamOptions[0])}</code>${s.steamOffline ? 'Not needed. Clear them in Steam (right-click ACE COMBAT 8, Properties, General) so Unmodded starts with anti-cheat on.' : 'These only apply when Steam starts the game (Unmodded). For Modded, use the box below.'}</div>`));
  }
  $('#storagePath').textContent = s.game.found ? s.game.storage : '';
  const sn = $('#storageNote');
  sn.classList.toggle('storage-warn', !!(s.game.storageMissing || s.game.storageOtherDrive));
  sn.textContent = !s.game.found ? '' : s.game.storageMissing ? 'This folder can\'t be found. Connect its drive, or choose it again.'
    : s.game.storageOtherDrive ? 'This is on a different drive from the game, so each switch copies the files instead of moving them, and takes longer.'
    : s.game.storageCustom ? 'On the same drive as the game, so switching is instant.' : 'Default: next to the game, on the same drive, so switching is instant.';
  $('#btnDefaultStorage').hidden = !s.game.storageCustom;
  const su = s.setup, m = currentMode();
  const chk = (ok, text, warn) => `<li><span class="mk ${ok ? 'y' : warn ? 'w' : 'n'}" aria-hidden="true">${ok ? '✓' : warn ? '!' : '✗'}</span><span>${text}</span></li>`;
  const items = [];
  if (s.game.found) {
    items.push(chk(su.ue4ss, su.ue4ss ? 'UE4SS installed' : 'UE4SS not installed'));
    items.push(chk(su.loader && su.loaderOn && !su.canUpdate, su.loader ? `Mod Loader ${esc(su.loader)}${su.loaderOn ? '' : ', switched off'}${su.canUpdate ? ' (' + esc(su.bundled) + ' available)' : ''}` : 'Mod Loader not installed', su.loader && su.loaderOn));
    if (!isLinux()) items.push(chk(su.vcRuntime, su.vcRuntime ? 'Microsoft Visual C++ runtime installed' : 'Microsoft Visual C++ runtime not found. Download it, open the file and press Install.'));
    else if (s.linux) items.push(chk(s.linux.set, s.linux.set ? 'Steam launch options set' : 'Steam launch options not set (needed for Modded)', true));
    // the install files are only needed to install or update; once modding is set up the launcher works without them
    if (!su.bundleFound && (!su.ue4ss || !su.loader)) items.push(chk(false, 'To install: copy the Game folder from the Mod Loader download into the ACE COMBAT 8 folder.'));
  }
  $('#setupChecks').innerHTML = items.join('');
  const actions = $('#setupActions'); actions.innerHTML = '';
  if (s.game.found && (su.bundleFound || (su.ue4ss && su.loader && !su.loaderOn))) {
    const label = !su.bundleFound ? 'Switch Mod Loader on' : !su.ue4ss || !su.loader ? 'Set up modding' : su.canUpdate ? 'Update Mod Loader' : 'Repair';
    const b = el(`<button class="btn ${!su.ue4ss || !su.loader || su.canUpdate || !su.loaderOn ? 'primary' : ''}" data-fid="set:setup">${setupRunning ? 'Working…' : label}</button>`);
    b.onclick = () => doSetup();
    if (label === 'Repair') b.title = 'Put back any missing UE4SS or Mod Loader files. Your settings and mods stay.';
    if (m === 'unmodded' || m === 'mixed') { b.disabled = true; actions.appendChild(b); actions.appendChild(el('<span class="fine">Switch to Modded first.</span>')); }
    else { b.disabled = busyLocal; actions.appendChild(b); }
  }
  if (!su.vcRuntime && s.game.found && !isLinux()) {
    const v = el('<button class="btn" data-fid="set:vc">Download from Microsoft</button>');
    v.onclick = () => openLink('https://aka.ms/vs/17/release/vc_redist.x64.exe');
    actions.appendChild(v);
  }
  $('#navSetBadge').hidden = s.game.found && !((su.needsSetup || su.canUpdate) && (m === 'modded' || m === 'none'));
  $('#aboutText').innerHTML = `ACE8 Mod Launcher ${esc(s.version)}${su.bundled ? ', with AC8 Mod Loader ' + esc(su.bundled) : ''}. Uses UE4SS (MIT licence) and the Big Shoulders font (SIL Open Font License).<br>${esc(s.contact)}`;
}

function showDirectCmd() {
  const extra = $('#extraArgs').value.trim();
  $('#cmdDirect').textContent = STATE.directCmd + (extra ? ' ' + extra : '');
}
$('#extraArgs').oninput = showDirectCmd;
$('#extraArgs').onchange = () => saveSetting({ extraArgs: $('#extraArgs').value.trim() });
async function saveSetting(obj, opts = {}) { if (await act('settings', obj) && !opts.quiet) toast('Saved.'); }

$('#btnPickGame1').onclick = $('#btnPickGame2').onclick = e => withBusy(e.currentTarget, 'pick-game', '', async () => {
  if (STATE && !STATE.hasPicker) {
    const p = await askPath('Game folder', 'Type or paste the ACE COMBAT 8 folder, for example ~/.local/share/Steam/steamapps/common/ACE COMBAT 8');
    if (p) { const r = await act('game', { path: p }); if (r && r !== true) toast('Game folder: ' + r); }
    return;
  }
  const r = await act('pick-game', {});
  if (r && r !== true) toast('Game folder: ' + r);
});
$('#btnUseElsewhere').onclick = e => withBusy(e.currentTarget, 'pick-game', '', async () => {
  const r = await act('game', { path: STATE.game.elsewhere });
  if (r && r !== true) toast('Game folder: ' + r);
});
$('#btnOpenGame').onclick = () => reveal({ what: 'game' });
$('#btnOpenStorage').onclick = () => reveal({ what: 'storage' });

async function pickStorage() {
  if (STATE && !STATE.hasPicker) {
    const p = await askPath('Mod storage folder', 'Type or paste the folder to keep switched-off and removed mods in.');
    if (p) storageMoved(await act('storage', { path: p }));
    return;
  }
  if (inFlight.has('pick-storage')) return;
  inFlight.add('pick-storage');
  const t = toastProgress('Choose a folder in the window that opened…');
  let r = null;
  try { r = await act('pick-storage', {}); } finally { t.remove(); inFlight.delete('pick-storage'); }
  storageMoved(r);
}
async function defaultStorage() {
  const r = await act('storage', { default: true });
  storageMoved(r);
}
function storageMoved(r) {
  if (!r || r === true || !r.path) return;
  if (r.unchanged) { toast('The mod storage folder is already there.'); return; }
  toast('Mod storage folder: ' + r.path, 'ok', { list: [r.moved ? 'Everything in the old folder was moved there.' : 'Nothing needed moving.']
    .concat(r.otherDrive ? ['It is on a different drive from the game, so switching copies files and takes longer.'] : []) });
}
$('#btnPickStorage').onclick = pickStorage;
$('#btnDefaultStorage').onclick = defaultStorage;
$('#btnLauncherLog').onclick = () => reveal({ what: 'launcherlog' });
$('#btnReport').onclick = $('#btnReport2').onclick = async () => {
  let r;
  try { r = await api('report'); } catch (e) { toast(e.message, 'bad'); return; }
  if (!r.ok || typeof r.text !== 'string') { toast(r.error || 'The report could not be made.', 'bad'); return; }
  copyText(r.text, 'Copied. Paste it into a message to @voidcallsreaper.', r.text);
};

async function copyText(text, msg, showIfFails) {
  try { await navigator.clipboard.writeText(text); toast(msg); }
  catch (e) {
    dialog({ title: 'Copy this', wide: true, html: `<p>Select it all (Ctrl+A) and copy it (Ctrl+C).</p><pre>${esc(showIfFails || text)}</pre>`, buttons: [{ label: 'Close', value: 'x', kind: 'primary' }] });
  }
}

// ---------------------------------------------------------------- live updates
let pollTimer = null, pollFails = 0;
const launchPending = st => !!(st.launch && !st.launch.sawGame && !st.launch.gameEnded && !st.launch.gaveUp);
async function poll() {
  clearTimeout(pollTimer);
  try {
    const st = await api('status');
    if (pollFails >= 3) closeToast('gone');
    pollFails = 0;
    const changed = !STATUS || st.fingerprint !== STATUS.fingerprint || st.gameRunning !== STATUS.gameRunning || st.logFresh !== STATUS.logFresh || st.busy !== STATUS.busy;
    const launchChanged = JSON.stringify(st.launch) !== JSON.stringify(STATUS && STATUS.launch);
    STATUS = st;
    if (changed && !busyLocal && !saveTimer && !savingOrder) await loadState();
    else if (launchChanged && STATE) { const f = rememberFocus(); renderPlay(); renderHud(); restoreFocus(f); }
    // a start that is still pending: the bar and the line under it change with time, not only with the launcher's answers
    else if (STATE && launchPending(st)) renderPlayBar();
  } catch (e) {
    pollFails++;
    if (pollFails === 3) toast('The launcher stopped answering. Close this window and start the launcher again.', 'bad', { id: 'gone' });
  }
  pollTimer = setTimeout(poll, document.hidden ? 8000 : 2000);
}
function pollSoon() { clearTimeout(pollTimer); pollTimer = setTimeout(poll, 600); }
document.addEventListener('visibilitychange', () => { if (!document.hidden) pollSoon(); });
window.addEventListener('focus', pollSoon);

// tell the launcher this window is closing; it stops a few seconds after its last window has gone
window.addEventListener('pagehide', () => {
  const url = '/api/bye?t=' + encodeURIComponent(TOKEN) + '&c=' + encodeURIComponent(CLIENT);
  try { if (navigator.sendBeacon && navigator.sendBeacon(url)) return; } catch (e) {}
  try { fetch(url, { method: 'POST', keepalive: true }).catch(() => {}); } catch (e) {}
});
window.addEventListener('pageshow', ev => { if (ev.persisted) api('ping').catch(() => {}); });
setInterval(() => api('ping').catch(() => {}), 20000);

// ---------------------------------------------------------------- first steps
// A short list on the Play page that ticks itself from the state: set up modding, add a mod,
// play Modded. It shows once the tour has been seen (or skipped) until all three are done or it is
// hidden, and stands in for the "Set up modding" card meanwhile, so there is one next thing to do.
const FS_HIDE = 'ace8ml-hide-firststeps';
const setupMissingIn = s => { const m = s.mode.mode, su = s.setup; return m === 'none' || (m === 'modded' && (!su.ue4ss || !su.loader)); };
function firstStepsDone(s) {
  return [!!(s.setup.ue4ss && s.setup.loader), s.pakMods.length + s.scriptMods.length > 0, !!s.run];
}
function firstStepsShown() {
  const s = STATE;
  if (!s || !s.game.found || !(s.settings.welcomeDone || tourSeen) || lsGet(FS_HIDE) === '1') return false;
  if (s.game.storageMissing || s.mode.mode === 'mixed' || s.unfinishedSwitch) return false;
  return firstStepsDone(s).includes(false);
}
function renderFirstSteps() {
  const box = $('#firstSteps');
  box.hidden = !firstStepsShown();
  if (box.hidden) return;
  const s = STATE, su = s.setup, m = currentMode(), st = STATUS || s.status;
  const done = firstStepsDone(s), now = done.indexOf(false), total = s.pakMods.length + s.scriptMods.length;
  const lineMissing = isLinux() && s.linux && !s.linux.set;
  const steps = [
    { title: 'Set up modding',
      text: done[0] ? 'UE4SS and the Mod Loader are set up.'
        : su.bundleFound ? 'Adds what mods need (UE4SS and the Mod Loader) to the game folder. The game\'s own files aren\'t changed.'
          : 'Copy the Game folder from the Mod Loader download into the ACE COMBAT 8 folder. The launcher notices by itself.',
      btn: su.bundleFound
        ? { label: 'Set up modding', busy: setupRunning && 'Setting up…', off: busyLocal && !setupRunning && 'One moment.', fn: () => doSetup() }
        : { label: 'Open the game folder', fn: () => reveal({ what: 'game' }) } },
    { title: 'Add a mod',
      text: done[1] ? 'You have ' + plural(total, 'mod') + '.' : 'Drop a mod\'s .zip onto this window, or press Add a mod…',
      btn: { label: 'Add a mod…', fn: () => { go('mods'); chooseAndAdd(false); } } },
    { title: 'Play Modded',
      text: 'Pick Modded and press Play modded. Offline only: for online play, pick Unmodded.' + (lineMissing ? ' Steam needs the launcher\'s line first (One step in Steam).' : ''),
      btn: m === 'unmodded'
        ? { label: 'Switch to Modded', off: $('#optModded').getAttribute('aria-disabled') === 'true' && 'One moment.', fn: () => doSwitch('modded') }
        : { label: 'Play modded', off: (!done[0] && 'Set up modding first.') || (m !== 'modded' && 'Pick Modded first.') || ($('#btnPlay').disabled && (st.gameRunning ? 'The game is running.' : 'One moment.')), fn: () => $('#btnPlay').click() } },
  ];
  $('#fsList').replaceChildren(...steps.map((x, i) => {
    const li = el(`<li class="fs-step ${done[i] ? 'done' : i === now ? 'now' : 'later'}" data-step="${i + 1}">
      <span class="fs-lamp" aria-hidden="true"></span><span class="fs-n" aria-hidden="true">${i + 1}</span>
      <div class="fs-text"><b>${esc(x.title)}</b><span class="sr-only">${done[i] ? ': done.' : ':'}</span> <span class="fs-desc">${esc(x.text)}</span></div></li>`);
    if (done[i]) { li.appendChild(el('<span class="fs-ok" aria-hidden="true">Done</span>')); return li; }
    // the next step's button is the bright one; for playing, the Play bar itself is
    const b = el(`<button class="btn ${i === now && !(i === 2 && m === 'modded') ? 'primary' : ''}" data-fid="fs:${i + 1}">${esc(x.btn.busy || x.btn.label)}</button>`);
    if (x.btn.busy) { b.setAttribute('aria-disabled', 'true'); b.classList.add('busy'); }
    if (x.btn.off) { b.disabled = true; b.title = x.btn.off; }
    b.onclick = () => { if (b.getAttribute('aria-disabled') !== 'true') x.btn.fn(); };
    li.appendChild(b);
    return li;
  }));
}
$('#fsHide').onclick = () => {
  lsSet(FS_HIDE, '1');
  renderPlay();
  toast('First steps hidden. Help, Quick start, has the same steps.');
};

// ---------------------------------------------------------------- the tour
// A short walk through the window, one thing at a time. It starts by itself once, the first time
// the game is found (settings.welcomeDone), and again from Help or the rail. The steps are picked
// from the live state; each points at one thing, which stays usable while the rest is dimmed.
const tourTodo = name => () => $$('#todoTop .card, #todo .card').find(c => c.dataset.card === name);
const steamLineMissing = s => s.platform === 'linux' && !!s.linux && !s.linux.set;
const TOUR = [
  { id: 'mode', page: 'play', anchor: () => $('#modeSwitch'), title: 'Modded or Unmodded',
    body: () => `<p><b>Modded</b> starts the game with your mods. Anti-cheat (the game's protection for online play) is off, so it's offline and single player only.</p>
      <p><b>Unmodded</b> is the normal game with anti-cheat on. Pick it to play online.</p>
      <p class="fine">Switching only moves the mod files out of the game or back in. Nothing is deleted.</p>` },
  { id: 'setup', page: 'play', when: setupMissingIn, title: 'Set up modding (once)',
    anchor: () => {
      const row = $('#firstSteps:not([hidden]) .fs-step[data-step="1"]'), c = !row && tourTodo('setup')();
      return row || (c && [c, c.querySelector('.row')]);
    },
    body: s => s.setup.bundleFound
      ? `<p>Mods need two helpers in the game folder: <b>UE4SS</b>, which runs mods inside the game, and the <b>Mod Loader</b>, which loads them.</p>
        <p>Press <b>Set up modding</b> and the launcher adds both. The game's own files aren't changed.</p>`
      : `<p>Mods need two helpers in the game folder: <b>UE4SS</b> and the <b>Mod Loader</b>. They're in the <b>War's Mod Loader</b> folder that came in the same download as the launcher.</p>
        <p>Copy that folder's <b>Game</b> folder into the ACE COMBAT 8 folder. If it asks, choose to merge the folders. The launcher notices by itself.</p>
        <p class="fine"><b>Open the game folder</b> shows you the ACE COMBAT 8 folder.</p>`,
    // with the install files next to the launcher, the tour can do it there and then
    extra: s => s.setup.bundleFound
      ? { label: 'Set up now', fn: btn => withBusy(btn, 'tour-setup', 'Setting up…', () => doSetup({ next: false })) } : null },
  { id: 'add', page: 'mods', anchor: () => $('#btnAddFiles'), title: 'Add a mod',
    body: s => `<p>Mods usually download as a <b>.zip</b> file. Drag it onto this window and let go, anywhere. ${s.hasPicker ? 'Or press <b>Add a mod…</b> and pick it.' : 'Or press <b>Add a mod…</b> and type or paste where it is.'}</p>
      <p>The launcher puts every file where it belongs. If it asks which version you want, pick one.</p>
      <p class="fine">A .7z or .rar file? Unpack it first, then drag in the folder, or press <b>Add unzipped folder…</b>.${setupMissingIn(s) ? ' If modding isn\'t set up yet, the launcher offers to do that first.' : ''}</p>` },
  { id: 'order', page: 'mods', anchor: () => $('#pakList .item') || $('#scriptList .item') || $('#pakEmpty'), title: 'Switch mods on or off',
    body: s => `<p>${s.pakMods.length + s.scriptMods.length ? '' : 'Your mods show up here. '}Each mod has a switch, so you can turn it off without removing it.</p>
      <p>If two mods change the same thing, the lower one in the list wins. Drag a mod down, or use its ▼ button, to make it win.</p>
      <p class="fine">Changes count the next time the game starts.</p>` },
  { id: 'play', page: 'play', title: 'Press Play',
    anchor: s => { const c = steamLineMissing(s) && tourTodo('steam-line')(); return c ? [c, c.querySelector('.row')] : $('#btnPlay'); },
    body: s => s.platform !== 'linux'
      ? `<p>Pick a mode and press <b>Play</b>.</p>
        <p>When Modded, always start the game here, not with Steam's Play button. Steam's button turns anti-cheat on, and the mods won't load.</p>
        <p class="fine">Steam has to be running. The launcher opens it if it isn't.</p>`
      : steamLineMissing(s)
        ? `<p>Pick a mode and press <b>Play</b>. Steam starts the game.</p>
          <p>One thing first, once: for Modded, Steam needs the launcher's line in the game's launch options (a setting in Steam). Close Steam (Steam menu, Exit) and press <b>Set it for me</b>, or press <b>Copy the line</b> and paste it in Steam.</p>
          <p class="fine">Both are also in Settings, Starting the game.</p>`
        : `<p>Pick a mode and press <b>Play</b>. Steam starts the game.</p>
          <p>Steam's own Play button and Game Mode on the Steam Deck start the game the right way too.</p>` },
  { id: 'help', page: 'settings', anchor: () => $('#btnReport'), title: 'If something goes wrong',
    body: () => `<p>After you play Modded, the Play page shows <b>Last game start</b>: each mod as Loaded, Not loaded or Failed, and why.</p>
      <p>Still stuck? Press <b>Copy support report</b> here and paste it in a message to <b>@voidcallsreaper</b> on Discord.</p>
      <p class="fine">Help has the usual fixes, and this tour.</p>` },
];
let tour = null;       // the open tour: {id, from: the page it was opened on, focus}
let tourSeen = false;  // this window has shown the tour: it doesn't start by itself again
const tourList = () => TOUR.filter(t => !t.when || t.when(STATE));

function tourMaybeStart() {
  const s = STATE;
  if (tour || tourSeen || !s || !s.game.found || s.settings.welcomeDone) return;
  // these need the player first; the tour waits until they are sorted out
  if (s.game.storageMissing || s.mode.mode === 'mixed' || s.unfinishedSwitch || $('#dlg').open) return;
  tourStart();
}
function tourStart() {
  if (!STATE || !STATE.game.found || tour) return;
  tourSeen = true;
  const cur = $$('.nav button').find(b => b.getAttribute('aria-current') === 'page');
  tour = { id: null, from: cur ? cur.dataset.page : 'play', focus: document.activeElement };
  $('#tour').hidden = false;
  document.body.classList.add('touring');
  renderPlay(); // the First steps list takes the setup card's place from now on
  tourShow(tourList()[0].id, false);
}
function tourShow(id, anim = true) {
  const list = tourList();
  const i = Math.max(0, list.findIndex(t => t.id === id)), t = list[i];
  const last = i === list.length - 1, ex = t.extra && t.extra(STATE);
  tour.id = t.id;
  go(t.page);
  $('#tourTitle').textContent = t.title;
  tourWords();
  $('#tourBack').hidden = i === 0;
  $('#tourSkip').hidden = last;
  const next = $('#tourNext'), xb = $('#tourExtra');
  next.textContent = last ? 'Done' : ex ? 'Later' : 'Next';
  next.className = 'btn ' + (ex ? 'ghost' : 'primary');
  xb.hidden = !ex;
  if (ex) xb.textContent = ex.label;
  tourScroll(t);
  tourPlace(anim);
  (ex ? xb : next).focus({ preventScroll: true });
}
// the count and the text follow the state (setting up drops a step; adding a mod changes the words)
function tourWords() {
  const list = tourList(), i = list.findIndex(t => t.id === tour.id), count = `Step ${i + 1} of ${list.length}`;
  const body = list[i].body(STATE);
  if ($('#tourCount').textContent !== count) $('#tourCount').textContent = count;
  if (tour.body !== body) { tour.body = body; $('#tourBody').innerHTML = body; }
}
function tourGo(d) {
  if (!tour) return;
  const list = tourList(), i = list.findIndex(t => t.id === tour.id);
  if (i + d >= list.length) { tourEnd(true); return; }
  tourShow(list[Math.max(0, i + d)].id);
}
// Done, Skip and Esc: the tour doesn't start by itself again (a save that fails says so)
function tourEnd(finished) {
  if (!tour) return;
  const { from, focus } = tour;
  tourClose();
  go(finished ? 'play' : from);
  if (STATE && !STATE.settings.welcomeDone) { STATE.settings.welcomeDone = true; act('settings', { welcomeDone: true }); }
  if (STATE && STATE.game.found) renderPlay();
  if (!finished && focus && focus.isConnected && focus !== document.body && !focus.closest('[hidden]')) focus.focus({ preventScroll: true });
}
function tourClose() {
  tour = null;
  $('#tour').hidden = true;
  document.body.classList.remove('touring');
}

const TOUR_PAD = 6, TOUR_GAP = 12, TOUR_EDGE = 12;
// A step can name a smaller part of its target too (a card's buttons): used when the whole of it
// and the tour card don't fit in the window together.
function tourAnchor(t) {
  const list = [].concat(t.anchor(STATE)).filter(a => a && a.isConnected && !a.closest('[hidden]'));
  if (list.length < 2) return list[0] || null;
  const room = $('#scroll').clientHeight - 2 * (TOUR_EDGE + TOUR_PAD) - TOUR_GAP - $('#tourCard').offsetHeight;
  return list.find(a => a.getBoundingClientRect().height <= room) || list[list.length - 1];
}
// Brings the step's target into view with room for the card under it when the window allows.
function tourScroll(t) {
  const a = tourAnchor(t), sc = $('#scroll');
  if (!a || !sc.contains(a)) return;
  const sr = sc.getBoundingClientRect(), r = a.getBoundingClientRect(), ch = $('#tourCard').offsetHeight;
  const pair = r.height + 2 * TOUR_PAD + TOUR_GAP + ch;
  const want = pair <= sr.height - 2 * TOUR_EDGE ? (sr.height - pair) / 2 + TOUR_PAD : TOUR_EDGE + TOUR_PAD;
  sc.scrollTop += r.top - sr.top - want;
}
// The card goes under the target, above it, or beside it; when none of those fit, into the corner
// that covers least of it. The rest of the window is dimmed, with a hole over the target.
let tourAnimTimer = 0;
function tourPlace(anim) {
  if (!tour || !STATE) return;
  if (!STATE.game.found) { tourClose(); return; }
  const list = tourList();
  if (!list.some(t => t.id === tour.id)) {
    // that step got done meanwhile (modding was set up): on to the next one
    const after = TOUR.slice(TOUR.findIndex(t => t.id === tour.id) + 1).find(t => list.includes(t));
    tourShow((after || list[list.length - 1]).id);
    return;
  }
  tourWords();
  const box = $('#tour'), card = $('#tourCard'), ring = $('#tourRing'), shade = $('#tourShade');
  if (anim) { box.classList.add('moving'); clearTimeout(tourAnimTimer); tourAnimTimer = setTimeout(() => box.classList.remove('moving'), 320); }
  const vw = document.documentElement.clientWidth, vh = document.documentElement.clientHeight;
  const cw = card.offsetWidth, ch = card.offsetHeight, E = TOUR_EDGE;
  const a = tourAnchor(TOUR.find(t => t.id === tour.id));
  let h = null;
  if (a) {
    // only the part of the target inside the scrolling area counts
    const r = a.getBoundingClientRect();
    const clip = $('#scroll').contains(a) ? $('#scroll').getBoundingClientRect() : { top: 0, bottom: vh };
    const top = Math.max(r.top, clip.top), bottom = Math.min(r.bottom, clip.bottom);
    if (r.width && bottom - top > 4) h = { l: r.left - TOUR_PAD, t: top - TOUR_PAD, r: r.right + TOUR_PAD, b: bottom + TOUR_PAD };
  }
  ring.hidden = !h;
  if (!h) {
    shade.style.clipPath = '';
    card.style.left = Math.max(E, (vw - cw) / 2) + 'px';
    card.style.top = Math.max(E, (vh - ch) / 2) + 'px';
    return;
  }
  Object.assign(ring.style, { left: h.l + 'px', top: h.t + 'px', width: h.r - h.l + 'px', height: h.b - h.t + 'px' });
  shade.style.clipPath = `polygon(evenodd, 0 0, 100% 0, 100% 100%, 0 100%, 0 0, ${h.l}px ${h.t}px, ${h.l}px ${h.b}px, ${h.r}px ${h.b}px, ${h.r}px ${h.t}px, ${h.l}px ${h.t}px)`;
  const clampX = x => Math.min(Math.max(E, x), vw - E - cw), clampY = y => Math.min(Math.max(E, y), vh - E - ch);
  const fits = ([x, y]) => x >= E - 0.5 && y >= E - 0.5 && x + cw <= vw - E + 0.5 && y + ch <= vh - E + 0.5;
  const area = (o, [x, y]) => o ? Math.max(0, Math.min(x + cw, o.r) - Math.max(x, o.l)) * Math.max(0, Math.min(y + ch, o.b) - Math.max(y, o.t)) : 0;
  // messages in the corner: the card keeps clear of them too when it can
  const rs = $$('#toasts .toast').map(t => t.getBoundingClientRect());
  const msgs = rs.length ? { l: Math.min(...rs.map(r => r.left)), t: Math.min(...rs.map(r => r.top)), r: Math.max(...rs.map(r => r.right)), b: Math.max(...rs.map(r => r.bottom)) } : null;
  const covers = p => area(h, p);
  const corners = [[vw - E - cw, vh - E - ch], [vw - E - cw, E], [E, vh - E - ch], [E, E]];
  const spots = [
    [clampX(h.l), h.b + TOUR_GAP],      // under it
    [clampX(h.l), h.t - TOUR_GAP - ch], // above it
    [h.r + TOUR_GAP, clampY(h.t)],      // right of it
    [h.l - TOUR_GAP - cw, clampY(h.t)], // left of it
  ].concat(corners).filter(fits);
  const at = spots.find(p => !covers(p) && !area(msgs, p)) || spots.find(p => !covers(p))
    || corners.reduce((best, p) => covers(p) < covers(best) ? p : best);
  card.style.left = Math.max(E, at[0]) + 'px';
  card.style.top = Math.max(E, at[1]) + 'px';
  // when it can't keep clear of a message, the card goes over it, so its buttons stay usable
  card.classList.toggle('over-msgs', area(msgs, at) > 0);
}
let tourFrame = 0;
function tourPlaceSoon() {
  if (!tour || tourFrame) return;
  tourFrame = requestAnimationFrame(() => { tourFrame = 0; tourPlace(false); });
}

$('#tourNext').onclick = () => tourGo(1);
$('#tourBack').onclick = () => tourGo(-1);
$('#tourSkip').onclick = () => tourEnd(false);
$('#tourExtra').onclick = () => {
  const t = tour && TOUR.find(x => x.id === tour.id), ex = t && t.extra && t.extra(STATE);
  if (ex && $('#tourExtra').getAttribute('aria-disabled') !== 'true') ex.fn($('#tourExtra'));
};
$('#btnTour').onclick = $('#btnTourRail').onclick = () => {
  if (STATE && !STATE.game.found) { toast('Choose the game folder first. The tour needs it.', 'warn'); go('play'); return; }
  tourStart();
};
document.addEventListener('keydown', e => {
  // a dialog or the drop overlay over the tour handles its own keys
  if (!tour || $('#dlg').open || !$('#drop').hidden) return;
  if (e.key === 'Escape') { e.preventDefault(); tourEnd(false); return; }
  const inCard = $('#tourCard').contains(document.activeElement);
  if (e.key === 'Tab') {
    // keyboard focus stays in the card
    const f = $$('#tourCard button').filter(b => !b.hidden && !b.disabled);
    let i = f.indexOf(document.activeElement);
    if (i < 0) i = e.shiftKey ? 0 : -1;
    e.preventDefault();
    f[(i + (e.shiftKey ? -1 : 1) + f.length) % f.length].focus();
  } else if (inCard && !e.altKey && (e.key === 'ArrowLeft' || e.key === 'ArrowRight')) {
    // arrow keys page through the steps; the last one closes only with Done
    e.preventDefault();
    if (e.key === 'ArrowLeft' && !$('#tourBack').hidden) tourGo(-1);
    else if (e.key === 'ArrowRight' && $('#tourNext').textContent !== 'Done') tourGo(1);
  }
});
addEventListener('resize', () => tourPlace(false));
$('#scroll').addEventListener('scroll', () => tourPlace(false), { passive: true });
if (window.ResizeObserver) new ResizeObserver(tourPlaceSoon).observe($('#tourCard'));
// a new message may sit where the card is
if (window.MutationObserver) new MutationObserver(tourPlaceSoon).observe($('#toasts'), { childList: true });
if (document.fonts && document.fonts.ready) document.fonts.ready.then(tourPlaceSoon);

// start
go(['play', 'mods', 'settings', 'help'].includes(lsGet('ace8ml-page')) ? lsGet('ace8ml-page') : 'play');
loadState().then(() => { if (STATE && !STATE.game.found) go('play'); poll(); });
