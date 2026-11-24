'use strict';
// Enter Space Light settings window. Plain DOM, no framework, no build step.
// Every piece of text from the daemon is set with textContent, never HTML.

let S = null;          // last /api/status
let providers = null;  // cached /api/providers
let editing = false;   // a dialog is open: don't re-render lists under it

// ---------- tiny DOM helper ----------
function h(tag, attrs, ...kids) {
  const el = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs || {})) {
    if (v === null || v === undefined || v === false) continue;
    if (k === 'class') el.className = v;
    else if (k.startsWith('on')) el.addEventListener(k.slice(2), v);
    else if (k === 'value') el.value = v;
    else if (k === 'checked') el.checked = !!v;
    else el.setAttribute(k, v === true ? '' : v);
  }
  for (const kid of kids.flat()) {
    if (kid === null || kid === undefined || kid === false) continue;
    el.append(kid instanceof Node ? kid : document.createTextNode(String(kid)));
  }
  return el;
}
const $ = (id) => document.getElementById(id);
// replaceChildren() would print null/false as text; drop them like h() does.
function fill(el, ...kids) { el.replaceChildren(...kids.flat().filter((k) => k !== null && k !== undefined && k !== false)); }

// ---------- API ----------
async function api(method, path, body) {
  const opt = { method, headers: { 'X-ESL': '1' } };
  if (body !== undefined) { opt.headers['Content-Type'] = 'application/json'; opt.body = JSON.stringify(body); }
  const r = await fetch(path, opt);
  let data = null;
  try { data = await r.json(); } catch { /* empty */ }
  if (r.status === 401 && path === '/api/status') { signedOut(); throw new Error('signed out'); }
  if (!r.ok) throw new Error((data && data.error) || r.statusText);
  return data;
}

function toast(msg, bad) {
  const t = $('toast');
  t.textContent = msg;
  t.className = 'show' + (bad ? ' bad' : '');
  clearTimeout(toast.t);
  toast.t = setTimeout(() => { t.className = ''; }, bad ? 6000 : 2600);
}

async function act(fn, ok) {
  try { const r = await fn(); if (ok) toast(ok); await refresh(); return r; }
  catch (e) { toast(e.message, true); return null; }
}

function signedOut() {
  fill(document.body, h('main', {}, h('div', { class: 'empty' },
    h('strong', {}, 'This window is no longer connected'),
    'Open Enter Space Light again from its tray icon, or run ', h('code', {}, 'esl open'), '.')));
}

// ---------- formatting ----------
function bytes(n) {
  if (!n) return '0 B';
  const u = ['B', 'KB', 'MB', 'GB', 'TB']; let i = 0;
  while (n >= 1024 && i < u.length - 1) { n /= 1024; i++; }
  return (i ? n.toFixed(1) : n) + ' ' + u[i];
}
function ago(t) {
  if (!t) return '';
  const s = (Date.now() - new Date(t).getTime()) / 1000;
  if (s < 60) return 'just now';
  if (s < 3600) return Math.round(s / 60) + ' min ago';
  if (s < 86400) return Math.round(s / 3600) + ' h ago';
  return new Date(t).toLocaleString();
}
function until(t) {
  const s = Math.max(0, (new Date(t).getTime() - Date.now()) / 1000);
  return s < 60 ? Math.round(s) + 's' : Math.round(s / 60) + ' min';
}
const isWin = () => S && S.os === 'windows';
const KIND = { copy: 'Copy', sync: 'Mirror', move: 'Move', bisync: 'Two-way' };
const KIND_HELP = {
  copy: 'Copies new and changed files. Never deletes anything at the destination.',
  sync: 'Makes the destination identical to the source, including deleting files that are no longer in the source.',
  move: 'Copies files, then deletes them from the source.',
  bisync: 'Keeps both sides in step: changes on either side are copied to the other. Conflicts keep the newer file.',
};

// ---------- render ----------
async function refresh() {
  try { S = await api('GET', '/api/status'); } catch { return; }
  document.body.classList.toggle('os-windows', S.os === 'windows');
  document.body.classList.toggle('os-linux', S.os === 'linux');
  renderBanners();
  if (!editing) { renderDrives(); renderSyncs(); renderRemotes(); renderSettings(); }
}

function renderBanners() {
  const b = $('banners'); fill(b);
  if (S.notice) b.append(h('div', { class: 'banner info' }, h('span', {}, S.notice)));
  if (S.winfsp.required && !S.winfsp.installed) {
    b.append(h('div', { class: 'banner warn' },
      h('span', {}, S.winfsp.installing ? 'Installing WinFsp… approve the Windows prompt if one appears.' : 'Drives need WinFsp, the free Windows filesystem driver. Install it once and drives connect automatically.'),
      S.winfsp.installing ? null : h('button', { class: 'primary', onclick: () => act(() => api('POST', '/api/winfsp/install')) }, 'Install WinFsp')));
  }
  if (S.config.locked) {
    const pw = h('input', { type: 'password', placeholder: 'rclone.conf password', autocomplete: 'current-password' });
    const remember = h('input', { type: 'checkbox', checked: true });
    b.append(h('div', { class: 'banner warn' },
      h('span', {}, 'Your rclone.conf is encrypted. Unlock it to connect drives and run syncs.'),
      h('form', { onsubmit: async (e) => { e.preventDefault(); const r = await act(() => api('POST', '/api/unlock', { password: pw.value, remember: remember.checked })); if (r) toast(r.saved ? 'Unlocked and saved to your keychain' : 'Unlocked'); } },
        pw, h('label', { class: 'check' }, remember, 'Remember'), h('button', { class: 'primary' }, 'Unlock'))));
  }
}

