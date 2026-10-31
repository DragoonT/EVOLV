/* EVOLV front brain (v6 shell) — connects to the organism over WebSocket and
   hot-swaps whatever the agent rewrites. No refresh. Ever. */
'use strict';

const $ = (s) => document.querySelector(s);
const state = {
  ws: null, connected: false, busy: false,
  evolutionCount: 0, mounted: new Map(), // name -> el
  shellReloadTimer: null, thinkingEl: null,
};

/* ---------------- update banner ---------------- */
function showUpdateBanner(u) {
  const b = $('#update-banner');
  if (!b || !u.version) return;
  $('#update-version').textContent = u.version;
  b.classList.add('show');
  toast(`🧬 <b>evolv v${u.version}</b> is out — a newer organism exists. <b>evolv update</b> to morph.`, 8000);
}
async function runUpdate() {
  toast('⬇️ morphing to the latest genome…');
  try {
    const r = await fetch('/api/update/run', { method: 'POST' });
    const d = await r.json();
    if (d.ok) toast(`✅ updated to <b>v${d.version}</b> — restart evolv to wake the new body`);
    else toast(`⚠️ update failed: ${d.error || 'unknown'}`);
  } catch { toast('⚠️ update failed — check your connection'); }
}

/* ---------------- toasts ---------------- */
function toast(msg, ms = 3800) {
  const t = document.createElement('div');
  t.className = 'toast'; t.innerHTML = msg;
  $('#toasts').appendChild(t);
  setTimeout(() => { t.classList.add('out'); setTimeout(() => t.remove(), 450); }, ms);
}
window.evolvToast = toast; // for grown components & plugins

/* ---------------- websocket: the spine ---------------- */
function connect() {
  const proto = location.protocol === 'https:' ? 'wss' : 'ws';
  const ws = new WebSocket(`${proto}://${location.host}/ws`);
  state.ws = ws;
  ws.onopen = () => { state.connected = true; $('#provider-pill .dot').classList.remove('busy'); };
  ws.onclose = () => { state.connected = false; setTimeout(connect, 1200); };
  ws.onerror = () => ws.close();
  ws.onmessage = (e) => {
    let ev; try { ev = JSON.parse(e.data); } catch { return; }
    handle(ev);
  };
}

function handle(ev) {
  switch (ev.type) {
    case 'hello': {
      const h = ev.payload || {};
      if (h.agent) {
        $('#prov-name').textContent = h.agent.provider || 'kilo';
        $('#model-name').textContent = h.agent.model || '';
      }
      break;
    }
    case 'fs:change': onFsChange(ev.payload || {}, ev.time); break;
    case 'evolution': onEvolution(ev.payload || {}); break;
    case 'agent:token': onToken((ev.payload || {}).text || ''); break;
    case 'agent:reasoning': onReasoning((ev.payload || {}).text || ''); break;
    case 'agent:phase': {
      const p = (ev.payload || {}).phase;
      const pill = $('#provider-pill');
      pill.classList.toggle('busy', p === 'thinking' || p === 'evolving');
      if (p === 'thinking' && !state.thinkingEl && !currentAssistant) showThinking();
      if (p === 'idle') finishAssistant();
      break;
    }
    case 'tool:call': onToolCall(ev.payload || {}); break;
    case 'agent:provider': {
      const p = ev.payload || {};
      $('#prov-name').textContent = p.provider; $('#model-name').textContent = p.model;
      toast(`🧠 mind swapped → <b>${p.provider} / ${p.model}</b>`);
      break;
    }
    case 'plugin:toast': toast((ev.payload || {}).text || ''); break;
    case 'dream': {
      const st = (ev.payload || {}).state;
      const pill = $('#provider-pill');
      pill.classList.toggle('busy', st === 'start');
      if (st === 'start') toast('💭 <b>evolv is dreaming</b> — autonomous evolution in progress…', 7000);
      else toast('🌅 dream over — see what it grew in the sidebar', 6000);
      break;
    }
    case 'plugin:log': console.log('[plugin]', (ev.payload || {}).text); break;
    case 'shell:reload': {
      $('#recompile-path').textContent = (ev.payload || {}).reason || 'the organism is rewriting its own shell…';
      $('#recompile').classList.add('show');
      clearTimeout(state.shellReloadTimer);
      state.shellReloadTimer = setTimeout(() => location.reload(), 900);
      break;
    }
    case 'update:available': {
      const u = ev.payload || {};
      showUpdateBanner(u);
      break;
    }
    case 'config:updated': {
      const p = ev.payload || {};
      if (p.provider) $('#prov-name').textContent = p.provider;
      if (p.model) $('#model-name').textContent = p.model;
      break;
    }
  }
}

