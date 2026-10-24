'use strict';
// ACE8 Mod Launcher window.

const $ = s => document.querySelector(s);
const $$ = s => Array.from(document.querySelectorAll(s));
const TOKEN = new URLSearchParams(location.search).get('t') || '';
const CLIENT = 'w' + Math.random().toString(36).slice(2, 10) + Date.now().toString(36);
let STATE = null;
let STATUS = null;
let busyLocal = false;

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
  try {
    const r = await api(path, body || {});
    if (r.state) { STATE = r.state; STATUS = r.state.status; render(); }
    if (!r.ok) { lastError = r.error; if (!opts.quiet) toast(r.error, 'bad'); return null; }
    return r.result === undefined || r.result === null ? true : r.result;
  } catch (e) { lastError = e.message; toast(e.message, 'bad'); return null; }
}

async function loadState() {
  try { STATE = await api('state'); STATUS = STATE.status; } catch (e) { return; /* the next poll tries again */ }
  render();
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
function el(html) { const t = document.createElement('template'); t.innerHTML = html.trim(); return t.content.firstElementChild; }
function plural(n, one, many) { return n + ' ' + (n === 1 ? one : (many || one + 's')); }
function lsGet(k) { try { return localStorage.getItem(k); } catch (e) { return null; } }
function lsSet(k, v) { try { localStorage.setItem(k, v); } catch (e) {} }

// ---------------------------------------------------------------- toasts & dialogs
function toast(text, kind = 'ok', opts = {}) {
  const t = el(`<div class="toast ${kind}" role="${kind === 'bad' ? 'alert' : 'status'}"><div>${esc(text)}</div></div>`);
  if (opts.list && opts.list.length) t.appendChild(el(`<ul>${opts.list.map(x => `<li>${esc(x)}</li>`).join('')}</ul>`));
  if (opts.action) {
    const b = el(`<button class="link" style="margin-top:6px">${esc(opts.action.label)}</button>`);
    b.onclick = () => { t.remove(); opts.action.fn(); };
    t.appendChild(b);
  }
  $('#toasts').appendChild(t);
  const ms = kind === 'bad' ? 12000 : opts.action ? 10000 : 6000;
  setTimeout(() => t.remove(), ms + (opts.list ? opts.list.length * 1500 : 0));
}

function dialog({ title, html, buttons, wide }) {
  return new Promise(resolve => {
    const d = $('#dlg');
    d.classList.toggle('wide', !!wide);
    $('#dlgTitle').textContent = title;
    $('#dlgBody').innerHTML = html;
    const bar = $('#dlgButtons'); bar.innerHTML = '';
    for (const b of buttons) {
      const btn = el(`<button class="btn ${b.kind || ''}" value="${esc(b.value)}">${esc(b.label)}</button>`);
      bar.appendChild(btn);
    }
    d.onclose = () => resolve(d.returnValue || 'cancel');
    d.returnValue = '';
    d.showModal();
    const first = bar.querySelector('.primary') || bar.querySelector('button');
    if (first) first.focus();
  });
}

// ---------------------------------------------------------------- navigation & theme
function go(page) {
  for (const b of $$('.nav button')) { if (b.dataset.page === page) b.setAttribute('aria-current', 'page'); else b.removeAttribute('aria-current'); }
  for (const p of $$('.page')) p.hidden = p.id !== 'page-' + page;
  $('#scroll').scrollTop = 0;
  lsSet('ace8ml-page', page);
}
for (const b of $$('.nav button')) b.onclick = () => go(b.dataset.page);

function applyTheme(t) {
  if (t === 'light' || t === 'dark') document.documentElement.dataset.theme = t; else delete document.documentElement.dataset.theme;
  for (const b of $$('.seg button')) b.setAttribute('aria-pressed', String(b.dataset.theme === (t || 'system')));
  lsSet('ace8-theme', t === 'system' ? '' : t);
}
for (const b of $$('.seg button')) b.onclick = () => applyTheme(b.dataset.theme);
applyTheme(lsGet('ace8-theme') || 'system');

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
    for (const p of $$('#page-help .path')) if (!p.textContent.includes('~')) p.textContent = p.textContent.replace(/\\/g, '/');
  }
}

function render() {
  if (!STATE) return;
  applyOS();
  $('#contact').textContent = STATE.contact;
  $('#ver').textContent = 'Version ' + STATE.version;
  renderHud();
  renderPlay();
  renderMods();
  renderSettings();
}

function currentMode() { return STATE.mode.mode; }
function modsOnCount() {
  return STATE.pakMods.filter(m => m.enabled).length + STATE.scriptMods.filter(m => m.enabled).length;
}