function mountDot(st) {
  if (st.status === 'mounted') return st.error ? 'warn' : 'ok';
  if (st.status === 'mounting') return 'busy';
  if (st.status === 'retrying' || st.status === 'locked') return 'bad';
  return '';
}
function mountWhere(m) {
  if (m.state.mountPoint) return m.state.mountPoint;
  if (m.mountPoint) return m.mountPoint;
  return isWin() ? 'next free drive letter' : (S.mountRoot + '/' + m.name);
}

function renderDrives() {
  $('drives-lede').textContent = isWin()
    ? 'Each drive appears in File Explorer like a mapped network drive, and works from any app or terminal.'
    : 'Each drive appears as a folder (in ' + S.mountRoot + ' unless you choose another) that works from any app or terminal.';
  const list = $('drive-list'); fill(list);
  if (!S.mounts.length) {
    list.append(h('div', { class: 'empty' }, h('strong', {}, 'No drives yet'),
      S.config.remotes.length ? 'Add a drive to mount one of your remotes.' : 'First add a remote (your cloud account or server), then add a drive for it.'));
    return;
  }
  for (const m of S.mounts) {
    const st = m.state || {};
    let line = null;
    if (st.status === 'retrying') line = h('div', { class: 'err' }, st.error + (st.nextRetry ? ` — retrying in ${until(st.nextRetry)}` : ''));
    else if (st.status === 'locked') line = h('div', { class: 'warn' }, 'Waiting for rclone.conf to be unlocked');
    else if (st.error) line = h('div', { class: 'warn' }, st.error);
    const connected = st.status === 'mounted';
    list.append(h('div', { class: 'item' },
      h('span', { class: 'dot ' + mountDot(st), title: st.status }),
      h('div', {},
        h('div', { class: 'title' }, m.name, m.readOnly ? h('span', { class: 'badge' }, 'read-only') : null, m.autoMount ? null : h('span', { class: 'badge' }, 'manual')),
        h('div', { class: 'sub' }, m.remote, '  →  ', mountWhere(m)),
        line),
      h('div', { class: 'actions' },
        connected ? h('button', { onclick: () => act(() => api('POST', `/api/mounts/${m.id}/open`)) }, 'Open') : null,
        connected || st.status === 'mounting' || st.want
          ? h('button', { onclick: () => act(() => api('POST', `/api/mounts/${m.id}/disconnect`), 'Disconnected') }, st.status === 'retrying' ? 'Stop trying' : 'Disconnect')
          : h('button', { class: 'primary', onclick: () => act(() => api('POST', `/api/mounts/${m.id}/connect`)) }, 'Connect'),
        h('button', { class: 'link', onclick: () => driveDialog(m) }, 'Edit'))));
  }
}

function jobStatusLine(j) {
  const st = j.state || {};
  const r = st.running;
  if (r) {
    if (r.waitingUntil) return h('div', { class: 'warn' }, `Attempt ${r.attempt} failed — retrying in ${until(r.waitingUntil)}`);
    const s = r.stats || {};
    const pct = s.totalBytes ? Math.min(100, Math.round(100 * s.bytes / s.totalBytes)) : 0;
    const bar = h('div', { class: 'progress' }, h('i'));
    bar.firstChild.style.width = pct + '%';
    return h('div', {}, h('div', { class: 'sub' }, `Running (${r.reason})`, s.totalBytes ? ` — ${bytes(s.bytes)} of ${bytes(s.totalBytes)}` : '', s.speed ? ` at ${bytes(s.speed)}/s` : '', r.attempt > 1 ? ` — attempt ${r.attempt}` : ''), bar);
  }
  if (st.last) {
    const l = st.last;
    if (l.ok) return h('div', { class: 'sub' }, `Last run ${ago(l.finished)} (${l.reason}): ${l.files} file${l.files === 1 ? '' : 's'}, ${bytes(l.bytes)}`);
    return h('div', { class: 'err' }, `Failed ${ago(l.finished)} after ${l.attempts} attempt${l.attempts === 1 ? '' : 's'}: ${l.error}`);
  }
  return h('div', { class: 'sub' }, 'Not run yet');
}

function triggerText(t) {
  const parts = [];
  if (t.onStart) parts.push('at sign-in');
  if (t.every) parts.push('every ' + t.every);
  if (t.daily) parts.push('daily at ' + t.daily);
  if (t.onVolume) parts.push('when “' + (t.onVolume.label || t.onVolume.id) + '” is connected');
  return parts.length ? parts.join(', ') : 'manual only';
}