/* ---------------- shell hot-reload (genome recompile) ---------------- */
const SHELL_FILES = new Set(['workspace/ui/index.html', 'workspace/ui/app.js']);
function onFsChange(ch, evTime) {
  const rel = ch.path.replace(/\\/g, '/');
  // replayed/stale events must not trigger recompiles: only react to fresh
  // writes and renames (boot-time 'create' storms are how the genome seeds)
  const age = Date.now() - new Date(evTime).getTime();
  const fresh = (ch.kind === 'write' || ch.kind === 'rename') && age < 15000;
  if (rel === 'workspace/ui/style.css') {
    // SKIN HOT-SWAP — no reload, no flash. The stylesheet is swapped under
    // the running app; you literally watch the organism change clothes.
    if (!fresh) return;
    const link = document.querySelector('link[rel="stylesheet"]');
    if (link) {
      link.href = '/style.css?v=' + Date.now();
      toast('🎨 skin hot-swapped live — zero refresh', 2500);
    }
    return;
  }
  if (genomeOpen) refreshGenomeList(rel); // the inspector watches the genome live
  if (ch.kind === 'remove') {
    if (ch.zone === 'components') {
      const name = (ch.path.split('/').pop() || '').replace(/\.html$/, '');
      if (state.mounted.has(name)) { state.mounted.get(name).remove(); state.mounted.delete(name); refreshEmpty(); }
    }
    return;
  }
  if (fresh && SHELL_FILES.has(rel)) {
    // the organism rewrote its own shell — recompile visibly, then hot-swap
    $('#recompile-path').textContent = ch.path;
    $('#recompile').classList.add('show');
    clearTimeout(state.shellReloadTimer);
    state.shellReloadTimer = setTimeout(() => location.reload(), 900);
    return;
  }
  if (ch.zone === 'components' && ch.path.endsWith('.html')) {
    const name = (ch.path.split('/').pop() || '').replace(/\.html$/, '');
    mountComponent(name, true);
  }
}

/* ---------------- components ---------------- */
function compRoot() { return $('#canvas'); }

async function mountComponent(name, animate) {
  let html;
  try {
    const r = await fetch(`/ui/components/${encodeURIComponent(name)}.html?${Date.now()}`);
    if (!r.ok) return;
    html = await r.text();
  } catch { return; }

  const existing = state.mounted.get(name);
  let card;
  if (existing) {
    card = existing;
    card.classList.remove('mutating'); void card.offsetWidth; card.classList.add('mutating');
  } else {
    card = document.createElement('div');
    card.className = 'comp-card';
    card.dataset.name = name;
    compRoot().prepend(card);
    state.mounted.set(name, card);
    refreshEmpty();
  }

  // parse & graft: <style> injected, markup into body, scripts re-executed
  const doc = new DOMParser().parseFromString(html, 'text/html');
  const head = card.querySelector('.comp-head');
  if (!head) {
    card.innerHTML = `<div class="comp-head"><span class="ico">🧩</span><span><div class="name">${name}</div><div class="desc">self-authored component</div></span><span class="x" title="ask evolv to remove it">✕</span></div><div class="comp-body"></div>`;
    card.querySelector('.x').addEventListener('click', () => send(`remove_component ${name} from the canvas via remove_component, confirm in one line`));
  }
  const body = card.querySelector('.comp-body');
  body.innerHTML = '';

  for (const st of Array.from(doc.querySelectorAll('style'))) {
    const s = document.createElement('style');
    s.textContent = st.textContent;
    body.appendChild(s);
  }
  const scripts = Array.from(doc.querySelectorAll('script')).map((sc) => sc.textContent);
  const frag = document.createDocumentFragment();
  for (const node of Array.from(doc.body.childNodes)) {
    if (node.nodeName === 'SCRIPT' || node.nodeName === 'STYLE') continue;
    frag.appendChild(document.importNode(node, true));
  }
  body.appendChild(frag);
  for (const code of scripts) {
    const s = document.createElement('script');
    s.textContent = code;
    body.appendChild(s); // appending executes it
  }

  if (!existing && animate) {
    toast(`⚡ <b>${name}</b> grew onto the canvas — live, no refresh`);
  }
  refreshEmpty();
}

