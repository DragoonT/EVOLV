/* EVOLV front brain — connects to the organism over WebSocket and hot-swaps
   whatever the agent rewrites. No refresh. Ever. */
'use strict';

const $ = (s) => document.querySelector(s);
const state = {
  ws: null, connected: false, busy: false,
  evolutionCount: 0, mounted: new Map(), // name -> el
  shellReloadTimer: null, thinkingEl: null,
};

/* ---------------- background: neural particles ---------------- */
(function bg() {
  const cv = $('#bg-canvas'), ctx = cv.getContext('2d');
  let W, H, pts = [], raf;
  function size() { W = cv.width = innerWidth; H = cv.height = innerHeight; }
  function init() {
    size(); pts = [];
    const n = Math.min(90, Math.floor(W * H / 22000));
    for (let i = 0; i < n; i++) pts.push({
      x: Math.random() * W, y: Math.random() * H,
      vx: (Math.random() - 0.5) * 0.35, vy: (Math.random() - 0.5) * 0.35,
      r: Math.random() * 1.6 + 0.4,
    });
  }
  function tick() {
    ctx.clearRect(0, 0, W, H);
    for (const p of pts) {
      p.x += p.vx; p.y += p.vy;
      if (p.x < 0 || p.x > W) p.vx *= -1;
      if (p.y < 0 || p.y > H) p.vy *= -1;
      ctx.beginPath(); ctx.arc(p.x, p.y, p.r, 0, 7);
      ctx.fillStyle = 'rgba(139,120,246,0.35)'; ctx.fill();
    }
    for (let i = 0; i < pts.length; i++) for (let j = i + 1; j < pts.length; j++) {
      const a = pts[i], b = pts[j], dx = a.x - b.x, dy = a.y - b.y, d2 = dx * dx + dy * dy;
      if (d2 < 15000) {
        ctx.beginPath(); ctx.moveTo(a.x, a.y); ctx.lineTo(b.x, b.y);
        ctx.strokeStyle = `rgba(99,102,241,${0.09 * (1 - d2 / 15000)})`; ctx.lineWidth = 1; ctx.stroke();
      }
    }
    raf = requestAnimationFrame(tick);
  }
  addEventListener('resize', () => { init(); });
  init(); tick();
  // slight pause when tab hidden — be kind to batteries
  document.addEventListener('visibilitychange', () => {
    if (document.hidden) cancelAnimationFrame(raf); else tick();
  });
})();

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
    case 'fs:change': onFsChange(ev.payload || {}); break;
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
    case 'agent:provider': {
      const p = ev.payload || {};
      $('#prov-name').textContent = p.provider; $('#model-name').textContent = p.model;
      toast(`🧠 mind swapped → <b>${p.provider} / ${p.model}</b>`);
      break;
    }
    case 'plugin:toast': toast((ev.payload || {}).text || ''); break;
    case 'plugin:log': console.log('[plugin]', (ev.payload || {}).text); break;
  }
}