function renderSyncs() {
  const list = $('sync-list'); fill(list);
  if (!S.jobs.length) {
    list.append(h('div', { class: 'empty' }, h('strong', {}, 'No syncs yet'), 'Back up a folder to the cloud nightly, or copy files to a USB drive whenever it is plugged in.'));
    return;
  }
  for (const j of S.jobs) {
    const st = j.state || {};
    const running = !!st.running;
    const dot = running ? 'busy' : (st.last ? (st.last.ok ? 'ok' : 'bad') : '');
    const needsDrive = (j.source + j.dest).includes('{volume}');
    list.append(h('div', { class: 'item' },
      h('span', { class: 'dot ' + dot }),
      h('div', {},
        h('div', { class: 'title' }, j.name, h('span', { class: 'badge' }, KIND[j.kind] || j.kind), j.enabled ? null : h('span', { class: 'badge' }, 'paused')),
        h('div', { class: 'sub' }, j.source, '  →  ', j.dest),
        h('div', { class: 'sub' }, 'Runs ' + triggerText(j.triggers)),
        jobStatusLine(j)),
      h('div', { class: 'actions' },
        running ? h('button', { onclick: () => act(() => api('POST', `/api/jobs/${j.id}/stop`), 'Stopping') }, 'Stop')
          : h('button', { onclick: () => act(() => api('POST', `/api/jobs/${j.id}/run`), 'Started'), title: needsDrive ? 'The drive must be connected' : '' }, 'Run now'),
        h('button', { class: 'link', onclick: () => syncDialog(j) }, 'Edit'))));
  }
}

function renderRemotes() {
  $('conf-path').textContent = 'Stored in ' + S.config.path + (S.config.encrypted ? ' (encrypted)' : '') + '. The same file the rclone command line uses.';
  $('edit-conf').disabled = S.config.encrypted;
  $('edit-conf').title = S.config.encrypted ? 'Encrypted configs are edited with: esl rclone config' : '';
  const list = $('remote-list'); fill(list);
  if (S.config.locked) { list.append(h('div', { class: 'empty' }, h('strong', {}, 'Locked'), 'Unlock rclone.conf above to see your remotes.')); return; }
  if (!S.config.remotes.length) {
    list.append(h('div', { class: 'empty' }, h('strong', {}, 'No remotes yet'), 'A remote is a cloud account or server: Google Drive, OneDrive, Dropbox, S3, B2, SFTP, SMB, WebDAV and about 70 more.'));
    return;
  }
  for (const r of S.config.remotes) {
    const used = S.mounts.filter((m) => m.remote.split(':')[0] === r.name).length + S.jobs.filter((j) => j.source.split(':')[0] === r.name || j.dest.split(':')[0] === r.name).length;
    list.append(h('div', { class: 'item' },
      h('span', { class: 'dot ok' }),
      h('div', {}, h('div', { class: 'title' }, r.name), h('div', { class: 'sub' }, r.type + (r.backend && r.backend !== r.type ? ' over ' + r.backend : '') + (used ? ` · used by ${used}` : ''))),
      h('div', { class: 'actions' },
        h('button', { onclick: () => driveDialog(null, r.name + ':') }, 'Add drive'),
        h('button', { class: 'link', onclick: () => reconnectRemote(r) }, 'Reconnect'),
        h('button', { class: 'link danger', onclick: () => deleteRemote(r.name, used) }, 'Delete'))));
  }
}

function renderSettings() {
  $('pref-autostart').checked = S.autostart;
  $('pref-mountroot').value = S.prefs.mountRoot || '';
  $('pref-mountroot').placeholder = S.mountRoot || '~/Cloud';
  $('pref-bookmarks').checked = S.prefs.bookmarks !== false;
  $('pref-cache').value = S.prefs.cacheMaxSize || '';
  $('pref-bw').value = S.prefs.bwLimit || '';
  $('pref-log').value = S.prefs.logLevel || '';
  $('about-version').textContent = `Version ${S.version} · rclone ${S.rclone} · ${S.os}/${S.arch}`;
  fill($('winfsp-notice'), S.winfsp.notice + ' — ', h('a', { href: S.winfsp.url, target: '_blank', rel: 'noopener' }, S.winfsp.url));

  const wc = $('winfsp-card');
  wc.classList.toggle('hidden', !S.winfsp.required);
  fill(wc, h('h2', {}, 'WinFsp driver'),
    h('p', {}, S.winfsp.installed ? 'Installed. Drives use it to appear in File Explorer.' : 'Not installed. Drives cannot connect without it.'),
    S.winfsp.installed ? null : h('button', { class: 'primary', disabled: S.winfsp.installing, onclick: () => act(() => api('POST', '/api/winfsp/install')) }, S.winfsp.installing ? 'Installing…' : 'Install WinFsp'));

  const cc = $('conf-card');
  fill(cc, h('h2', {}, 'rclone.conf'), h('p', { class: 'sub' }, S.config.path),
    S.config.encrypted ? h('p', {}, S.config.passwordSaved ? 'Encrypted. The password is saved in your system keychain, so it unlocks at sign-in.' : 'Encrypted. You will be asked for the password each time Enter Space Light starts.') : h('p', {}, 'Not encrypted. Use ', h('code', {}, 'esl rclone config'), ' and choose “Set configuration password” to encrypt it.'),
    S.config.encrypted && S.config.passwordSaved ? h('button', { onclick: () => act(() => api('POST', '/api/forget-password'), 'Password removed from keychain') }, 'Forget saved password') : null);
}