function refreshEmpty() {
  $('#canvas-empty').style.display = state.mounted.size ? 'none' : '';
}

/* ---------------- evolution stream (sidebar) ---------------- */
const KIND_META = {
  component: ['🧩'], tool: ['🔧'], skill: ['🧠'],
  prompt: ['✍️'], plugin: ['🔌'], file: ['📄'],
  edit: ['✏️'], delete: ['💥'], move: ['📦'],
  project: ['🎯'], remove_component: ['🗑'],
  snapshot: ['🧊'], rollback: ['⏪'], self_update: ['🦋'],
};
function evoItem(kind, name, desc, time) {
  const [ico] = KIND_META[kind] || ['✨'];
  const t = time || new Date().toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit' });
  const item = document.createElement('div');
  item.className = 'evo-item';
  item.innerHTML = `<span class="ico">${ico}</span><span><div><span class="k">${escapeHtml(kind)}</span> <span class="n">${escapeHtml(name || '')}</span></div>${desc ? `<div class="d">${escapeHtml(desc)}</div>` : ''}</span><span class="t">${t}</span>`;
  return item;
}
function bumpCounter() {
  for (const id of ['#evo-count', '#evo-count2']) {
    const c = $(id); if (c) c.textContent = state.evolutionCount;
  }
  const box = $('#evo-counter');
  box.classList.remove('bump'); void box.offsetWidth; box.classList.add('bump');
}
function onEvolution(e) {
  state.evolutionCount = Math.max(state.evolutionCount, e.n || 0);
  loadMind(); // the mind panel updates the instant something grows
  bumpCounter();
  $('#stream-list').prepend(evoItem(e.kind, e.name, e.description));
}

async function loadEvolutionHistory() {
  try {
    const r = await fetch('/api/evolution');
    const d = await r.json();
    state.evolutionCount = d.count || 0;
    bumpCounter();
    (d.history || []).slice().reverse().forEach((h) => {
      $('#stream-list').appendChild(evoItem(h.kind, h.name, h.description, (h.time || '').slice(11, 19)));
    });
    (d.components || []).forEach((n) => mountComponent(n, false));
  } catch {}
}

/* ---------------- chat ---------------- */
function el(tag, cls, html) {
  const e = document.createElement(tag);
  if (cls) e.className = cls;
  if (html !== undefined) e.innerHTML = html;
  return e;
}
function escapeHtml(s) {
  return (s || '').replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
}

let currentAssistant = null; // {m, body, raw}
function showThinking() {
  if (currentAssistant) return;
  const m = el('div', 'msg evolv');
  m.innerHTML = `<div class="avatar">E</div><div class="body"><div class="bubble thinking"><i></i><i></i><i></i></div></div>`;
  $('#chat-scroll').appendChild(m);
  state.thinkingEl = m;
  scrollChat();
}
function hideThinking() {
  if (state.thinkingEl) { state.thinkingEl.remove(); state.thinkingEl = null; }
}

function onToken(text) {
  hideThinking();
  if (!currentAssistant) {
    const m = el('div', 'msg evolv');
    m.innerHTML = `<div class="avatar">E</div><div class="body"></div>`;
    $('#chat-scroll').appendChild(m);
    currentAssistant = { m, body: m.querySelector('.body'), raw: '' };
  }
  currentAssistant.raw += text;
  renderAssistant();
  scrollChat();
}

function onReasoning(text) {
  if (!currentAssistant) return;
  let d = currentAssistant.m.querySelector('details.reason');
  if (!d) {
    d = el('details', 'reason');
    d.innerHTML = '<summary>🤔 thoughts</summary><pre></pre>';
    currentAssistant.body.prepend(d);
  }
  d.querySelector('pre').textContent += text;
  scrollChat();
}