function renderHud() {
  const s = STATE, st = STATUS || s.status, cells = [];
  const cell = (k, v, cls, go, title) => cells.push(`<button class="cell" ${go ? `data-go="${go}"` : 'tabindex="-1"'} ${title ? `title="${esc(title)}"` : ''}><span class="k">${k}</span><span class="v ${cls || ''}">${esc(v)}</span></button>`);
  if (!s.game.found) { cell('Game', 'Not found', 'bad', 'play'); $('#hud').innerHTML = cells.join(''); return; }
  cell('Game', st.gameRunning ? 'Running' : 'Found', st.gameRunning ? 'good' : '', 'settings', s.game.root);
  const m = currentMode();
  const modeText = { modded: ['Modded', 'hot'], unmodded: ['Unmodded', 'blue'], mixed: ['Half switched', 'bad'], none: ['No mods installed', ''] }[m];
  cell('Mode', modeText[0], modeText[1], 'play');
  if (s.game.found) {
    const l = s.setup.loader;
    cell('Mod Loader', l ? (l === 'unknown' ? 'Installed' : l) + (s.setup.loaderOn ? '' : ' (off)') : 'Not installed', l && s.setup.loaderOn ? '' : 'bad', 'settings');
    const on = s.pakMods.filter(x => x.enabled).length, sc = s.scriptMods.filter(x => x.enabled).length;
    cell('Mods on', on + ' pak' + (sc ? ' + ' + sc + ' script' : ''), '', 'mods');
    if (s.run && m === 'modded') {
      const r = s.run;
      const short = { ok: [r.mounted + ' of ' + r.total + ' loaded', 'good'], problems: [r.mounted + ' of ' + r.total + ' loaded', 'warn'], unsupported: ['Game version not supported', 'bad'], otherloader: ['Two mod loaders', 'bad'], nothing: ['No mods on', ''], waiting: ['Did not finish', 'warn'] }[r.state] || ['', ''];
      cell('Last game start', short[0], short[1], 'last', fmtWhen(r.when));
    }
  }
  $('#hud').innerHTML = cells.join('');
  for (const c of $$('#hud .cell[data-go]')) c.onclick = () => {
    if (c.dataset.go === 'last') { go('play'); $('#lastRun').scrollIntoView({ behavior: 'smooth' }); } else go(c.dataset.go);
  };
}

// ---------------------------------------------------------------- play page
function renderPlay() {
  const s = STATE, st = STATUS || s.status;
  $('#findGame').hidden = s.game.found;
  $('#playBody').hidden = !s.game.found;
  if (!s.game.found) return;
  $('#welcome').hidden = !!s.settings.welcomeDone;

  const m = currentMode();
  const sw = $('#modeSwitch');
  sw.classList.toggle('mixed', m === 'mixed');
  $('#optModded').setAttribute('aria-checked', String(m === 'modded'));
  $('#optUnmodded').setAttribute('aria-checked', String(m === 'unmodded' || m === 'none'));
  const n = modsOnCount();
  $('#descModded').textContent = m === 'none' ? 'Sets up UE4SS and the Mod Loader' : n ? `UE4SS + ${plural(n, 'mod')}` : 'UE4SS, no mods on';
  const note = $('#modeNote');
  if (!busyLocal) {
    note.classList.remove('bad');
    if (st.gameRunning) note.textContent = 'Close the game to switch.';
    else if (m === 'mixed') { note.textContent = 'The last switch didn\'t finish. Pick Modded or Unmodded to finish it.'; note.classList.add('bad'); }
    else if (m === 'unmodded') note.textContent = 'Mods are switched off and kept in the mod storage folder.';
    else note.textContent = '';
  }
  for (const b of $$('.mode-opt')) b.setAttribute('aria-disabled', String(!!(st.gameRunning || busyLocal || st.busy)));

  // play button
  const play = $('#btnPlay');
  const modded = m === 'modded';
  play.classList.toggle('unmodded', !modded);
  $('#playLabel').textContent = st.gameRunning ? 'Game running' : m === 'mixed' ? 'Play' : modded ? 'Play modded' : 'Play unmodded';
  play.disabled = !!(st.gameRunning || st.busy || busyLocal || m === 'mixed');
  const how = modded ? (isLinux() ? 'Starts through Steam with anti-cheat off. Offline and single player only.' : 'Anti-cheat off. Offline and single player only.') : 'Starts through Steam with anti-cheat on.';
  $('#playHow').textContent = how;
  renderLive();
  renderTodo();
  renderLastRun();
}

function renderLive() {
  const s = STATE, st = STATUS || s.status, live = $('#playLive');
  const L = st.launch;
  if (st.steamStartMissed) {
    live.innerHTML = '<span class="bad-text">The game started without the launcher\'s line in Steam\'s launch options, so anti-cheat is on and the mods won\'t load. Close it and see Settings, Starting the game.</span>';
  } else if (st.gameRunning) {
    let t = '<span class="dot"></span>Game running';
    if (L && st.logFresh && s.run && L.mode === 'modded') t += ': ' + esc(s.run.summary);
    live.innerHTML = t;
  } else if (L && !L.sawGame && Date.now() - new Date(L.started) < 90000) {
    live.textContent = L.how === 'steam' ? 'Steam is starting the game…' : 'Starting the game…';
  } else if (L && L.gameEnded) {
    live.textContent = 'Game closed.';
  } else if (L && !L.sawGame) {
    live.textContent = 'The game didn\'t start. Check Steam for a message.';
  } else live.textContent = '';
}

function card(kind, title, html, buttons = []) {
  const c = el(`<div class="card ${kind}"><h3>${esc(title)}</h3>${html}</div>`);
  if (buttons.length) {
    const row = el('<div class="row"></div>');
    for (const b of buttons) { const btn = el(`<button class="btn ${b.kind || ''}">${esc(b.label)}</button>`); btn.onclick = b.fn; row.appendChild(btn); }
    c.appendChild(row);
  }
  return c;
}

const steamSteps = `<p>In Steam: right-click <b>ACE COMBAT 8</b>, <b>Properties</b>, <b>General</b>, then clear <b>Launch options</b>.</p>
  <p class="fine">Steam saves it when it closes, so this can stay until Steam restarts.</p>`;