// ---------- dialog plumbing ----------
function openDialog(title, body, buttons) {
  const dlg = $('dlg'); const form = $('dlg-form');
  const err = h('p', { class: 'error' });
  fill(form,
    h('div', { class: 'dhead' }, h('h2', {}, title), h('button', { type: 'button', class: 'link', onclick: () => closeDialog() }, 'Close')),
    ...[].concat(body), err,
    h('div', { class: 'dfoot' }, ...buttons));
  form.onsubmit = (e) => e.preventDefault();
  editing = true;
  if (!dlg.open) dlg.showModal();
  return { err, form };
}
function closeDialog() { $('dlg').close(); }
$('dlg').addEventListener('close', () => { editing = false; refresh(); });

function field(label, input, hint) {
  return h('label', {}, label, hint ? h('span', { class: 'hint' }, hint) : null, input);
}
function check(label, input) { return h('label', { class: 'check' }, input, label); }

function parseOptions(text) {
  const out = {};
  for (const raw of text.split('\n')) {
    const line = raw.trim();
    if (!line || line.startsWith('#')) continue;
    const i = line.indexOf('=');
    if (i < 0) throw new Error(`Option line “${line}” needs key = value`);
    out[line.slice(0, i).trim().replace(/^--/, '').replace(/-/g, '_')] = line.slice(i + 1).trim();
  }
  return out;
}
const optionsText = (o) => Object.entries(o || {}).map(([k, v]) => `${k} = ${v}`).join('\n');

// Folder picker over /api/ls, rooted at a remote ("gdrive:") or local path.
function pathPicker(target, rootFn) {
  const box = h('div', { class: 'picker hidden' });
  async function show(fsName, path) {
    box.classList.remove('hidden');
    fill(box, h('div', { class: 'sub' }, 'Loading…'));
    try {
      const r = await api('GET', `/api/ls?fs=${encodeURIComponent(fsName)}&path=${encodeURIComponent(path)}`);
      const rows = [];
      if (path) {
        const up = path.split('/').slice(0, -1).join('/');
        rows.push(h('button', { type: 'button', onclick: () => show(fsName, up) }, '⬑ ..'));
      }
      rows.push(h('button', { type: 'button', onclick: () => { target.value = join(fsName, path); box.classList.add('hidden'); } }, '✓ Use ' + (join(fsName, path) || fsName)));
      for (const d of r.dirs) rows.push(h('button', { type: 'button', onclick: () => show(fsName, d.Path) }, '📁 ' + d.Name));
      fill(box, ...rows);
    } catch (e) { fill(box, h('div', { class: 'error' }, e.message)); }
  }
  function join(fsName, p) { return fsName.endsWith(':') ? fsName + p : (p ? fsName.replace(/[\\/]$/, '') + '/' + p : fsName); }
  const btn = h('button', { type: 'button', onclick: () => {
    const root = rootFn();
    if (!root) { toast('Choose a remote first', true); return; }
    const v = target.value;
    const rest = v.startsWith(root) ? v.slice(root.length).replace(/^\/+/, '') : '';
    show(root, rest);
  } }, 'Browse…');
  return { btn, box };
}