/* Render streamed text: visible prose + tool call cards for evolv blocks.
   Incomplete trailing evolv block shows a "running…" card. */
const ACTION_META = {
  evolve_component: ['🧩', 'grow component'], remove_component: ['🗑️', 'remove component'],
  evolve_tool: ['🔧', 'grow tool'], run_tool: ['⚙️', 'run tool'],
  evolve_skill: ['🧠', 'learn skill'], evolve_prompt: ['✍️', 'rewrite prompt'],
  evolve_plugin: ['🔌', 'install plugin'], write_file: ['📝', 'write file'],
  edit_file: ['✏️', 'edit file'], read_file: ['📖', 'read file'],
  list_dir: ['📂', 'list dir'], tree: ['🌲', 'tree'],
  delete_file: ['💥', 'delete'], move: ['📦', 'move'],
  set_project: ['🎯', 'bind project'], reload_shell: ['🔄', 'recompile shell'],
  snapshot: ['🧊', 'freeze genome'], rollback: ['⏪', 'rewind genome'],
  shell: ['❖', 'shell'], self_update: ['🦋', 'self update'],
};
function toolCard(act, pending) {
  const [ico, verb] = ACTION_META[act.action] || ['✨', act.action];
  const target = act.name || act.path || act.to || act.command || '';
  const shortTarget = String(target).length > 64 ? String(target).slice(0, 61) + '…' : target;
  return `<div class="tool-card${pending ? ' pending' : ''}" data-action="${escapeHtml(act.action || '')}" data-target="${escapeHtml(String(target))}">`
    + `<div class="tc-line"><span class="tc-dot"></span><span class="tc-verb">${escapeHtml(verb)}</span>`
    + (shortTarget ? `<span class="tc-target">${escapeHtml(shortTarget)}</span>` : '')
    + `<span class="tc-status">${pending ? '◌' : '✓'}</span></div></div>`;
}
function evolvBlockCards(body) {
  const acts = [];
  const dec = evolvLines(body);
  for (const line of dec) {
    try { acts.push(JSON.parse(line)); } catch {}
  }
  return acts.map((a) => toolCard(a, false)).join('');
}
// tolerant line-splitter: JSON objects may span lines, so use a brace scanner
function evolvLines(body) {
  const out = []; let depth = 0, cur = '', inStr = false, esc = false;
  for (const ch of body) {
    if (depth === 0 && !/\s/.test(ch) && ch !== '{') { continue; }
    cur += ch;
    if (esc) { esc = false; continue; }
    if (ch === '\\') { esc = true; continue; }
    if (ch === '"') inStr = !inStr;
    if (inStr) continue;
    if (ch === '{') depth++;
    else if (ch === '}') { depth--; if (depth === 0) { out.push(cur); cur = ''; } }
  }
  return out;
}
function renderAssistant() {
  const { raw, body } = currentAssistant;
  const re = /```evolv\s*\n([\s\S]*?)```/g;
  let htmlOut = '', last = 0, mch;
  while ((mch = re.exec(raw))) {
    htmlOut += mdLite(raw.slice(last, mch.index));
    htmlOut += `<div class="tool-group">` + evolvBlockCards(mch[1]) + `</div>`;
    last = re.lastIndex;
  }
  const tail = raw.slice(last);
  const openIdx = tail.lastIndexOf('```evolv');
  if (openIdx >= 0) {
    htmlOut += proseWithSalvage(tail.slice(0, openIdx));
    htmlOut += `<div class="tool-group">` + evolvLines(tail.slice(openIdx + 8)).map((l) => {
      try { return toolCard(JSON.parse(l), true); } catch { return `<div class="tool-card pending"><div class="tc-line"><span class="tc-dot"></span><span class="tc-verb">mutating genome…</span></div></div>`; }
    }).join('') + `</div>`;
  } else {
    htmlOut += proseWithSalvage(tail);
  }
  body.innerHTML = htmlOut;
}

/* Prose render that salvages action-JSON the model leaked without a fence:
   the kernel executes it anyway, so the UI shows it as a tool card instead of
   raw garbage. */