/* ---------------- shell hot-reload (genome recompile) ---------------- */
const SHELL_FILES = new Set(['workspace/ui/index.html', 'workspace/ui/app.js', 'workspace/ui/style.css']);
function onFsChange(ch) {
  if (ch.kind === 'remove') {
    if (ch.zone === 'components') {
      const name = (ch.path.split('/').pop() || '').replace(/\.html$/, '');
      if (state.mounted.has(name)) { state.mounted.get(name).remove(); state.mounted.delete(name); refreshEmpty(); }
    }
    return;
  }
  if (SHELL_FILES.has(ch.path.replace(/\\/g, '/'))) {
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
    if (!st.closest('body') || true) body.appendChild(s);
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

/* ---------------- evolution stream ---------------- */
const KIND_META = {
  component: ['🧩', '#22d3ee'], tool: ['🔧', '#a3e635'], skill: ['🧠', '#8b5cf6'],
  prompt: ['✍️', '#fbbf24'], plugin: ['🔌', '#f472b6'], file: ['📄', '#8b93ab'],
  remove_component: ['🗑', '#f87171'],
};
function onEvolution(e) {
  state.evolutionCount = Math.max(state.evolutionCount, e.n || 0);
  const c = $('#evo-count');
  c.textContent = state.evolutionCount;
  const box = $('#evo-counter');
  box.classList.remove('bump'); void box.offsetWidth; box.classList.add('bump');

  const [ico] = KIND_META[e.kind] || ['✨', '#fff'];
  const t = new Date().toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit' });
  const item = document.createElement('div');
  item.className = 'evo-item';
  item.innerHTML = `<span class="ico">${ico}</span><span><div><span class="k">${e.kind}</span> <span class="n">${escapeHtml(e.name || '')}</span></div>${e.description ? `<div class="d">${escapeHtml(e.description)}</div>` : ''}</span><span class="t">${t}</span>`;
  $('#stream-list').prepend(item);
}

async function loadEvolutionHistory() {
  try {
    const r = await fetch('/api/evolution');
    const d = await r.json();
    state.evolutionCount = d.count || 0;
    $('#evo-count').textContent = state.evolutionCount;
    (d.history || []).slice().reverse().forEach((h) => {
      const [ico] = KIND_META[h.kind] || ['✨', '#fff'];
      const item = document.createElement('div');
      item.className = 'evo-item';
      item.innerHTML = `<span class="ico">${ico}</span><span><div><span class="k">${h.kind}</span> <span class="n">${escapeHtml(h.name || '')}</span></div>${h.description ? `<div class="d">${escapeHtml(h.description)}</div>` : ''}</span><span class="t">${(h.time || '').slice(11, 19)}</span>`;
      $('#stream-list').appendChild(item);
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

let currentAssistant = null; // {bubble, raw}
function showThinking() {
  if (currentAssistant) return;
  const m = el('div', 'msg evolv');
  m.innerHTML = `<div class="who">🧬 EVOLV</div><div class="bubble thinking"><i></i><i></i><i></i></div>`;
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
    m.innerHTML = `<div class="who">🧬 EVOLV</div><div class="bubble"></div>`;
    $('#chat-scroll').appendChild(m);
    currentAssistant = { m, raw: '', doneBlocks: 0 };
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
    currentAssistant.m.querySelector('.bubble').before(d);
  }
  d.querySelector('pre').textContent += text;
  scrollChat();
}

/* Render streamed text: visible prose + mutation chips for evolv blocks.
   Incomplete trailing evolv block shows a "writing DNA…" chip. */
function renderAssistant() {
  const { raw } = currentAssistant;
  const bubble = currentAssistant.m.querySelector('.bubble');
  const re = /```evolv\s*\n([\s\S]*?)```/g;
  let htmlOut = '', last = 0, mch;
  while ((mch = re.exec(raw))) {
    htmlOut += mdLite(raw.slice(last, mch.index));
    htmlOut += `<div class="toast" style="animation:none;margin:6px 0">🧬 mutation committed → genome</div>`;
    last = re.lastIndex;
  }
  const tail = raw.slice(last);
  const openIdx = tail.lastIndexOf('```evolv');
  if (openIdx >= 0) {
    htmlOut += mdLite(tail.slice(0, openIdx));
    htmlOut += `<div class="toast" style="animation:none;margin:6px 0;border-color:#22d3ee">✍️ writing DNA…</div>`;
  } else {
    htmlOut += mdLite(tail);
  }
  bubble.innerHTML = htmlOut;
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
  const who = role === 'user' ? 'YOU' : '🧬 EVOLV';
  m.innerHTML = `<div class="who">${who}</div><div class="bubble">${role === 'user' ? escapeHtml(text) : mdLite(text)}</div>`;
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
    const [cfg, models] = await Promise.all([
      fetch('/api/settings').then((r) => r.json()),
      fetch('/api/models').then((r) => r.json()).catch(() => ({})),
    ]);
    const c = cfg.config || {};
    $('#set-provider').value = c.provider || 'kilo';
    $('#set-model').value = c.model || '';
    $('#set-username').value = c.user_name || '';
    $('#genome-path').textContent = '~/.evolv';
    const dl = $('#model-list'); dl.innerHTML = '';
    (models.models || []).forEach((m) => {
      const o = document.createElement('option'); o.value = m; dl.appendChild(o);
    });
  } catch {}
}

async function saveSettings() {
  const body = {
    provider: $('#set-provider').value,
    model: $('#set-model').value.trim(),
    user_name: $('#set-username').value.trim(),
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

/* ---------------- wiring ---------------- */
window.addEventListener('DOMContentLoaded', () => {
  connect();
  loadEvolutionHistory();

  $('#send').addEventListener('click', () => send());
  $('#input').addEventListener('keydown', (e) => {
    if (e.key === 'Enter' && !e.shiftKey) { e.preventDefault(); send(); }
  });
  $('#input').addEventListener('input', function () {
    this.style.height = 'auto'; this.style.height = Math.min(this.scrollHeight, 120) + 'px';
  });
  document.querySelectorAll('.quick').forEach((b) => b.addEventListener('click', () => send(b.dataset.q)));
  $('#btn-settings').addEventListener('click', openDrawer);
  $('#provider-pill').addEventListener('click', openDrawer);
  $('#btn-close-settings').addEventListener('click', () => $('#drawer').classList.remove('open'));
  $('#btn-save').addEventListener('click', saveSettings);
  $('#btn-files').addEventListener('click', () => {
    toast('🗂 genome lives at <code>~/.evolv/workspace/</code> — edit anything, it hot-reloads', 6000);
  });

  addMsg('evolv', `**I'm alive.** ⚡\n\nI'm not a chat window — I'm a workspace that rewrites itself. Ask me for anything, and if it deserves UI, I'll grow it onto the canvas in front of you, live.\n\nTry: *“evolve a pomodoro timer”* — or hit **🧬 evolve freely** and watch me surprise myself.`);
});