function renderTodo() {
  const s = STATE, m = currentMode(), box = $('#todo'), cards = [];
  const su = s.setup;
  if (s.game.storageMissing) {
    cards.push(card('bad', 'Mod storage folder not found', `<p><span class="path">${esc(s.game.storage)}</span> can't be found. Connect the drive it is on, or choose the folder again.</p>`,
      [{ label: 'Choose folder…', kind: 'primary', fn: pickStorage }, { label: 'Use the default', fn: defaultStorage }]));
    box.replaceChildren(...cards);
    return;
  }
  if (m === 'mixed' || s.unfinishedSwitch) {
    cards.push(card('bad', 'Finish switching', '<p>Some mod files are in the game and some are in storage.</p>',
      [{ label: 'Make it Modded', fn: () => doSwitch('modded') }, { label: 'Make it Unmodded', fn: () => doSwitch('unmodded') }]));
  }
  if (m === 'none' || (m === 'modded' && (!su.ue4ss || !su.loader))) {
    if (su.bundleFound) cards.push(card('warn', 'Set up modding', `<p>Installs UE4SS and Mod Loader ${esc(su.bundled)}. Game files aren't changed.</p>`,
      [{ label: 'Install', kind: 'primary', fn: doSetup }]));
    else cards.push(card('warn', 'Install UE4SS and the Mod Loader', `<p>Copy the <b>Game</b> folder from the Mod Loader download into the ACE COMBAT 8 folder and let it merge the folders. The launcher notices by itself.</p>`,
      [{ label: 'Open the game folder', kind: 'primary', fn: () => api('reveal', { what: 'game' }) }]));
  } else if (m === 'modded' && !su.loaderOn) {
    cards.push(card('warn', 'Mod Loader is off', '<p>No pak mods will load.</p>', [{ label: 'Switch it on', kind: 'primary', fn: doSetup }]));
  } else if (su.canUpdate && m === 'modded') {
    cards.push(card('info', 'Mod Loader update', `<p>${esc(su.loader)} installed, ${esc(su.bundled)} available. Your mods and load order stay.</p>`, [{ label: 'Update', kind: 'primary', fn: doSetup }]));
  }
  if (m === 'modded' && su.ue4ss && !su.vcRuntime) {
    cards.push(card('warn', 'Microsoft runtime missing', '<p>UE4SS needs the Visual C++ 2015–2022 runtime (x64). Without it the game may not start while Modded.</p>',
      [{ label: 'Download', fn: () => api('open', { url: 'https://aka.ms/vs/17/release/vc_redist.x64.exe' }) }]));
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
    cards.push(card('warn', 'One step in Steam', `<p>For Modded, Steam needs the launcher's line in the game's launch options. You only do this once.</p>${linuxSteps(s.linux)}`, linuxButtons(s.linux, true)));
  }
  const attention = s.pakMods.filter(x => x.enabled && x.notes && x.notes.some(n => n.kind === 'warn'));
  if (attention.length && m !== 'mixed') {
    cards.push(card('warn', plural(attention.length, 'mod needs', 'mods need') + ' a look', `<p>${attention.map(x => esc(x.key)).join(', ')}</p>`, [{ label: 'Open Mods', fn: () => go('mods') }]));
  }
  box.replaceChildren(...cards);
}

function renderLastRun() {
  const s = STATE, r = s.run, box = $('#lastRun');
  box.hidden = !r || currentMode() !== 'modded';
  if (!r) return;
  $('#lastSummary').textContent = (fmtWhen(r.when) ? 'Started ' + fmtWhen(r.when) + '. ' : '') + r.summary;
  const rows = [];
  for (const m of s.pakMods) {
    if (!m.enabled) continue;
    const lr = m.lastRun;
    if (lr && lr.failed) rows.push(`<li><span class="st no">Failed</span><span>${esc(m.key)} <span class="msg">${esc(lr.messages.join(' '))}</span></span></li>`);
    else if (lr && lr.loaded) rows.push(`<li><span class="st ok">Loaded</span><span>${esc(m.key)}</span></li>`);
    else rows.push(`<li><span class="st off">Not loaded</span><span>${esc(m.key)} <span class="msg">${new Date(m.mtime) > new Date(r.when) ? 'Added after that start.' : (m.notes.find(n => n.kind === 'warn') || {}).text || ''}</span></span></li>`);
  }
  $('#lastMods').innerHTML = rows.join('');
}

// ---------------------------------------------------------------- switching & playing
async function doSwitch(to) {
  const st = STATUS || STATE.status;
  if (busyLocal) return;
  if (st.gameRunning) { $('#modeNote').textContent = 'Close the game first, then switch.'; $('#modeNote').classList.add('bad'); return; }
  busyLocal = true;
  const sw = $('#modeSwitch'); sw.classList.add('working');
  $('#modeNote').classList.remove('bad');
  $('#modeNote').textContent = to === 'modded' ? 'Moving your mods into the game…' : 'Moving your mods to storage…';
  renderPlay();
  const r = await act('switch', { to }, { quiet: true });
  busyLocal = false; sw.classList.remove('working');
  if (r) {
    $('#modeNote').textContent = '';
    toast(to === 'modded' ? 'Switched to Modded. Your mods are back in the game.' : 'Switched to Unmodded. Your mods are in the mod storage folder.', 'ok',
      r.aside && r.aside.length ? { list: r.aside.map(a => 'A copy that was in the way is kept as ' + a.split(/[\\/]/).pop()) } : {});
  } else {
    const last = (await api('state').catch(() => null));
    if (last) STATE = last;
    $('#modeNote').classList.add('bad');
    $('#modeNote').textContent = lastError || 'The switch did not work.';
  }
  render();
}