function proseWithSalvage(bodyText) {
  if (!bodyText) return '';
  let out = '', last = 0, depth = 0, start = -1, inStr = false, esc = false;
  const flush = (from, to) => { const seg = bodyText.slice(from, to); if (seg.trim()) out += mdLite(seg); };
  for (let i = 0; i < bodyText.length; i++) {
    const ch = bodyText[i];
    if (esc) { esc = false; continue; }
    if (ch === '\\' && inStr) { esc = true; continue; }
    if (ch === '"') { inStr = !inStr; continue; }
    if (inStr) continue;
    if (ch === '{') { if (depth === 0) start = i; depth++; }
    else if (ch === '}') {
      depth--;
      if (depth === 0 && start >= 0) {
        const obj = bodyText.slice(start, i + 1);
        if (obj.includes('"action"')) {
          try {
            flush(last, start);
            out += toolCard(JSON.parse(obj), false);
            last = i + 1;
          } catch { /* not valid JSON — leave it in the prose */ }
        }
        start = -1;
      } else if (depth < 0) depth = 0;
    }
  }
  flush(last, bodyText.length);
  return out;
}

/* Live tool status: the kernel publishes tool:call (running/ok/error) around
   every execution. Match the newest pending card with the same action+target. */
function onToolCall(p) {
  const cards = document.querySelectorAll(`.tool-card.pending[data-action="${CSS.escape(p.action || '')}"]`);
  for (let i = cards.length - 1; i >= 0; i--) {
    const c = cards[i];
    if (!p.target || !p.path || String(c.dataset.target) === String(p.target) || String(c.dataset.target) === String(p.path)) {
      c.classList.remove('pending');
      if (p.state === 'running') c.classList.add('running');
      else if (p.state === 'error') c.classList.add('error');
      const st = c.querySelector('.tc-status');
      if (st) st.textContent = p.state === 'running' ? '…' : (p.state === 'error' ? '✗' : '✓');
      return;
    }
  }
}

function finishAssistant() {
  if (!currentAssistant) return;
  hideThinking();
  renderAssistant();
  currentAssistant = null;
  state.busy = false;
  $('#send').disabled = false;
}