// ---------- drive dialog ----------
function driveDialog(m, presetRemote) {
  const isNew = !m;
  m = m || { name: '', remote: presetRemote || '', mountPoint: '', autoMount: true, readOnly: false, options: {} };
  if (!S.config.remotes.length && isNew) { toast('Add a remote first', true); remoteDialog(); return; }
  const remoteName = m.remote.includes(':') ? m.remote.split(':')[0] : '';
  const remoteSel = h('select', {}, S.config.remotes.map((r) => h('option', { value: r.name }, `${r.name} (${r.type})`)));
  remoteSel.value = remoteName || (S.config.remotes[0] && S.config.remotes[0].name) || '';
  const sub = h('input', { value: m.remote.includes(':') ? m.remote.slice(m.remote.indexOf(':') + 1) : '', placeholder: 'Whole remote' });
  const name = h('input', { value: m.name, required: true, placeholder: 'e.g. Google Drive' });
  if (isNew && !m.name) name.value = remoteSel.value;
  remoteSel.addEventListener('change', () => { if (isNew) name.value = remoteSel.value; sub.value = ''; });

  let where;
  if (isWin()) {
    where = h('select', {}, h('option', { value: '' }, 'Automatic (next free letter)'),
      ...'ZYXWVUTSRQPONMLKJIHGFED'.split('').map((l) => h('option', { value: l + ':' }, l + ':')),
      h('option', { value: '__folder' }, 'A folder…'));
    const folder = h('input', { placeholder: 'C:\\Users\\you\\Cloud\\Drive', class: 'hidden' });
    if (m.mountPoint && !/^[A-Za-z]:\\?$/.test(m.mountPoint)) { where.value = '__folder'; folder.value = m.mountPoint; folder.classList.remove('hidden'); }
    else where.value = (m.mountPoint || '').toUpperCase().replace(/\\$/, '');
    where.addEventListener('change', () => folder.classList.toggle('hidden', where.value !== '__folder'));
    where.read = () => (where.value === '__folder' ? folder.value.trim() : where.value);
    where.extra = folder;
  } else {
    where = h('input', { value: m.mountPoint, placeholder: S.mountRoot + '/<name>' });
    where.read = () => where.value.trim();
  }
  const auto = h('input', { type: 'checkbox', checked: m.autoMount });
  const ro = h('input', { type: 'checkbox', checked: m.readOnly });
  const opts = h('textarea', { placeholder: 'vfs_cache_mode = writes\ndir_cache_time = 10m', value: optionsText(m.options) });
  const picker = pathPicker({ get value() { return remoteSel.value + ':' + sub.value; }, set value(v) { sub.value = v.slice(v.indexOf(':') + 1); } }, () => remoteSel.value + ':');

  const save = h('button', { class: 'primary', type: 'submit' }, isNew ? 'Add drive' : 'Save');
  const { err, form } = openDialog(isNew ? 'Add drive' : 'Edit drive', [
    field('Remote', remoteSel),
    h('label', {}, 'Folder on the remote', h('span', { class: 'hint' }, 'Optional: mount just one folder.'), h('div', { class: 'inline' }, sub, picker.btn)), picker.box,
    field('Name', name, isWin() ? 'Shown in File Explorer.' : 'Also the folder name.'),
    field(isWin() ? 'Drive letter' : 'Mount folder', where, isWin() ? null : 'Leave empty for ' + S.mountRoot + '/<name>.'), where.extra || null,
    check('Connect automatically when I sign in', auto),
    check('Read-only', ro),
    h('details', {}, h('summary', {}, 'Advanced rclone options'),
      h('p', { class: 'help' }, 'Automatic tuning picks caching, chunk sizes, parallelism and rate limits for this provider. Anything you set here wins. Use rclone flag names without the leading dashes.'), opts),
  ], [
    isNew ? null : h('button', { type: 'button', class: 'danger', onclick: async () => {
      if (!confirm(`Remove the drive “${m.name}”? Files on the remote are not touched.`)) return;
      if (await act(() => api('DELETE', `/api/mounts/${m.id}`), 'Drive removed')) closeDialog();
    } }, 'Remove'),
    save]);
  form.onsubmit = async (e) => {
    e.preventDefault();
    err.textContent = '';
    let options;
    try { options = parseOptions(opts.value); } catch (x) { err.textContent = x.message; return; }
    const body = { name: name.value.trim(), remote: remoteSel.value + ':' + sub.value.trim().replace(/^\/+/, ''), mountPoint: where.read(), autoMount: auto.checked, readOnly: ro.checked, options };
    save.disabled = true;
    try {
      if (isNew) await api('POST', '/api/mounts', body); else await api('PUT', `/api/mounts/${m.id}`, body);
      closeDialog(); toast(isNew ? 'Drive added' : 'Saved');
    } catch (x) { err.textContent = x.message; } finally { save.disabled = false; }
  };
}