for (const b of $$('.mode-opt')) b.onclick = () => {
  const m = currentMode(), want = b.dataset.mode;
  if (b.getAttribute('aria-disabled') === 'true') {
    const st = STATUS || STATE.status;
    if (st.gameRunning) { $('#modeNote').textContent = 'Close the game first, then switch.'; $('#modeNote').classList.add('bad'); }
    else if (st.busy && !busyLocal) { $('#modeNote').textContent = 'One moment: the launcher is ' + st.busy + '.'; }
    return;
  }
  if (m === want && m !== 'mixed') return;
  if (m === 'none') {
    if (want === 'modded') doSetup();
    else $('#modeNote').textContent = 'Already unmodded: no mod files are installed.';
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
  const b = $('#btnPlay');
  if (isLinux()) return playLinux();
  b.disabled = true;
  const st = STATUS || STATE.status, modded = currentMode() === 'modded';
  $('#playLive').textContent = modded && !st.steamRunning ? 'Opening Steam first…' : 'Starting…';
  // Modded waits for Steam to be signed in before the game starts
  const slow = modded ? setTimeout(() => { $('#playLive').textContent = 'Waiting for Steam to sign in…'; }, 6000) : 0;
  const r = await act('play', {});
  clearTimeout(slow);
  if (!r) { $('#playLive').textContent = ''; }
  else if (r.note) toast(r.note, 'warn');
  pollSoon();
};

// ---------------------------------------------------------------- Linux: Steam starts the game
function linuxSteps(L) {
  return `<p>In Steam: right-click <b>ACE COMBAT 8</b>, <b>Properties</b>, <b>General</b>, <b>Launch Options</b>, and paste this:</p><code class="cmd">${esc(L.suggested)}</code>`
    + (L.stale ? '<p class="fine">Steam has the line from an older place of the launcher\'s script; this one replaces it.</p>' : '');
}
function linuxButtons(L, withHow) {
  return [{ label: 'Copy the line', kind: 'primary', fn: () => copyText(L.suggested, 'Copied. Paste it into Steam\'s launch options for ACE COMBAT 8.') },
    { label: 'Set it for me', fn: setLaunchOptions }]
    .concat(withHow ? [{ label: 'More', kind: 'ghost', fn: () => { go('settings'); document.getElementById('h-start').scrollIntoView({ behavior: 'smooth' }); } }] : []);
}
async function setLaunchOptions() {
  const r = await act('steam-launch-options', {});
  if (r) toast('Steam\'s launch options are set. Start Steam again whenever you like.', 'ok');
}
async function playLinux() {
  const b = $('#btnPlay'), L = STATE.linux, modded = currentMode() === 'modded';
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
  b.disabled = true;
  $('#playLive').textContent = 'Asking Steam to start the game…';
  const r = await act('play', { force });
  if (!r) $('#playLive').textContent = '';
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
    const btn = el(`<button class="btn ${L.set ? '' : bt.kind || ''}">${esc(bt.label)}</button>`);
    btn.onclick = bt.fn; row.appendChild(btn);
  }
  box.appendChild(row);
  if (!L.set) box.appendChild(el('<p class="fine">Set it for me changes Steam\'s settings directly, keeping any launch options you already have. Steam has to be closed for that: Steam menu, Exit. A copy of the old settings is kept next to them.</p>'));
}

// When the computer has no folder chooser (zenity or kdialog), the path is typed in.
function askPath(title, label) {
  const done = dialog({ title, wide: true, html: `<label class="field"><span>${esc(label)}</span><input type="text" id="pathIn" spellcheck="false" autocomplete="off"></label>`,
    buttons: [{ label: 'Cancel', value: 'cancel', kind: 'ghost' }, { label: 'Use this folder', value: 'ok', kind: 'primary' }] });
  const inp = document.getElementById('pathIn');
  inp.focus();
  inp.onkeydown = e => { if (e.key === 'Enter') { e.preventDefault(); $('#dlgButtons .primary').click(); } };
  return done.then(v => { const p = inp.value.trim(); return v === 'ok' && p ? p : null; });
}

async function doSetup() {
  const r = await act('setup', {});
  if (r) toast('Modding is set up.', 'ok', r.kept && r.kept.length ? { list: ['Kept your own: ' + r.kept.map(k => k.split(/[\\/]/).pop()).join(', ')] } : {});
}

$('#btnWelcome').onclick = () => { $('#welcome').hidden = true; saveSetting({ welcomeDone: true }); };
$('#btnShowLog').onclick = $('#btnShowLog2').onclick = showLoaderLog;
async function showLoaderLog() {
  const r = await api('loader-log');
  dialog({ title: 'Mod Loader log', wide: true, html: r.text ? `<pre>${esc(r.text)}</pre>` : '<p>There is no log yet. The Mod Loader writes it each time the game starts while Modded.</p>',
    buttons: [{ label: 'Open the folder', value: 'open', kind: 'ghost' }, { label: 'Close', value: 'close', kind: 'primary' }] })
    .then(v => { if (v === 'open') api('reveal', { what: 'loaderlog' }); });
}

// ---------------------------------------------------------------- mods page
let order = []; // [{key, enabled}] as shown
let selectedKey = null;