// tiny markdown: **bold**, `code`, fenced code, paragraphs
function mdLite(s) {
  let out = escapeHtml(s);
  out = out.replace(/```(\w*)\n([\s\S]*?)```/g, (_, l, c) => `<pre><code>${c}</code></pre>`);
  out = out.replace(/`([^`\n]+)`/g, '<code>$1</code>');
  out = out.replace(/\*\*([^*\n]+)\*\*/g, '<strong>$1</strong>');
  out = out.replace(/\*([^*\n]+)\*/g, '<em>$1</em>');
  out = out.split(/\n{2,}/).map((p) => p.trim() && `<p>${p.replace(/\n/g, '<br>')}</p>`).filter(Boolean).join('');
  return out;
}

function scrollChat() { const sc = $('#chat-scroll'); sc.scrollTop = sc.scrollHeight; }

function addMsg(role, text) {
  const m = el('div', 'msg ' + (role === 'user' ? 'user' : 'evolv'));
  if (role === 'user') {
    m.innerHTML = `<div class="bubble">${escapeHtml(text)}</div>`;
  } else {
    m.innerHTML = `<div class="avatar">E</div><div class="body">${mdLite(text)}</div>`;
  }
  $('#chat-scroll').appendChild(m);
  scrollChat();
  return m;
}

async function send(text) {
  text = (text !== undefined ? text : $('#input').value).trim();
  if (!text || state.busy) return;
  $('#input').value = '';
  $('#input').style.height = 'auto';
  state.busy = true;
  $('#send').disabled = true;
  addMsg('user', text);
  showThinking();
  try {
    await fetch('/api/chat', {
      method: 'POST', headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ message: text }),
    });
  } catch {
    hideThinking(); finishAssistant();
    toast('⚠️ kernel unreachable — is evolv still running?');
  }
}

/* ---------------- settings ---------------- */
function openDrawer() { $('#drawer').classList.add('open'); loadSettings(); }
async function loadSettings() {
  try {
    const [cfg, models, upd] = await Promise.all([
      fetch('/api/settings').then((r) => r.json()),
      fetch('/api/models').then((r) => r.json()).catch(() => ({})),
      fetch('/api/update').then((r) => r.json()).catch(() => ({})),
    ]);
    const c = cfg.config || {};
    $('#set-provider').value = c.provider || 'kilo';
    $('#set-model').value = c.model || '';
    $('#set-username').value = c.user_name || '';
    $('#set-project').value = c.project_dir || '';
    $('#set-dream').checked = !!c.auto_evolve;
    $('#set-dream-mins').value = c.heartbeat_mins || 15;
    $('#genome-path').textContent = '~/.evolv';
    const dl = $('#model-list'); dl.innerHTML = '';
    (models.models || []).forEach((m) => {
      const o = document.createElement('option'); o.value = m; dl.appendChild(o);
    });
    $('#genome-path').textContent = `~/.evolv  ·  v${upd.current || '?'}`;
    if (upd.available) showUpdateBanner(upd.available);
  } catch {}
}

async function saveSettings() {
  const body = {
    provider: $('#set-provider').value,
    model: $('#set-model').value.trim(),
    user_name: $('#set-username').value.trim(),
    project_dir: $('#set-project').value.trim(),
    auto_evolve: $('#set-dream').checked,
    heartbeat_mins: Math.max(1, parseInt($('#set-dream-mins').value, 10) || 15),
    api_keys: {},
    base_urls: {},
  };
  const key = $('#set-key').value.trim();
  if (key) body.api_keys[body.provider] = key;
  const base = $('#set-base').value.trim();
  if (base) body.base_urls[body.provider] = base;
  try {
    await fetch('/api/settings', {
      method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body),
    });
    toast('🧬 genome updated — new mind active');
    $('#drawer').classList.remove('open');
  } catch { toast('⚠️ could not save settings'); }
}

/* ---------------- mind panel (what the organism knows, live) ---------------- */
async function loadMind() {
  try {
    const r = await fetch('/api/mind');
    const d = await r.json();
    const box = $('#mind-list');
    if (!box) return;
    const row = (ico, cls, name) => `<span class="mind-item ${cls || ''}">${ico} ${escapeHtml(name)}</span>`;
    box.innerHTML =
      (d.skills || []).map((s) => row('🧠', 'm-skill', s)).join('') +
      (d.tools || []).map((t) => row('🔧', 'm-tool', t)).join('') +
      (d.plugins || []).map((p) => row('🔌', 'm-plugin', p)).join('') +
      (d.prompts || []).map((p) => row('✍️', 'm-prompt', p)).join('') || '<span class="mind-empty">nothing grown yet — ask it to evolve</span>';
    const c = $('#mind-stats');
    if (c) c.textContent = `${(d.skills || []).length}·${(d.tools || []).length}·${(d.plugins || []).length}`;
  } catch {}
}

/* ---------------- genome inspector (watch the body, live) ---------------- */
let genomeOpen = false, genomeFile = null, genomeDirty = false;
async function openGenome() {
  genomeOpen = true;
  $('#genome-modal').classList.add('show');
  await refreshGenomeList();
}
function closeGenome() {
  genomeOpen = false;
  $('#genome-modal').classList.remove('show');
}
async function refreshGenomeList(changedPath) {
  try {
    const r = await fetch('/api/genome');
    const d = await r.json();
    const list = $('#genome-list');
    list.innerHTML = '';
    (d.files || []).forEach((f) => {
      const item = document.createElement('div');
      item.className = 'g-file' + (f === genomeFile ? ' active' : '');
      item.textContent = f;
      item.addEventListener('click', () => openGenomeFile(f));
      list.appendChild(item);
    });
    $('#genome-count').textContent = `${(d.files || []).length} files · live`;
    if (changedPath) {
      const elx = [...list.children].find((x) => x.textContent === changedPath);
      if (elx) { elx.classList.add('g-flash'); setTimeout(() => elx.classList.remove('g-flash'), 1200); }
      // external change to the file being edited → reload it (unless typing)
      if (changedPath === genomeFile && !genomeDirty) openGenomeFile(genomeFile, true);
    }
  } catch {}
}
async function openGenomeFile(path, silent) {
  genomeFile = path; genomeDirty = false;
  try {
    const r = await fetch('/api/files?path=' + encodeURIComponent(path));
    if (!r.ok) return;
    $('#genome-editor').value = await r.text();
    $('#genome-open-path').textContent = 'workspace/' + path;
    document.querySelectorAll('.g-file').forEach((x) => x.classList.toggle('active', x.textContent === path));
    if (!silent) $('#genome-editor').focus();
  } catch {}
}
async function saveGenomeFile() {
  if (!genomeFile) return;
  try {
    const r = await fetch('/api/files', {
      method: 'POST', headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ path: genomeFile, content: $('#genome-editor').value }),
    });
    if (r.ok) { genomeDirty = false; toast('⚡ written to the genome — the organism feels it live'); }
    else toast('⚠️ write failed');
  } catch { toast('⚠️ write failed'); }
}

/* ---------------- layout toggles ---------------- */
function setSidebar(hidden) {
  document.body.classList.toggle('sb-hidden', hidden);
  localStorage.setItem('evolv.sb', hidden ? '1' : '');
}
function setCanvas(hidden) {
  document.body.classList.toggle('cv-hidden', hidden);
  localStorage.setItem('evolv.cv', hidden ? '1' : '');
}

/* ---------------- wiring ---------------- */
window.addEventListener('DOMContentLoaded', () => {
  connect();
  loadEvolutionHistory();
  loadMind();

  // restore panel state
  setSidebar(localStorage.getItem('evolv.sb') === '1');
  setCanvas(localStorage.getItem('evolv.cv') === '1');

  $('#send').addEventListener('click', () => send());
  $('#input').addEventListener('keydown', (e) => {
    if (e.key === 'Enter' && !e.shiftKey) { e.preventDefault(); send(); }
  });
  $('#input').addEventListener('input', function () {
    this.style.height = 'auto'; this.style.height = Math.min(this.scrollHeight, 140) + 'px';
  });
  document.querySelectorAll('.quick').forEach((b) => b.addEventListener('click', () => send(b.dataset.q)));
  $('#btn-settings').addEventListener('click', openDrawer);
  $('#provider-pill').addEventListener('click', openDrawer);
  $('#btn-close-settings').addEventListener('click', () => $('#drawer').classList.remove('open'));
  $('#btn-update').addEventListener('click', runUpdate);
  $('#btn-save').addEventListener('click', saveSettings);
  $('#btn-dream').addEventListener('click', async () => {
    toast('💭 tucking the organism in — dream starting…');
    try { await fetch('/api/dream', { method: 'POST' }); } catch { toast('⚠️ could not start dream'); }
  });
  $('#btn-files').addEventListener('click', openGenome);
  $('#btn-genome-close').addEventListener('click', closeGenome);
  $('#btn-genome-save').addEventListener('click', saveGenomeFile);
  $('#genome-editor').addEventListener('input', () => { genomeDirty = true; });
  $('#genome-editor').addEventListener('keydown', (e) => {
    if ((e.ctrlKey || e.metaKey) && e.key === 's') { e.preventDefault(); saveGenomeFile(); }
  });

  // layout
  $('#btn-sidebar').addEventListener('click', () => setSidebar(true));
  $('#btn-sidebar-open').addEventListener('click', () => setSidebar(false));
  $('#btn-canvas').addEventListener('click', () => setCanvas(document.body.classList.contains('cv-hidden')));
  $('#btn-canvas-close').addEventListener('click', () => setCanvas(true));
  $('#btn-new').addEventListener('click', async () => {
    $('#chat-scroll').innerHTML = '';
    currentAssistant = null; state.thinkingEl = null;
    try { await fetch('/api/reset', { method: 'POST' }); } catch {}
    welcome();
  });

  welcome();
});

function welcome() {
  addMsg('evolv', `**Hey.** I'm EVOLV — this window is my own source code.

When I change anything — my skin, my tool cards, a component on the canvas — you see it land live. Open **Genome inspector** in the sidebar to read and edit every file of my body yourself.

Ask me for a timer, a monitor, a small app. Or point me at your project: *"work on ~/my-app" — I read, edit and run code anywhere on this machine.*`);
}