// ---------- sync dialog ----------
function syncDialog(j) {
  const isNew = !j;
  j = j || { name: '', kind: 'copy', enabled: true, source: '', dest: '', triggers: { every: '6h' }, filters: [], options: {} };
  const t = j.triggers || {};
  const name = h('input', { value: j.name, required: true, placeholder: 'e.g. Photos backup' });
  const kind = h('select', {}, Object.entries(KIND).map(([k, v]) => h('option', { value: k }, v)));
  kind.value = j.kind;
  const kindHelp = h('p', { class: 'help' }, KIND_HELP[j.kind]);
  kind.addEventListener('change', () => { kindHelp.textContent = KIND_HELP[kind.value]; });
  const src = h('input', { value: j.source, required: true, placeholder: isWin() ? 'C:\\Users\\you\\Pictures  or  gdrive:Photos' : '/home/you/Pictures  or  gdrive:Photos' });
  const dst = h('input', { value: j.dest, required: true, placeholder: 'b2:backup/photos  or  {volume}/Backup' });
  const remotePick = (input) => {
    const sel = h('select', {}, h('option', { value: '' }, 'Browse a remote…'), S.config.remotes.map((r) => h('option', { value: r.name + ':' }, r.name)));
    const p = pathPicker(input, () => sel.value);
    sel.addEventListener('change', () => { if (sel.value) { input.value = sel.value; p.btn.click(); } });
    return [h('div', { class: 'inline' }, input, sel), p.box];
  };

  const onStart = h('input', { type: 'checkbox', checked: t.onStart });
  const everySel = h('select', {}, [['', 'Off'], ['15m', 'Every 15 minutes'], ['30m', 'Every 30 minutes'], ['1h', 'Every hour'], ['3h', 'Every 3 hours'], ['6h', 'Every 6 hours'], ['12h', 'Every 12 hours'], ['24h', 'Every 24 hours'], ['custom', 'Custom…']].map(([v, l]) => h('option', { value: v }, l)));
  const everyCustom = h('input', { placeholder: 'e.g. 45m or 2h30m', class: 'hidden' });
  if (t.every && ![...everySel.options].some((o) => o.value === t.every)) { everySel.value = 'custom'; everyCustom.value = t.every; everyCustom.classList.remove('hidden'); } else everySel.value = t.every || '';
  everySel.addEventListener('change', () => everyCustom.classList.toggle('hidden', everySel.value !== 'custom'));
  const daily = h('input', { type: 'time', value: t.daily || '' });

  const vols = S.volumes.filter((v) => !v.system);
  const volSel = h('select', {}, h('option', { value: '' }, 'Off'),
    vols.map((v) => h('option', { value: 'id:' + v.id }, `${v.label} (${v.root})${v.removable ? '' : ' — fixed disk'}`)),
    t.onVolume && !vols.some((v) => v.id === t.onVolume.id) ? h('option', { value: 'id:' + (t.onVolume.id || ''), 'data-label': t.onVolume.label || '' }, `${t.onVolume.label || t.onVolume.id} (not connected now)`) : null);
  volSel.value = t.onVolume ? 'id:' + (t.onVolume.id || '') : '';
  const volHint = h('p', { class: 'help' }, 'Plug the drive in to see it here. Use {volume} in the source or destination for wherever the drive appears (its letter or mount folder can change).');
  const insertVol = h('button', { type: 'button', class: 'link', onclick: () => { if (!dst.value.includes('{volume}')) dst.value = '{volume}/' + (dst.value.replace(/^\/+/, '') || 'Backup'); } }, 'Use {volume} as destination');

  const filters = h('textarea', { placeholder: '- *.tmp\n- node_modules/**\n+ /Documents/**\n- **', value: (j.filters || []).join('\n') });
  const enabled = h('input', { type: 'checkbox', checked: j.enabled });
  const opts = h('textarea', { placeholder: 'transfers = 8\nchecksum = true', value: optionsText(j.options) });

  const save = h('button', { class: 'primary', type: 'submit' }, isNew ? 'Add sync' : 'Save');
  const { err, form } = openDialog(isNew ? 'Add sync' : 'Edit sync', [
    field('Name', name),
    field('Kind', kind), kindHelp,
    h('label', {}, 'Source', ...remotePick(src)),
    h('label', {}, 'Destination', ...remotePick(dst)),
    h('fieldset', {}, h('legend', {}, 'When it runs'),
      check('At sign-in', onStart),
      h('div', { class: 'grid2' }, field('Interval', h('div', {}, everySel, everyCustom)), field('Daily at', daily)),
      field('When this drive is connected', volSel), volHint, insertVol),
    h('details', {}, h('summary', {}, 'Filters and advanced options'),
      field('Filter rules', filters, 'One rclone filter rule per line, first match wins: “+ pattern” includes, “- pattern” excludes. A leading / anchors to the sync root; “- **” excludes everything not included above.'),
      field('rclone options', opts, 'Flag names without dashes, one per line: key = value.')),
    check('Enabled', enabled),
  ], [
    isNew ? null : h('button', { type: 'button', class: 'danger', onclick: async () => {
      if (!confirm(`Delete the sync “${j.name}”? No files are deleted.`)) return;
      if (await act(() => api('DELETE', `/api/jobs/${j.id}`), 'Sync deleted')) closeDialog();
    } }, 'Delete'),
    save]);
  form.onsubmit = async (e) => {
    e.preventDefault(); err.textContent = '';
    let options;
    try { options = parseOptions(opts.value); } catch (x) { err.textContent = x.message; return; }
    const triggers = {};
    if (onStart.checked) triggers.onStart = true;
    const ev = everySel.value === 'custom' ? everyCustom.value.trim() : everySel.value;
    if (ev) triggers.every = ev;
    if (daily.value) triggers.daily = daily.value;
    if (volSel.value) {
      const id = volSel.value.slice(3);
      const v = S.volumes.find((x) => x.id === id);
      const opt = volSel.selectedOptions[0];
      triggers.onVolume = { id, label: v ? v.label : (opt.getAttribute('data-label') || '') };
    }
    const body = { name: name.value.trim(), kind: kind.value, enabled: enabled.checked, source: src.value.trim(), dest: dst.value.trim(),
      triggers, filters: filters.value.split('\n').map((s) => s.trim()).filter(Boolean), options };
    if (kind.value === 'sync' && isNew && !confirm('Mirror deletes files at the destination that are not in the source. Continue?')) return;
    save.disabled = true;
    try {
      if (isNew) await api('POST', '/api/jobs', body); else await api('PUT', `/api/jobs/${j.id}`, body);
      closeDialog(); toast(isNew ? 'Sync added' : 'Saved');
    } catch (x) { err.textContent = x.message; } finally { save.disabled = false; }
  };
}

// ---------- remotes ----------
function providerVisible(o, provider) {
  if (!o.provider) return true;
  const neg = o.provider.startsWith('!');
  const list = (neg ? o.provider.slice(1) : o.provider).split(',').map((s) => s.trim());
  const hit = list.includes(provider);
  return neg ? !hit : hit;
}

async function remoteDialog() {
  if (!providers) {
    try { providers = await api('GET', '/api/providers'); } catch (e) { toast(e.message, true); return; }
  }
  const search = h('input', { placeholder: 'Search: drive, s3, sftp, onedrive…', autofocus: true });
  const listBox = h('div', { class: 'providers' });
  const refill = () => {
    const q = search.value.toLowerCase();
    fill(listBox, ...providers.filter((p) => !q || p.name.includes(q) || p.description.toLowerCase().includes(q))
      .map((p) => h('button', { type: 'button', title: p.description, onclick: () => remoteForm(p) }, h('span', { class: 'pdesc' }, p.description), h('small', {}, p.name))));
  };
  search.addEventListener('input', refill);
  refill();
  openDialog('Add remote — choose a provider', [search, listBox], []);
  search.focus();
}