function renderMods() {
  const s = STATE;
  const m = currentMode();
  const banner = $('#modsBanner');
  banner.innerHTML = '';
  if (m === 'unmodded') banner.appendChild(el('<div class="banner">Unmodded: your mods are in storage. Changes here count when you switch back to Modded.</div>'));
  if (s.game.found && !s.setup.loader && m !== 'unmodded') banner.appendChild(el('<div class="banner warn">The Mod Loader isn\'t installed yet. Set up modding on the Play page first.</div>'));

  order = s.pakMods.map(x => ({ key: x.key, enabled: x.enabled }));
  const list = $('#pakList');
  list.innerHTML = '';
  $('#pakEmpty').hidden = s.pakMods.length > 0;
  const on = s.pakMods.filter(x => x.enabled).length;
  $('#orderSub').textContent = s.pakMods.length ? `${on} of ${s.pakMods.length} on` : '';
  s.pakMods.forEach((mod, i) => list.appendChild(pakRow(mod, i)));

  const warn = s.pakMods.filter(x => x.enabled && (x.notes || []).some(n => n.kind === 'warn')).length;
  $('#navModsBadge').hidden = !warn; $('#navModsBadge').textContent = warn;

  const sl = $('#scriptList'); sl.innerHTML = '';
  $('#scriptEmpty').hidden = s.scriptMods.length > 0;
  for (const sm of s.scriptMods) sl.appendChild(scriptRow(sm));

  const rb = $('#removedBlock'), rl = $('#removedList');
  rb.hidden = !s.removed.length;
  $('#removedSub').textContent = s.removed.length ? plural(s.removed.length, 'mod') : '';
  rl.innerHTML = '';
  for (const r of s.removed) rl.appendChild(removedRow(r));
}

const gripSVG = '<svg viewBox="0 0 12 16" aria-hidden="true"><circle cx="3" cy="3" r="1.4"/><circle cx="9" cy="3" r="1.4"/><circle cx="3" cy="8" r="1.4"/><circle cx="9" cy="8" r="1.4"/><circle cx="3" cy="13" r="1.4"/><circle cx="9" cy="13" r="1.4"/></svg>';
const lockSVG = '<svg viewBox="0 0 12 16" aria-hidden="true"><path d="M3 7V5a3 3 0 0 1 6 0v2h1v7H2V7zm1.5 0h3V5a1.5 1.5 0 0 0-3 0z"/></svg>';

function pakRow(m, i) {
  const pinned = m.skinList;
  const li = el(`<li class="item ${m.enabled ? '' : 'off'} ${m.key === selectedKey ? 'selected' : ''}" tabindex="0" data-key="${esc(m.key)}" aria-label="${esc(m.key)}, position ${i + 1}"></li>`);
  const handle = el(`<button class="handle ${pinned ? 'pinned' : ''}" title="${pinned ? 'Skin Studio\'s skin list always loads last, so every new skin is in it' : 'Drag to change the order'}" aria-label="${pinned ? 'Always last' : 'Move ' + esc(m.key)}" tabindex="-1">${pinned ? lockSVG : gripSVG}</button>`);
  const tog = el(`<button class="toggle" role="switch" aria-checked="${m.enabled}" aria-label="Load ${esc(m.key)}"></button>`);
  tog.onclick = e => { e.stopPropagation(); setEnabled(m.key, !(tog.getAttribute('aria-checked') === 'true')); };
  const tags = [];
  if (m.skin) tags.push(`<span class="tag skin">Skin Studio skin${m.skin.aircraft && m.skin.aircraft.length ? ': ' + esc(m.skin.aircraft.join(', ')) : ''}</span>`);
  if (pinned) tags.push('<span class="tag skin">Skin Studio skin list</span>');
  if (m.containers.some(c => /_p\.pak$/i.test(c.name))) tags.push('<span class="tag" title="A .pak mod (text, fonts and other files kept in .pak files)">.pak</span>');
  if (m.where === 'tilde') tags.push('<span class="tag" title="In the game\'s Content\\Paks\\~mods folder">~mods</span>');
  if (m.enabled && m.lastRun) tags.push(m.lastRun.failed ? '<span class="tag no">Failed</span>' : '<span class="tag ok">Loaded</span>');
  const files = [].concat(...m.containers.map(c => c.files || []));
  const meta = [];
  if (m.skin && m.skin.title && m.skin.title !== m.name) meta.push(esc(m.skin.title));
  meta.push(fmtSize(m.size));
  if (m.containers.length > 1) meta.push(m.containers.length + ' mod files');
  const when = fmtWhen(m.mtime); if (when) meta.push('Added ' + when);
  const mainCol = el(`<div class="main-col"><div class="name">${esc(m.key.replace(/^~mods\\/, ''))}</div><div class="meta">${tags.join('')}<span>${meta.join('</span><span>')}</span></div></div>`);
  const actions = el(`<div class="actions"></div>`);
  const up = el(`<button class="btn small ghost mv" title="Move up" aria-label="Move ${esc(m.key)} up">▲</button>`);
  const dn = el(`<button class="btn small ghost mv" title="Move down" aria-label="Move ${esc(m.key)} down">▼</button>`);
  up.onclick = e => { e.stopPropagation(); move(m.key, -1); };
  dn.onclick = e => { e.stopPropagation(); move(m.key, 1); };
  if (pinned) { up.disabled = dn.disabled = true; }
  const show = el(`<button class="btn small ghost">Show</button>`);
  show.onclick = e => { e.stopPropagation(); api('reveal', { path: m.path }); };
  const rm = el(`<button class="btn small ghost">Remove</button>`);
  rm.onclick = e => { e.stopPropagation(); removeMod('pak', m.key); };
  actions.append(up, dn, show, rm);
  li.append(handle, tog, mainCol, actions);
  if (m.notes && m.notes.length) li.appendChild(el(`<div class="notes">${m.notes.map(n => `<div class="note ${n.kind}">${esc(n.text)}</div>`).join('')}</div>`));
  if (files.length) li.appendChild(el(`<details class="files"><summary>Changes ${plural(files.length, 'game file')}</summary><ul>${files.map(f => `<li>${esc(f)}</li>`).join('')}</ul></details>`));
  li.onclick = () => { selectedKey = m.key; for (const x of $$('#pakList .item')) x.classList.toggle('selected', x.dataset.key === selectedKey); };
  li.onkeydown = e => {
    if (e.altKey && (e.key === 'ArrowUp' || e.key === 'ArrowDown')) { e.preventDefault(); selectedKey = m.key; move(m.key, e.key === 'ArrowUp' ? -1 : 1); }
    else if (e.key === ' ' && e.target === li) { e.preventDefault(); tog.click(); }
    else if ((e.key === 'ArrowUp' || e.key === 'ArrowDown') && e.target === li) { e.preventDefault(); const sib = e.key === 'ArrowUp' ? li.previousElementSibling : li.nextElementSibling; if (sib) sib.focus(); }
  };
  if (!pinned) wireDrag(handle, li);
  return li;
}

