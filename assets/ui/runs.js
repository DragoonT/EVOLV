/* EVOLV durable execution runs — a small, quiet panel backed by /api/runs.
   The backend owns all lifecycle truth; this file only reflects it. */
(function () {
  'use strict';

  const phaseLabels = {
    queued: 'queued', planning: 'planning', running: 'running', waiting: 'waiting',
    verifying: 'verifying', paused: 'paused', completed: 'completed',
    failed: 'failed', cancelled: 'cancelled',
  };

  function el(tag, cls, text) {
    const n = document.createElement(tag);
    if (cls) n.className = cls;
    if (text != null) n.textContent = text;
    return n;
  }

  function badge(status) {
    const b = el('span', 'run-badge run-' + status, phaseLabels[status] || status);
    return b;
  }

  function render(container, runs) {
    container.replaceChildren();
    if (!runs.length) {
      container.append(el('p', 'runs-empty', 'No durable runs yet. Long assignments create one.'));
      return;
    }
    for (const run of runs) {
      const card = el('article', 'run-card run-' + run.status);
      const head = el('div', 'run-head');
      head.append(el('b', null, run.title || run.id));
      head.append(badge(run.status));
      card.append(head);
      if (run.objective) card.append(el('p', 'run-objective', run.objective));
      const p = run.progress || {};
      const total = p.total || 0;
      const done = p.completed || 0;
      const pct = total ? Math.round((done / total) * 100) : 0;
      const bar = el('div', 'run-bar');
      const fill = el('i');
      fill.style.width = pct + '%';
      bar.append(fill);
      card.append(bar);
      const meta = el('div', 'run-meta');
      meta.append(el('span', null, done + '/' + total + ' tasks'));
      if (p.failed) meta.append(el('span', null, p.failed + ' failed'));
      if (run.ready) meta.append(el('span', null, run.ready + ' ready'));
      card.append(meta);
      container.append(card);
    }
  }

  async function load(container) {
    try {
      const res = await fetch('/api/runs', { cache: 'no-store' });
      if (!res.ok) throw new Error('HTTP ' + res.status);
      const data = await res.json();
      const runs = data.runs || [];
      render(container, runs);
      const panel = document.getElementById('runs-panel');
      const count = document.getElementById('runs-count');
      const sec = document.getElementById('plan-sec');
      const busy = runs.filter(r => r.status === 'running' || r.status === 'planning').length;
      // Only claim sidebar space when there is real work to show.
      if (panel) panel.hidden = runs.length === 0;
      if (count) count.textContent = busy ? busy + ' active' : String(runs.length);
      if (panel && !panel.hidden && sec) sec.before(panel);
    } catch (e) {
      container.replaceChildren(el('p', 'runs-empty', 'Runs unavailable: ' + e.message));
    }
  }

  function mount() {
    const host = document.getElementById('runs-panel');
    if (!host) return;
    const list = host.querySelector('.runs-list');
    if (!list) return;
    load(list);
    setInterval(() => {
      if (document.hidden) return;
      load(list);
    }, 15000);
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', mount);
  } else {
    mount();
  }
})();