function optionInput(o, value) {
  let input;
  const exclusive = o.examples && o.examples.length && o.exclusive;
  if (o.type === 'bool') {
    input = h('input', { type: 'checkbox', checked: (value || o.default) === 'true' });
    input.read = () => (input.checked ? 'true' : 'false');
  } else if (exclusive) {
    input = h('select', {}, o.required ? null : h('option', { value: '' }, '(default)'), o.examples.map((e) => h('option', { value: e.value }, e.help ? `${e.value} — ${e.help.split('\n')[0]}` : e.value)));
    input.value = value || o.default || '';
    input.read = () => input.value;
  } else {
    const listId = o.examples && o.examples.length ? 'dl-' + o.name + '-' + Math.random().toString(36).slice(2) : null;
    input = h('input', { type: o.isPassword ? 'password' : 'text', value: value || '', placeholder: o.default && o.default !== '<nil>' ? o.default : '', list: listId, autocomplete: 'off' });
    if (listId) input.extraNode = h('datalist', { id: listId }, o.examples.map((e) => h('option', { value: e.value }, e.help)));
    input.read = () => input.value;
  }
  return input;
}

function remoteForm(p) {
  const name = h('input', { required: true, placeholder: 'e.g. gdrive', value: S.config.remotes.some((r) => r.name === p.name) ? '' : p.name });
  const inputs = {};
  const basic = h('div'); const adv = h('div');
  const providerOpt = p.options.find((o) => o.name === 'provider' && !o.advanced);
  const build = () => {
    const prov = inputs.provider ? inputs.provider.read() : '';
    fill(basic); fill(adv);
    for (const o of p.options) {
      if (!providerVisible(o, prov)) continue;
      const prev = inputs[o.name] ? inputs[o.name].read() : '';
      const input = optionInput(o, prev);
      inputs[o.name] = input;
      const help = o.help.split('\n')[0];
      const node = o.type === 'bool'
        ? h('label', { class: 'check', title: o.help }, input, `${o.name} — ${help}`)
        : h('label', { title: o.help }, o.name + (o.required ? ' *' : ''), h('span', { class: 'hint' }, help), input, input.extraNode || null);
      (o.advanced ? adv : basic).append(node);
      if (o === providerOpt) input.addEventListener('change', build);
    }
  };
  build();
  const save = h('button', { class: 'primary', type: 'submit' }, 'Continue');
  const short = p.description.split(/ including | \(|, /)[0].slice(0, 48);
  const { err, form } = openDialog(`New ${short} remote`, [
    p.description.length > short.length ? h('p', { class: 'help clamp' }, p.description) : null,
    field('Remote name', name, 'Short, no spaces; used as name: in paths.'),
    h('p', { class: 'help' }, 'Most fields can stay empty. Providers that sign in through a browser will open one next.'),
    basic, h('details', {}, h('summary', {}, 'Advanced'), adv)], [save]);
  form.onsubmit = async (e) => {
    e.preventDefault(); err.textContent = '';
    const parameters = {};
    for (const o of p.options) {
      const i = inputs[o.name];
      if (!i || !providerVisible(o, inputs.provider ? inputs.provider.read() : '')) continue;
      const v = i.read();
      if (v === '' || (o.type === 'bool' && v === (o.default || 'false'))) continue;
      parameters[o.name] = v;
    }
    save.disabled = true; save.textContent = 'Working…';
    try {
      const out = await api('POST', '/api/remotes/config', { name: name.value.trim(), type: p.name, parameters });
      await configLoop(name.value.trim(), out);
    } catch (x) { err.textContent = x.message; save.disabled = false; save.textContent = 'Continue'; }
  };
}

// rclone asks follow-up questions (sign in, choose a team drive…) one at a time.
async function configLoop(name, out) {
  while (out && out.State) {
    const answer = await askQuestion(name, out);
    if (answer === null) { toast('Setup cancelled; the remote was saved as far as it got', true); closeDialog(); return; }
    out = await api('POST', '/api/remotes/config', { name, state: out.State, result: answer, continue: true });
  }
  closeDialog();
  toast(`Remote “${name}” is ready`);
  if (!S.mounts.some((m) => m.remote.startsWith(name + ':'))) setTimeout(() => driveDialog(null, name + ':'), 300);
}

function askQuestion(name, out) {
  return new Promise((resolve) => {
    const o = out.Option || {};
    const input = optionInput({ name: o.Name, type: o.Type || (typeof o.Default === 'boolean' ? 'bool' : 'string'), examples: (o.Examples || []).map((e) => ({ value: e.Value, help: e.Help })), exclusive: o.Exclusive, required: o.Required, isPassword: o.IsPassword, default: o.Default === undefined || o.Default === null ? '' : String(o.Default) },
      o.Default === undefined || o.Default === null ? '' : String(o.Default));
    const next = h('button', { class: 'primary', type: 'submit' }, 'Continue');
    const isAuth = /token|auth|browser/i.test((o.Name || '') + (o.Help || ''));
    const { err, form } = openDialog(`Set up “${name}”`, [
      out.Error ? h('p', { class: 'error' }, out.Error) : null,
      h('p', { class: 'help' }, o.Help || ''),
      o.Type === 'bool' ? h('label', { class: 'check' }, input, o.Name) : field(o.Name || 'Answer', input),
      input.extraNode || null,
      isAuth ? h('p', { class: 'help' }, 'If a browser window opens, finish signing in there, then come back.') : null,
    ], [h('button', { type: 'button', onclick: () => resolve(null) }, 'Cancel'), next]);
    form.onsubmit = (e) => { e.preventDefault(); err.textContent = ''; next.disabled = true; next.textContent = 'Working…'; resolve(input.read()); };
  });
}

async function reconnectRemote(r) {
  // Re-runs the provider's sign-in questions (refreshes OAuth tokens).
  if (!confirm(`Sign in to “${r.name}” again? Use this if its access has expired.`)) return;
  openDialog(`Reconnect “${r.name}”`, [h('p', {}, 'Working… if a browser window opens, finish signing in there.')], []);
  try {
    const out = await api('POST', '/api/remotes/config', { name: r.name, update: true, parameters: {} });
    await configLoop(r.name, out);
  } catch (e) { closeDialog(); toast(e.message, true); }
}

async function deleteRemote(name, used) {
  if (!confirm(used ? `“${name}” is used by ${used} drive(s)/sync(s), which will stop working. Delete it from rclone.conf anyway?` : `Delete “${name}” from rclone.conf? Files on it are not touched.`)) return;
  await act(() => api('DELETE', `/api/remotes/${encodeURIComponent(name)}${used ? '?force=1' : ''}`), 'Remote deleted');
}

async function confDialog() {
  let conf;
  try { conf = await api('GET', '/api/rclone-conf'); } catch (e) { toast(e.message, true); return; }
  const ta = h('textarea', { class: 'conf', spellcheck: 'false', value: conf.text });
  const save = h('button', { class: 'primary', type: 'submit' }, 'Save');
  const { err, form } = openDialog('Edit rclone.conf', [h('p', { class: 'help' }, conf.path + '\nA backup of the previous version is kept as rclone.conf.bak. If rclone cannot read your changes, nothing is saved.'), ta], [save]);
  form.onsubmit = async (e) => {
    e.preventDefault(); err.textContent = ''; save.disabled = true;
    try { await api('PUT', '/api/rclone-conf', { text: ta.value }); closeDialog(); toast('rclone.conf saved'); }
    catch (x) { err.textContent = x.message; } finally { save.disabled = false; }
  };
}

// ---------- settings / log ----------
$('prefs').addEventListener('submit', async (e) => {
  e.preventDefault();
  const prefs = { mountRoot: $('pref-mountroot').value.trim(), cacheMaxSize: $('pref-cache').value.trim(), bwLimit: $('pref-bw').value.trim(), logLevel: $('pref-log').value };
  if (S.os === 'linux') prefs.bookmarks = $('pref-bookmarks').checked;
  const ok = await act(() => api('PUT', '/api/prefs', prefs));
  if (ok) { $('prefs-saved').textContent = 'Saved'; setTimeout(() => { $('prefs-saved').textContent = ''; }, 2000); }
});
$('pref-autostart').addEventListener('change', (e) => act(() => api('POST', '/api/autostart', { on: e.target.checked }), e.target.checked ? 'Will start at sign-in' : 'Will not start at sign-in'));
$('quit').addEventListener('click', async () => {
  if (!confirm('Quit Enter Space Light? Drives disconnect and scheduled syncs stop until it is opened again.')) return;
  await api('POST', '/api/quit');
  signedOut();
});
async function loadLog() {
  try { const r = await api('GET', '/api/log?kb=96'); const pre = $('log-text'); pre.textContent = r.text || '(empty)'; pre.scrollTop = pre.scrollHeight; } catch (e) { toast(e.message, true); }
}
$('refresh-log').addEventListener('click', loadLog);
$('open-log').addEventListener('click', () => act(() => api('POST', '/api/open-log')));
$('add-drive').addEventListener('click', () => driveDialog(null));
$('add-sync').addEventListener('click', () => syncDialog(null));
$('add-remote').addEventListener('click', () => remoteDialog());
$('edit-conf').addEventListener('click', () => confDialog());

// ---------- tabs ----------
function showTab(name) {
  for (const b of document.querySelectorAll('#tabs button')) b.classList.toggle('on', b.dataset.tab === name);
  for (const s of document.querySelectorAll('.tab')) s.classList.toggle('on', s.id === name);
  if (name === 'log') loadLog();
  try { localStorage.setItem('esl-tab', name); } catch { /* storage may be unavailable */ }
}
for (const b of document.querySelectorAll('#tabs button')) b.addEventListener('click', () => showTab(b.dataset.tab));
try { const t = localStorage.getItem('esl-tab'); if (t && $(t)) showTab(t); } catch { /* ignore */ }

// ---------- live updates ----------
function listen() {
  const es = new EventSource('/api/events');
  es.addEventListener('changed', () => refresh());
  es.addEventListener('quit', () => { es.close(); signedOut(); });
  es.onerror = () => { es.close(); setTimeout(() => refresh().then(listen), 3000); };
}
refresh().then(listen);
setInterval(() => { if (S && (S.jobs.some((j) => j.state && j.state.running) || S.mounts.some((m) => m.state && m.state.nextRetry))) refresh(); }, 5000);