function lastMovable() {
  let n = order.length;
  while (n > 0 && STATE.pakMods.find(x => x.key === order[n - 1].key)?.skinList) n--;
  return n - 1;
}

function applyOrder(newOrder) {
  // the skin list stays last
  const pins = newOrder.filter(o => STATE.pakMods.find(x => x.key === o.key)?.skinList);
  order = newOrder.filter(o => !pins.includes(o)).concat(pins);
  const byKey = Object.fromEntries(STATE.pakMods.map(x => [x.key, x]));
  STATE.pakMods = order.map(o => Object.assign(byKey[o.key], { enabled: o.enabled }));
  renderMods();
  const focusEl = $$('#pakList .item').find(x => x.dataset.key === selectedKey);
  if (focusEl) focusEl.focus();
  saveOrder();
}

function move(key, d) {
  const i = order.findIndex(o => o.key === key), j = i + d;
  if (i < 0 || j < 0 || j > lastMovable()) return;
  const o = order.slice(); [o[i], o[j]] = [o[j], o[i]];
  selectedKey = key;
  applyOrder(o);
}

function setEnabled(key, on) {
  const o = order.map(x => x.key === key ? { key, enabled: on } : x);
  applyOrder(o);
}

let saveTimer = null;
function saveOrder() {
  clearTimeout(saveTimer);
  saveTimer = setTimeout(async () => {
    saveTimer = null;
    const r = await act('order', { mods: order });
    if (r) toast((STATUS || STATE.status).gameRunning ? 'Saved. Restart the game to use the new order.' : 'Saved. It applies the next time the game starts.');
  }, 450);
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
      let j = o.findIndex(x => x.key === tkey) + (after ? 1 : 0);
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
  const li = el(`<li class="item ${sm.enabled ? '' : 'off'}"></li>`);
  const tog = el(`<button class="toggle" role="switch" aria-checked="${sm.enabled}" aria-label="Load ${esc(sm.name)}"></button>`);
  tog.onclick = async () => {
    tog.disabled = true;
    const r = await act('script', { name: sm.name, enabled: !sm.enabled });
    if (r) toast(`${sm.name} is ${!sm.enabled ? 'on' : 'off'}. It applies the next time the game starts.`);
  };
  const main = el(`<div class="main-col"><div class="name">${esc(sm.name)}</div><div class="meta"><span class="tag">${esc(sm.kind)}</span>${sm.description ? `<span>${esc(sm.description)}</span>` : ''}</div></div>`);
  const actions = el('<div class="actions"></div>');
  const show = el('<button class="btn small ghost">Show</button>'); show.onclick = () => api('reveal', { path: sm.path });
  const rm = el('<button class="btn small ghost">Remove</button>'); rm.onclick = () => removeMod('script', sm.name);
  actions.append(show, rm);
  li.append(el('<span></span>'), tog, main, actions);
  return li;
}

function removedRow(r) {
  const li = el(`<li class="item"></li>`);
  const kind = { pak: 'Pak mod', tilde: '~mods', script: 'Script mod', logic: 'LogicMods', modloader: 'Old Mod Loader' }[r.kind] || 'Mod';
  li.appendChild(el(`<div class="main-col"><div class="name">${esc(r.name.replace(/^~mods\\/, ''))}</div><div class="meta"><span class="tag">${esc(kind)}</span><span>${fmtSize(r.size)}</span><span>Removed ${esc(fmtWhen(r.removed))}</span></div></div>`));
  const actions = el('<div class="actions"></div>');
  if (r.kind !== 'modloader') {
    const rs = el('<button class="btn small">Restore</button>');
    rs.onclick = async () => { if (await act('restore', { id: r.id })) toast(`${r.name} is back.`); };
    actions.appendChild(rs);
  }
  const del = el('<button class="btn small danger">Delete</button>');
  del.onclick = async () => {
    const v = await dialog({ title: 'Delete ' + r.name + '?', html: '<p>Its files are deleted from the mod storage folder. This can\'t be undone.</p>', buttons: [{ label: 'Keep it', value: 'no', kind: 'ghost' }, { label: 'Delete', value: 'yes', kind: 'danger' }] });
    if (v === 'yes' && await act('delete-removed', { id: r.id })) toast(r.name + ' was deleted.');
  };
  actions.appendChild(del);
  li.appendChild(actions);
  return li;
}

async function removeMod(kind, key) {
  const before = order.slice();
  const dir = await act('remove', { kind, key });
  if (!dir) return;
  const id = String(dir).split(/[\\/]/).pop();
  toast(`${key.replace(/^~mods\\/, '')} moved to Removed mods.`, 'ok', { action: { label: 'Undo', fn: async () => {
    if (!await act('restore', { id })) return;
    toast(key.replace(/^~mods\\/, '') + ' is back.');
    // put it back where it was in the load order
    if (kind === 'pak' && before.some(o => o.key === key)) {
      const now = new Set(STATE.pakMods.map(m => m.key));
      const o = before.filter(x => now.has(x.key)).concat(order.filter(x => !before.some(b => b.key === x.key)));
      if (o.length === STATE.pakMods.length) applyOrder(o);
    }
  } } });
}

