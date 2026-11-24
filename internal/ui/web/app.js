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
  for (const kid of kids.flat(Infinity)) {
    if (kid === null || kid === undefined || kid === false) continue;
    el.append(kid instanceof Node ? kid : document.createTextNode(String(kid)));
  }
  return el;
}
const $ = (id) => document.getElementById(id);
// replaceChildren() would print null/false as text; drop them like h() does.
function fill(el, ...kids) { el.replaceChildren(...kids.flat(Infinity).filter((k) => k !== null && k !== undefined && k !== false)); }

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
  if ($('portal').classList.contains('on')) renderRestoreStatus();
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
    list.append(h('div', { class: 'empty' }, h('strong', {}, 'Nothing set up yet'), 'Back up your files to the cloud every night, or to a USB drive whenever it is plugged in.',
      h('div', { class: 'row', style: null }, h('button', { class: 'primary', onclick: () => backupWizard() }, 'Set up a backup'))));
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
        h('div', { class: 'title' }, j.name,
          j.backup ? h('span', { class: 'badge' }, 'Backup') : h('span', { class: 'badge' }, KIND[j.kind] || j.kind),
          j.enabled ? null : h('span', { class: 'badge' }, 'paused')),
        h('div', { class: 'sub' }, j.source, '  →  ', j.dest),
        h('div', { class: 'sub' }, 'Runs ' + triggerText(j.triggers) + (j.backup ? ' · ' + historyText(j.backup.retentionDays) : '')),
        st.running && st.running.phase ? h('div', { class: 'sub' }, st.running.phase === 'checking' ? 'Checking it is safe to run…' : 'Recording this backup for Time Portal…') : null,
        jobStatusLine(j),
        j.health ? h('div', { class: 'health ' + (j.health.critical ? 'critical' : 'warning') }, '⚠ ' + j.health.message) : null),
      h('div', { class: 'actions' },
        running ? h('button', { onclick: () => act(() => api('POST', `/api/jobs/${j.id}/stop`), 'Stopping') }, 'Stop')
          : h('button', { onclick: () => act(() => api('POST', `/api/jobs/${j.id}/run`), 'Started'), title: needsDrive ? 'The drive must be connected' : '' }, 'Run now'),
        j.backup ? h('button', { class: 'link', onclick: () => { portalState.sel = 'job:' + j.id; showTab('portal'); } }, 'Time Portal') : null,
        h('button', { class: 'link', onclick: () => syncDialog(j) }, 'Edit'))));
  }
}

function historyText(days) { return days ? `keeps old versions ${days} days` : 'keeps old versions forever'; }

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
$('add-backup').addEventListener('click', () => backupWizard());
$('portal-other').addEventListener('click', () => portalOther());
$('portal-pick').addEventListener('change', (e) => { portalState.sel = e.target.value; portalLoad(); });
$('add-remote').addEventListener('click', () => remoteDialog());
$('edit-conf').addEventListener('click', () => confDialog());