// ---------------------------------------------------------------- adding mods
$('#btnAddFiles').onclick = async () => {
  const paths = await act('pick-mods', { folder: false });
  if (paths && paths.length) addPaths(paths);
};
$('#btnAddFolder').onclick = async () => {
  const paths = await act('pick-mods', { folder: true });
  if (paths && paths.length) addPaths(paths);
};
$('#btnOpenLoader').onclick = () => api('reveal', { what: 'loader' }).then(r => { if (!r.ok) toast(r.error, 'bad'); });

async function addPaths(paths, upload, pick) {
  const t = toastProgress('Adding…');
  const r = await act('add', { paths, upload, pick });
  t.remove();
  if (!r) return;
  await showAddResult(r, paths, upload);
}

async function showAddResult(r, paths, upload) {
  if (r.skinStudio) {
    await dialog({ title: 'Skin Studio skin', html: '<p>Add this one in ACE8 Skin Studio: <b>Skin slots</b>, then <b>Import</b>. That gives it a slot of its own in the skin list.</p>', buttons: [{ label: 'OK', value: 'ok', kind: 'primary' }] });
    return;
  }
  if (r.choices && r.choices.length) {
    const html = `<p>This download has ${r.choices.length} versions that change the same game files. Only one can show in the game:</p>` +
      r.choices.map((c, i) => `<label class="pick"><input type="radio" name="pick" value="${esc(c.id)}" ${i === 0 ? 'checked' : ''}><span><b>${esc(c.name)}</b><small>${esc(c.files.join(', '))}</small></span></label>`).join('');
    const v = await dialog({ title: 'Which version?', html, buttons: [{ label: 'Cancel', value: 'cancel', kind: 'ghost' }, { label: 'Add', value: 'add', kind: 'primary' }] });
    if (v !== 'add') return;
    const pick = $$('#dlgBody input[name=pick]:checked').map(x => x.value);
    if (!pick.length) return;
    return addPaths(paths, upload, pick);
  }
  const added = (r.added || []).map(x => 'Added ' + x), updated = (r.updated || []).map(x => 'Updated ' + x);
  const lines = added.concat(updated);
  const head = lines.length === 1 ? lines[0] + '.' : lines.length ? plural(lines.length, 'mod') + ' added or updated.' : 'Nothing was added.';
  toast(head, lines.length ? 'ok' : 'warn', { list: (lines.length > 1 ? lines : []).concat(r.notes || []) });
  go('mods');
}

function toastProgress(text) {
  const t = el(`<div class="toast warn" role="status">${esc(text)}</div>`);
  $('#toasts').appendChild(t);
  return t;
}

// drop files anywhere in the window
let dragDepth = 0;
const hasFiles = e => e.dataTransfer && Array.from(e.dataTransfer.types || []).includes('Files');
window.addEventListener('dragenter', e => { if (!hasFiles(e)) return; e.preventDefault(); dragDepth++; $('#drop').hidden = false; });
window.addEventListener('dragover', e => { if (!hasFiles(e)) return; e.preventDefault(); e.dataTransfer.dropEffect = 'copy'; });
window.addEventListener('dragleave', e => { if (!hasFiles(e)) return; dragDepth = Math.max(0, dragDepth - 1); if (!dragDepth) $('#drop').hidden = true; });
window.addEventListener('drop', async e => {
  if (!hasFiles(e)) return;
  e.preventDefault(); dragDepth = 0; $('#drop').hidden = true;
  if (!STATE || !STATE.game.found) { toast('Choose the game folder first.', 'bad'); return; }
  const entries = Array.from(e.dataTransfer.items || []).map(i => i.webkitGetAsEntry && i.webkitGetAsEntry()).filter(Boolean);
  let files = [];
  if (entries.length) {
    for (const en of entries) files = files.concat(await readEntry(en, ''));
  } else files = Array.from(e.dataTransfer.files).map(f => ({ file: f, path: f.name }));
  if (!files.length) return;
  uploadFiles(files);
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

function uploadFiles(files) {
  const total = files.reduce((a, f) => a + f.file.size, 0);
  const t = toastProgress('Receiving ' + plural(files.length, 'file') + '…');
  const fd = new FormData();
  for (const f of files) { fd.append('path', f.path); fd.append('file', f.file, f.file.name); }
  const xhr = new XMLHttpRequest();
  xhr.open('POST', '/api/upload');
  xhr.setRequestHeader('X-Token', TOKEN);
  xhr.setRequestHeader('X-Client', CLIENT);
  xhr.upload.onprogress = ev => { if (ev.lengthComputable && total > 4e6) t.textContent = 'Receiving files… ' + Math.round(ev.loaded / ev.total * 100) + '%'; };
  xhr.onload = () => {
    t.remove();
    let r = null; try { r = JSON.parse(xhr.responseText); } catch (e) {}
    if (!r || !r.ok) { toast(r ? r.error : 'The files could not be received.', 'bad'); return; }
    addPaths(null, r.result);
  };
  xhr.onerror = () => { t.remove(); toast('The files could not be received.', 'bad'); };
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
  const chk = (ok, text, warn) => `<li><span class="mk ${ok ? 'y' : warn ? 'w' : 'n'}">${ok ? '✓' : warn ? '!' : '✗'}</span><span>${text}</span></li>`;
  const items = [];
  if (s.game.found) {
    items.push(chk(su.ue4ss, su.ue4ss ? 'UE4SS installed' : 'UE4SS not installed'));
    items.push(chk(su.loader && su.loaderOn && !su.canUpdate, su.loader ? `Mod Loader ${esc(su.loader)}${su.loaderOn ? '' : ', switched off'}${su.canUpdate ? ' (' + esc(su.bundled) + ' available)' : ''}` : 'Mod Loader not installed', su.loader && su.loaderOn));
    if (!isLinux()) items.push(chk(su.vcRuntime, su.vcRuntime ? 'Microsoft Visual C++ runtime installed' : 'Microsoft Visual C++ runtime not found'));
    else if (s.linux) items.push(chk(s.linux.set, s.linux.set ? 'Steam launch options set' : 'Steam launch options not set (needed for Modded)', true));
    // the install files are only needed to install or update; once modding is set up the launcher works without them
    if (!su.bundleFound && (!su.ue4ss || !su.loader)) items.push(chk(false, 'To install: copy the Game folder from the Mod Loader download into the ACE COMBAT 8 folder.'));
  }
  $('#setupChecks').innerHTML = items.join('');
  const actions = $('#setupActions'); actions.innerHTML = '';
  if (s.game.found && (su.bundleFound || (su.ue4ss && su.loader && !su.loaderOn))) {
    const label = !su.bundleFound ? 'Switch Mod Loader on' : !su.ue4ss || !su.loader ? 'Install' : su.canUpdate ? 'Update Mod Loader' : 'Repair';
    const b = el(`<button class="btn ${!su.ue4ss || !su.loader || su.canUpdate || !su.loaderOn ? 'primary' : ''}">${label}</button>`);
    b.onclick = doSetup;
    if (m === 'unmodded' || m === 'mixed') { b.disabled = true; actions.appendChild(b); actions.appendChild(el('<span class="fine">Switch to Modded first.</span>')); }
    else actions.appendChild(b);
  }
  if (!su.vcRuntime && s.game.found) {
    const v = el('<button class="btn">Download runtime</button>');
    v.onclick = () => api('open', { url: 'https://aka.ms/vs/17/release/vc_redist.x64.exe' });
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
async function saveSetting(obj) { if (await act('settings', obj)) toast('Saved.'); }

$('#btnPickGame1').onclick = $('#btnPickGame2').onclick = async () => {
  if (STATE && !STATE.hasPicker) {
    const p = await askPath('Game folder', 'Type or paste the ACE COMBAT 8 folder, for example ~/.local/share/Steam/steamapps/common/ACE COMBAT 8');
    if (p) { const r = await act('game', { path: p }); if (r && r !== true) toast('Game folder: ' + r); }
    return;
  }
  const r = await act('pick-game', {});
  if (r && r !== true) toast('Game folder: ' + r);
};
$('#btnOpenGame').onclick = () => api('reveal', { what: 'game' });
$('#btnOpenStorage').onclick = () => api('reveal', { what: 'storage' });

async function pickStorage() {
  if (STATE && !STATE.hasPicker) {
    const p = await askPath('Mod storage folder', 'Type or paste the folder to keep switched-off and removed mods in.');
    if (p) storageMoved(await act('storage', { path: p }));
    return;
  }
  const t = toastProgress('Choose a folder in the window that opened…');
  const r = await act('pick-storage', {});
  t.remove();
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
$('#btnLauncherLog').onclick = () => api('reveal', { what: 'launcherlog' });
$('#btnReport').onclick = async () => {
  const r = await api('report');
  copyText(r.text, 'Copied. Paste it into a message to @voidcallsreaper.', r.text);
};

async function copyText(text, msg, showIfFails) {
  try { await navigator.clipboard.writeText(text); toast(msg); }
  catch (e) {
    dialog({ title: 'Copy this', wide: true, html: `<p>Select it all (Ctrl+A) and copy it (Ctrl+C).</p><pre>${esc(showIfFails || text)}</pre>`, buttons: [{ label: 'Close', value: 'x', kind: 'primary' }] });
  }
}

// ---------------------------------------------------------------- live updates
let pollTimer = null;
async function poll() {
  clearTimeout(pollTimer);
  try {
    const st = await api('status');
    const changed = !STATUS || st.fingerprint !== STATUS.fingerprint || st.gameRunning !== STATUS.gameRunning || st.logFresh !== STATUS.logFresh || st.busy !== STATUS.busy;
    const launchChanged = JSON.stringify(st.launch) !== JSON.stringify(STATUS && STATUS.launch);
    STATUS = st;
    if (changed && !busyLocal && !saveTimer) await loadState();
    else if (launchChanged && STATE) { renderPlay(); renderHud(); }
  } catch (e) {}
  pollTimer = setTimeout(poll, document.hidden ? 8000 : 2000);
}
function pollSoon() { clearTimeout(pollTimer); pollTimer = setTimeout(poll, 600); }
document.addEventListener('visibilitychange', () => { if (!document.hidden) pollSoon(); });
window.addEventListener('focus', pollSoon);

// tell the launcher this window is closing; it stops a few seconds after its last window has gone
window.addEventListener('pagehide', () => {
  const url = '/api/bye?t=' + encodeURIComponent(TOKEN) + '&c=' + encodeURIComponent(CLIENT);
  try { if (navigator.sendBeacon && navigator.sendBeacon(url)) return; } catch (e) {}
  try { fetch(url, { method: 'POST', keepalive: true }); } catch (e) {}
});
window.addEventListener('pageshow', ev => { if (ev.persisted) api('ping').catch(() => {}); });
setInterval(() => api('ping').catch(() => {}), 20000);

// start
go(['play', 'mods', 'settings', 'help'].includes(lsGet('ace8ml-page')) ? lsGet('ace8ml-page') : 'play');
loadState().then(() => { if (STATE && !STATE.game.found) go('play'); poll(); });