// ---------- tabs ----------
function showTab(name) {
  for (const b of document.querySelectorAll('#tabs button')) b.classList.toggle('on', b.dataset.tab === name);
  for (const s of document.querySelectorAll('.tab')) s.classList.toggle('on', s.id === name);
  if (name === 'log') loadLog();
  if (name === 'portal') portalOpen();
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


// =====================================================================
// Backup wizard
// =====================================================================
async function backupWizard() {
  let setup;
  try { setup = await api('GET', '/api/backup/setup'); } catch (e) { toast(e.message, true); return; }
  if (!setup.remotes.length && !setup.volumes.some((v) => !v.system)) {
    toast('Add a remote (or plug in a USB drive) to back up to first', true);
    return;
  }
  const win = setup.os === 'windows';
  const sep = win ? '\\' : '/';
  const computer = (setup.computer || 'Computer').replace(/[\\/:*?"<>|]/g, '-');
  const w = {
    what: 'home', folders: new Set(setup.folders), extra: '', drive: '',
    where: setup.remotes.length ? 'remote' : 'usb',
    remote: (setup.remotes.find((r) => r.type === 'crypt') || setup.remotes[0] || {}).name || '',
    remotePath: 'Backup/' + computer, usb: '', usbPath: 'Backup/' + computer, folder: '',
    groups: new Set(setup.groups.filter((g) => g.defaultOn).map((g) => g.id)),
    when: 'daily', daily: '02:30', every: '6h', onStart: false, mode: 'sync', days: 30,
    name: '', runNow: true, second: false,
  };
  const STEPS = ['What', 'Where', 'Skip', 'When', 'Review'];
  let step = 0, secondFor = null;

  const radio = (name, value, cur, title, sub, rec, onpick) => {
    const i = h('input', { type: 'radio', name, value, checked: cur === value });
    i.addEventListener('change', () => { onpick(value); render(); });
    return h('label', { class: 'choice' }, i, h('span', {}, h('b', {}, title, rec ? h('span', { class: 'rec' }, 'Recommended') : null), sub ? h('small', {}, sub) : null));
  };
  const stepsBar = () => h('div', { class: 'steps' }, STEPS.map((s, i) => h('span', { class: i === step ? 'on' : '' }, `${i + 1}. ${s}`)));
  const vols = () => setup.volumes.filter((v) => !v.system);

  function sourceSpec() {
    if (w.what === 'drive') {
      const v = setup.volumes.find((x) => x.id === w.drive);
      if (v && v.removable) return { source: '{volume}', volume: { id: v.id, label: v.label }, filters: [], label: v.label, template: 'drive' };
      return { source: v ? v.root : '', filters: [], label: v ? v.label : 'Drive', template: 'drive' };
    }
    if (w.what === 'folders') {
      const picked = [...w.folders];
      if (w.extra.trim()) picked.push(w.extra.trim());
      const underHome = picked.filter((f) => f.startsWith(setup.home + sep));
      if (picked.length === 1 && !underHome.length) return { source: picked[0], filters: [], label: picked[0].split(/[\\/]/).pop(), template: 'folder' };
      if (underHome.length !== picked.length) return { error: 'Folders outside your user folder need their own backup — pick one at a time.' };
      if (!picked.length) return { error: 'Choose at least one folder.' };
      return { source: setup.home, template: 'folder', label: 'Folders',
        filters: picked.map((f) => '+ /' + f.slice(setup.home.length + 1).replace(/\\/g, '/') + '/**').concat(['- **']) };
    }
    return { source: setup.home, filters: [], label: 'Home', template: 'home' };
  }

  function destSpec() {
    if (w.where === 'usb') {
      const v = setup.volumes.find((x) => x.id === w.usb);
      return { dest: '{volume}/' + w.usbPath.replace(/^[\\/]+/, ''), volume: v ? { id: v.id, label: v.label } : null, label: v ? v.label : 'USB drive' };
    }
    if (w.where === 'folder') return { dest: w.folder.trim(), label: w.folder.trim() };
    return { dest: w.remote + ':' + w.remotePath.replace(/^\/+/, ''), label: w.remote };
  }

  function validate() {
    if (step === 0) {
      if (w.what === 'drive' && !w.drive) return 'Choose a drive.';
      const s = sourceSpec(); if (s.error) return s.error;
    }
    if (step === 1) {
      if (w.where === 'remote' && (!w.remote || !w.remotePath.trim())) return 'Choose a remote and a folder on it.';
      if (w.where === 'usb' && !w.usb) return 'Plug in the drive and choose it.';
      if (w.where === 'folder' && !w.folder.trim()) return 'Enter a folder.';
      const s = sourceSpec(), d = destSpec();
      if (w.what === 'drive' && w.where === 'usb' && w.drive === w.usb) return 'A drive can’t be backed up onto itself.';
      if (s.volume && w.where === 'usb') return 'Back up a USB drive to the cloud or a folder, not to another USB drive.';
      if (s.source && d.dest && d.dest.startsWith(s.source + sep)) return 'The backup can’t go inside the folder being backed up.';
    }
    return '';
  }

  function render() {
    let body = [];
    if (step === 0) {
      body = [h('p', { class: 'help' }, secondFor ? 'Second copy of “' + secondFor + '”.' : 'What should be protected?'),
        h('div', { class: 'choices' },
          radio('what', 'home', w.what, 'Everything in my user folder', setup.home + ' — documents, pictures, settings and more. Caches and system files are skipped.', true, (v) => { w.what = v; }),
          radio('what', 'folders', w.what, 'Only some folders', 'Choose Documents, Pictures, or any folder.', false, (v) => { w.what = v; }),
          vols().length ? radio('what', 'drive', w.what, 'A whole drive', 'Back up another drive, such as an external disk.', false, (v) => { w.what = v; }) : null)];
      if (w.what === 'folders') {
        body.push(h('div', { class: 'sub-choices' }, setup.folders.map((f) => {
          const c = h('input', { type: 'checkbox', checked: w.folders.has(f) });
          c.addEventListener('change', () => { c.checked ? w.folders.add(f) : w.folders.delete(f); });
          return h('label', { class: 'check' }, c, f.split(/[\\/]/).pop());
        }), field('Another folder', (() => { const i = h('input', { value: w.extra, placeholder: win ? 'D:\\Projects' : '/srv/projects' }); i.addEventListener('input', () => { w.extra = i.value; }); return i; })())));
      }
      if (w.what === 'drive') {
        body.push(h('div', { class: 'sub-choices' }, vols().map((v) => radio('drive', v.id, w.drive, `${v.label} (${v.root})`, v.removable ? 'Removable' : 'Fixed disk', false, (x) => { w.drive = x; }))));
      }
    } else if (step === 1) {
      const remoteSel = h('select', {}, setup.remotes.slice().sort((a, b) => (b.type === 'crypt') - (a.type === 'crypt')).map((r) => h('option', { value: r.name }, r.name + (r.type === 'crypt' ? ' — encrypted (recommended)' : ` (${r.type})`))));
      remoteSel.value = w.remote; remoteSel.addEventListener('change', () => { w.remote = remoteSel.value; });
      const rp = h('input', { value: w.remotePath }); rp.addEventListener('input', () => { w.remotePath = rp.value; });
      const usbSel = h('select', {}, h('option', { value: '' }, 'Choose a drive…'), vols().filter((v) => v.id !== w.drive).map((v) => h('option', { value: v.id }, `${v.label} (${v.root})`)));
      usbSel.value = w.usb; usbSel.addEventListener('change', () => { w.usb = usbSel.value; });
      const up = h('input', { value: w.usbPath }); up.addEventListener('input', () => { w.usbPath = up.value; });
      const fp = h('input', { value: w.folder, placeholder: win ? '\\\\nas\\backups\\' + computer : '/mnt/nas/backups/' + computer }); fp.addEventListener('input', () => { w.folder = fp.value; });
      body = [h('p', { class: 'help' }, 'Where should the backup go? Keep at least one copy away from this computer.'),
        h('div', { class: 'choices' },
          setup.remotes.length ? radio('where', 'remote', w.where, 'A cloud remote', 'Runs on a schedule. Pick an encrypted (crypt) remote if you have one.', true, (v) => { w.where = v; }) : null,
          vols().length ? radio('where', 'usb', w.where, 'A USB drive', 'Runs every time this drive is plugged in.', false, (v) => { w.where = v; }) : null,
          radio('where', 'folder', w.where, 'A folder or network share', 'Another disk or a NAS share on this computer.', false, (v) => { w.where = v; }))];
      if (w.where === 'remote') body.push(h('div', { class: 'sub-choices' }, field('Remote', remoteSel), field('Folder on the remote', rp, 'Old versions are kept in .previous-versions next to it.')));
      if (w.where === 'usb') body.push(h('div', { class: 'sub-choices' }, field('Drive', usbSel, vols().length ? null : 'Plug the drive in to see it.'), field('Folder on the drive', up)));
      if (w.where === 'folder') body.push(h('div', { class: 'sub-choices' }, field('Folder', fp)));
    } else if (step === 2) {
      body = [h('p', { class: 'help' }, w.what === 'drive' ? 'These apply inside the drive, too.' : 'Skip things that don’t need backing up. They come back on their own or live elsewhere.'),
        h('div', { class: 'choices' }, setup.groups.map((g) => {
          const c = h('input', { type: 'checkbox', checked: w.groups.has(g.id) });
          c.addEventListener('change', () => { c.checked ? w.groups.add(g.id) : w.groups.delete(g.id); });
          return h('label', { class: 'choice' }, c, h('span', {}, h('b', {}, 'Skip ' + g.title.charAt(0).toLowerCase() + g.title.slice(1), g.recommended ? h('span', { class: 'rec' }, 'Recommended') : null), h('small', {}, g.why)));
        }))];
    } else if (step === 3) {
      const usb = w.where === 'usb';
      const t = h('input', { type: 'time', value: w.daily }); t.addEventListener('input', () => { w.daily = t.value; });
      const ev = h('select', {}, [['1h', 'Every hour'], ['3h', 'Every 3 hours'], ['6h', 'Every 6 hours'], ['12h', 'Every 12 hours']].map(([v, l]) => h('option', { value: v }, l))); ev.value = w.every; ev.addEventListener('change', () => { w.every = ev.value; });
      const os = h('input', { type: 'checkbox', checked: w.onStart }); os.addEventListener('change', () => { w.onStart = os.checked; });
      const days = h('select', {}, [[7, '1 week'], [30, '30 days'], [90, '90 days'], [365, '1 year'], [0, 'Forever']].map(([v, l]) => h('option', { value: v }, l))); days.value = String(w.days); days.addEventListener('change', () => { w.days = +days.value; });
      body = [usb || sourceSpec().volume ? h('p', { class: 'help' }, 'This backup runs each time the drive is plugged in.') :
        h('div', {}, h('p', { class: 'help' }, 'When should it run? A run the computer slept through happens as soon as it wakes.'),
          h('div', { class: 'choices' },
            radio('when', 'daily', w.when, 'Every day', null, true, (v) => { w.when = v; }),
            radio('when', 'every', w.when, 'Several times a day', null, false, (v) => { w.when = v; })),
          w.when === 'daily' ? h('div', { class: 'sub-choices' }, field('At', t)) : h('div', { class: 'sub-choices' }, ev),
          h('label', { class: 'check' }, os, 'Also run shortly after I sign in')),
        h('p', { class: 'help' }, 'When a file is deleted from this computer:'),
        h('div', { class: 'choices' },
          radio('mode', 'sync', w.mode, 'Remove it from the backup too, but keep it in Time Portal', 'The backup stays the same size as your files; deleted and old versions are kept for the time below.', true, (v) => { w.mode = v; }),
          radio('mode', 'copy', w.mode, 'Keep it in the backup', 'Nothing is ever removed from the backup. It grows over time.', false, (v) => { w.mode = v; })),
        field('Keep old versions for', days)];
    } else {
      const s = sourceSpec(), d = destSpec();
      if (!w.name) w.name = (secondFor ? secondFor + ' (second copy)' : (s.label === 'Home' ? computer : s.label) + ' to ' + d.label);
      const nm = h('input', { value: w.name }); nm.addEventListener('input', () => { w.name = nm.value; });
      const rn = h('input', { type: 'checkbox', checked: w.runNow }); rn.addEventListener('change', () => { w.runNow = rn.checked; });
      const sc = h('input', { type: 'checkbox', checked: w.second }); sc.addEventListener('change', () => { w.second = sc.checked; });
      body = [field('Name', nm),
        h('dl', { class: 'review' },
          h('dt', {}, 'Backs up'), h('dd', {}, (s.volume ? 'The drive “' + s.volume.label + '” whenever it is plugged in' : s.source) + (s.filters.length ? ' (' + s.filters.filter((f) => f.startsWith('+')).length + ' folders)' : '')),
          h('dt', {}, 'To'), h('dd', {}, d.dest.replace('{volume}', (d.volume ? d.volume.label : 'drive') + ':')),
          h('dt', {}, 'When'), h('dd', {}, w.where === 'usb' || s.volume ? 'When the drive is plugged in' : (w.when === 'daily' ? 'Daily at ' + w.daily : 'Every ' + w.every) + (w.onStart ? ', and after sign-in' : '')),
          h('dt', {}, 'History'), h('dd', {}, historyText(w.days)),
          h('dt', {}, 'Skips'), h('dd', {}, setup.groups.filter((g) => w.groups.has(g.id)).map((g) => g.title.toLowerCase()).join(', ') || 'nothing')),
        h('label', { class: 'check' }, rn, 'Start the first backup now'),
        secondFor ? null : h('label', { class: 'check' }, sc, 'Then set up a second copy somewhere else (recommended: one copy at home, one in the cloud)')];
    }
    const back = step ? h('button', { type: 'button', onclick: () => { step--; render(); } }, 'Back') : null;
    const next = step < 4 ? h('button', { class: 'primary', type: 'button', onclick: () => { const e = validate(); if (e) { toast(e, true); return; } step++; render(); } }, 'Continue')
      : h('button', { class: 'primary', type: 'submit' }, 'Create backup');
    const { form, err: errBox } = openDialog(secondFor ? 'Second copy' : 'Set up a backup', [stepsBar(), ...body], [back, next]);
    errBox.textContent = '';
    form.onsubmit = async (e) => {
      e.preventDefault();
      const s = sourceSpec(), d = destSpec();
      const triggers = {};
      if (w.where === 'usb') triggers.onVolume = d.volume;
      else if (s.volume) triggers.onVolume = s.volume;
      else {
        if (w.when === 'daily') triggers.daily = w.daily; else triggers.every = w.every;
        if (w.onStart) triggers.onStart = true;
      }
      const job = { name: w.name.trim(), kind: w.mode, enabled: true, source: s.source, dest: d.dest, triggers, filters: s.filters,
        backup: { template: s.template, retentionDays: w.days, excludeGroups: [...w.groups] } };
      try {
        const made = await api('POST', '/api/jobs', job);
        if (w.runNow) { try { await api('POST', `/api/jobs/${made.id}/run`); } catch (x) { toast(x.message, true); } }
        toast(w.runNow ? 'Backup created and started' : 'Backup created');
        if (w.second && !secondFor) {
          secondFor = made.name; step = 1; w.name = ''; w.second = false;
          w.where = w.where === 'remote' && vols().length ? 'usb' : (setup.remotes.length ? 'remote' : 'folder');
          render(); return;
        }
        closeDialog();
        showTab('syncs');
      } catch (x) { errBox.textContent = x.message; }
    };
  }
  render();
}

// =====================================================================
// Time Portal
// =====================================================================
const portalState = { sel: '', path: '', at: null, timeline: [], runs: [], selected: new Set(), listing: null, foreign: [] };
const STATE_TEXT = { deletedSince: 'Deleted since', modifiedSince: 'Changed since', unchangedSince: 'Unchanged', prunedUnavailable: 'No longer kept', unknown: 'Unknown' };

function portalQuery() {
  const sel = portalState.sel;
  if (sel.startsWith('job:')) return 'job=' + encodeURIComponent(sel.slice(4));
  const [, fs, id] = sel.split('|');
  return 'fs=' + encodeURIComponent(fs) + '&id=' + encodeURIComponent(id);
}

function portalOpen() {
  const pick = $('portal-pick');
  const own = S.jobs.filter((j) => j.backup);
  fill(pick, own.map((j) => h('option', { value: 'job:' + j.id }, j.name)),
    portalState.foreign.map((r) => h('option', { value: 'ext|' + r.destFs + '|' + r.id }, `${r.name} (on ${r.destFs})`)));
  if (!portalState.sel && pick.options.length) portalState.sel = pick.options[0].value;
  pick.value = portalState.sel;
  pick.classList.toggle('hidden', !pick.options.length);
  if (!portalState.sel) {
    fill($('portal-body'), h('div', { class: 'empty' }, h('strong', {}, 'No backups yet'),
      'Set up a backup and its history appears here. You can also open a backup made by Enter Space on another computer or a Mac.',
      h('div', { class: 'row' }, h('button', { class: 'primary', onclick: () => backupWizard() }, 'Set up a backup'))));
    return;
  }
  portalLoad();
}

async function portalLoad(refresh) {
  const body = $('portal-body');
  fill(body, h('p', { class: 'sub' }, 'Reading backup history…'));
  try {
    const t = await api('GET', '/api/portal/timeline?' + portalQuery() + (refresh ? '&refresh=1' : ''));
    portalState.timeline = t.timeline.map((x) => new Date(x));
    portalState.runs = t.runs;
    portalState.offline = t.offline;
    if (!portalState.at || !portalState.timeline.some((d) => +d === +portalState.at)) portalState.at = portalState.timeline[0];
    portalState.path = ''; portalState.selected.clear();
    await portalList();
  } catch (e) { fill(body, h('div', { class: 'empty' }, h('strong', {}, 'Can’t open this backup'), e.message)); }
}

async function portalList() {
  const body = $('portal-body');
  if (!portalState.timeline.length) { fill(body, h('div', { class: 'empty' }, h('strong', {}, 'No finished backups yet'), 'Once the first backup completes you can browse it here.')); return; }
  let l;
  try { l = await api('GET', `/api/portal/list?${portalQuery()}&at=${Math.floor(+portalState.at / 1000)}&path=${encodeURIComponent(portalState.path)}`); }
  catch (e) { toast(e.message, true); return; }
  portalState.listing = l;
  renderPortal();
}

function renderPortal() {
  const l = portalState.listing; const tl = portalState.timeline;
  const idx = tl.findIndex((d) => +d === +portalState.at);
  const slider = h('input', { type: 'range', min: 0, max: Math.max(0, tl.length - 1), value: tl.length - 1 - idx, 'aria-label': 'Point in time' });
  slider.addEventListener('change', () => { portalState.at = tl[tl.length - 1 - (+slider.value)]; portalState.selected.clear(); portalList(); });
  const step = (d) => { const i = Math.min(tl.length - 1, Math.max(0, idx + d)); portalState.at = tl[i]; portalState.selected.clear(); portalList(); };
  const timebar = h('div', { class: 'timebar card' },
    h('button', { type: 'button', disabled: idx >= tl.length - 1, onclick: () => step(1), title: 'Earlier backup' }, '◀'),
    slider,
    h('button', { type: 'button', disabled: idx <= 0, onclick: () => step(-1), title: 'Later backup' }, '▶'),
    h('span', { class: 'when' }, portalState.at.toLocaleString(undefined, { dateStyle: 'medium', timeStyle: 'short' })),
    h('button', { type: 'button', class: 'link', onclick: () => portalLoad(true) }, 'Refresh'));

  const parts = l.path ? l.path.split('/') : [];
  const crumbs = h('div', { class: 'crumbs' }, h('button', { onclick: () => { portalState.path = ''; portalState.selected.clear(); portalList(); } }, 'Top'),
    parts.map((p, i) => [' / ', h('button', { onclick: () => { portalState.path = parts.slice(0, i + 1).join('/'); portalState.selected.clear(); portalList(); } }, p)]));

  const all = h('input', { type: 'checkbox', 'aria-label': 'Select all' });
  all.addEventListener('change', () => { for (const it of l.items) all.checked ? portalState.selected.add(it.path) : portalState.selected.delete(it.path); renderPortal(); });
  all.checked = l.items.length > 0 && l.items.every((it) => portalState.selected.has(it.path));
  const rows = l.items.map((it) => {
    const c = h('input', { type: 'checkbox', checked: portalState.selected.has(it.path), disabled: it.state === 'prunedUnavailable' && !it.isDir });
    c.addEventListener('change', () => { c.checked ? portalState.selected.add(it.path) : portalState.selected.delete(it.path); renderActionbar(); });
    const name = it.isDir ? h('button', { onclick: () => { portalState.path = it.path; portalState.selected.clear(); portalList(); } }, '📁 ' + it.name) : h('span', {}, it.name);
    return h('tr', {}, h('td', {}, c), h('td', { class: 'name' }, name),
      h('td', {}, h('span', { class: 'pill ' + it.state, title: it.confidence === 'uncertain' ? 'Best guess: this period of history is incomplete' : '' }, STATE_TEXT[it.state] + (it.confidence === 'uncertain' ? ' ?' : ''))),
      h('td', { class: 'num hide-sm' }, it.isDir ? '' : bytes(it.size)),
      h('td', { class: 'num hide-sm' }, it.isDir ? '' : new Date(it.modTime).toLocaleString(undefined, { dateStyle: 'medium', timeStyle: 'short' })));
  });
  const table = l.items.length ? h('table', { class: 'files' }, h('thead', {}, h('tr', {}, h('th', {}, all), h('th', {}, 'Name'), h('th', {}, 'Since then'), h('th', { class: 'hide-sm' }, 'Size'), h('th', { class: 'hide-sm' }, 'Modified'))), h('tbody', {}, rows))
    : h('div', { class: 'empty' }, 'Nothing here at this moment.');
  fill($('portal-body'), timebar,
    portalState.offline ? h('div', { class: 'banner info' }, 'The backup can’t be reached right now; showing what this computer remembers. Restoring needs the backup to be reachable.') : null,
    l.note ? h('div', { class: 'banner warn' }, l.note) : null,
    crumbs, table, h('div', { class: 'actionbar', id: 'portal-actions' }), h('div', { id: 'restore-status' }));
  renderActionbar();
  renderRestoreStatus();
}

function renderActionbar() {
  const bar = $('portal-actions'); if (!bar) return;
  const n = portalState.selected.size;
  fill(bar, h('span', { class: 'sub' }, n ? `${n} selected` : 'Select files or folders to bring back'),
    h('button', { class: 'primary', disabled: !n, onclick: () => restoreDialog() }, 'Restore…'));
}

function restoreDialog() {
  const own = portalState.sel.startsWith('job:');
  const when = portalState.at.toLocaleString(undefined, { dateStyle: 'medium', timeStyle: 'short' });
  let target = own ? 'original' : 'folder';
  const folder = h('input', { value: defaultRestoreFolder() });
  const render = () => {
    const { form } = openDialog(`Restore ${portalState.selected.size} item(s) as of ${when}`, [
      h('div', { class: 'choices' },
        own ? (() => { const i = h('input', { type: 'radio', name: 't', checked: target === 'original' }); i.addEventListener('change', () => { target = 'original'; render(); });
          return h('label', { class: 'choice' }, i, h('span', {}, h('b', {}, 'Put them back where they were'), h('small', {}, 'Deleted files come back. If a newer version is there now, it is kept and the old one is added beside it as “name (restored …)”.'))); })() : null,
        (() => { const i = h('input', { type: 'radio', name: 't', checked: target === 'folder' }); i.addEventListener('change', () => { target = 'folder'; render(); });
          return h('label', { class: 'choice' }, i, h('span', {}, h('b', {}, 'Into a separate folder'), h('small', {}, 'Nothing existing is touched.'))); })()),
      target === 'folder' ? field('Folder', folder, '~ means your user folder.') : null,
    ], [h('button', { class: 'primary', type: 'submit' }, 'Restore')]);
    form.onsubmit = async (e) => {
      e.preventDefault();
      try {
        await api('POST', '/api/portal/restore?' + portalQuery(), { paths: [...portalState.selected], at: Math.floor(+portalState.at / 1000), target, folder: folder.value.trim(), keepBoth: true });
        closeDialog(); toast('Restoring…'); portalState.selected.clear(); renderPortal();
      } catch (x) { toast(x.message, true); }
    };
  };
  render();
}

function defaultRestoreFolder() {
  const d = portalState.at, p = (n) => String(n).padStart(2, '0');
  const stamp = `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}.${p(d.getMinutes())}`;
  return (S.os === 'windows' ? '~\\' : '~/') + 'Restored from backup ' + stamp;
}

async function renderRestoreStatus() {
  const box = $('restore-status'); if (!box) return;
  let st; try { st = (await api('GET', '/api/portal/restore')).restore; } catch { return; }
  if (!st) { fill(box); return; }
  const failed = Object.keys(st.failed || {}).length, renamed = Object.keys(st.renamed || {}).length, unavailable = (st.unavailable || []).length;
  fill(box, h('div', { class: 'card' },
    h('h2', {}, st.finished ? 'Restore finished' : 'Restoring…'),
    h('p', { class: 'sub' }, `${st.done} of ${st.total} files · ${bytes(st.bytes)} into ${st.target}`),
    st.finished ? null : h('div', { class: 'progress' }, (() => { const i = h('i'); i.style.width = (st.total ? Math.round(100 * st.done / st.total) : 0) + '%'; return i; })()),
    renamed ? h('p', { class: 'sub' }, `${renamed} kept beside a newer copy as “(restored …)”.`) : null,
    unavailable ? h('p', { class: 'warn' }, `${unavailable} file(s) can’t be recovered: their old versions are no longer kept.`) : null,
    failed ? h('p', { class: 'err' }, `${failed} failed: ` + Object.entries(st.failed).slice(0, 3).map(([p, e]) => `${p} (${e})`).join('; ')) : null,
    st.finished ? null : h('button', { onclick: () => act(() => api('POST', '/api/portal/restore/stop')) }, 'Stop')));
}

async function portalOther() {
  const sel = h('select', {}, S.config.remotes.map((r) => h('option', { value: r.name + ':' }, r.name)), h('option', { value: '' }, 'A local folder…'));
  const path = h('input', { placeholder: 'Backup/Home', value: 'Backup' });
  const results = h('div', { class: 'choices' });
  const look = h('button', { type: 'button', onclick: async () => {
    const fs = sel.value ? sel.value + path.value.replace(/^\/+/, '') : path.value.trim();
    fill(results, h('p', { class: 'sub' }, 'Looking…'));
    try {
      const refs = await api('GET', '/api/portal/discover?fs=' + encodeURIComponent(fs));
      if (!refs.length) { fill(results, h('p', { class: 'sub' }, 'No Enter Space backups found there. Point at the backup folder itself (the one that contains .enterspace).')); return; }
      fill(results, refs.map((r) => h('button', { type: 'button', onclick: () => {
        if (!portalState.foreign.some((x) => x.id === r.id && x.destFs === r.destFs)) portalState.foreign.push(r);
        portalState.sel = 'ext|' + r.destFs + '|' + r.id; closeDialog(); portalOpen();
      } }, `${r.name || r.id} — ${r.runs} backups, last ${new Date(r.lastRun).toLocaleDateString()}`)));
    } catch (e) { fill(results, h('p', { class: 'error' }, e.message)); }
  } }, 'Look for backups');
  openDialog('Open another backup', [h('p', { class: 'help' }, 'Open a backup made by another computer, or by Enter Space on a Mac, iPhone or iPad. Choose where it is stored.'),
    field('Stored on', sel), field('Folder', path, 'The backup folder (the one containing .enterspace).'), look, results], []);
}
