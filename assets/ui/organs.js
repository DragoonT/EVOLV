/* EVOLV organs — the improvement ledger, not another chat panel.
   Every grown tool, skill, prompt, plugin and component, with its real health
   and a one-click rollback. Data comes from /api/organs (never invented). */
(function () {
  'use strict';

  const $ = (s) => document.querySelector(s);
  const esc = (s) => String(s == null ? '' : s).replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));

  const VERDICT_LABEL = {
    healthy: 'healthy', unproven: 'unproven', flaky: 'flaky',
    broken: 'broken', stale: 'retired',
  };

  // Health tiers, worst first — mirrors internal/genome HealthReport ordering
  // (broken, flaky, unproven, healthy, stale) so the ledger reads the same on
  // both sides of the API. Unknown verdicts sink below stale.
  const HEALTH_TIER = { broken: 0, flaky: 1, unproven: 2, healthy: 3, stale: 4 };
  const UNKNOWN_TIER = 5;

  function healthTier(verdict) {
    return Object.prototype.hasOwnProperty.call(HEALTH_TIER, verdict) ? HEALTH_TIER[verdict] : UNKNOWN_TIER;
  }

  // Stable, deterministic order: health tier, then name, then kind, then
  // version — so the same data always renders in the same row order.
  function sortedOrgans(organs) {
    return organs.slice().sort((a, b) => {
      const tier = healthTier(a.verdict) - healthTier(b.verdict);
      if (tier) return tier;
      const an = String(a.name || '');
      const bn = String(b.name || '');
      if (an !== bn) return an < bn ? -1 : 1;
      const ak = String(a.kind || '');
      const bk = String(b.kind || '');
      if (ak !== bk) return ak < bk ? -1 : 1;
      return (a.version || 0) - (b.version || 0);
    });
  }

  // Status chips reuse the existing run-badge pill style: active is green,
  // quarantined red, anything else the plain dim badge.
  const STATUS_CHIP_CLASS = { active: 'run-completed', quarantined: 'run-cancelled' };

  let cache = { organs: [] };

  async function load() {
    const list = $('#organs-list');
    if (!list) return;
    list.setAttribute('aria-busy', 'true');
    try {
      const res = await fetch('/api/organs', { cache: 'no-store' });
      if (!res.ok) throw new Error('HTTP ' + res.status);
      cache = await res.json();
      render();
    } catch (e) {
      list.innerHTML = '<div class="organ-empty"><b>Organs unavailable.</b><br>' + esc(e.message) + '</div>';
    } finally {
      list.removeAttribute('aria-busy');
    }
  }

  function render() {
    const list = $('#organs-list');
    const summary = $('#organs-summary');
    const organs = sortedOrgans(cache.organs || []);
    if (summary) {
      const s = cache.summary || {};
      summary.textContent = organs.length
        ? (s.needing_attention ? s.needing_attention + ' need attention · ' + s.active + ' active' : s.active + ' active')
        : '';
    }
    if (!organs.length) {
      list.innerHTML = '<div class="organ-empty"><b>Nothing grown yet.</b><br>' +
        'Ask EVOLV to grow a tool, skill or plugin — it will appear here with a version and a health record.</div>';
      return;
    }
    list.innerHTML = organs.map((o) => card(o)).join('');
    list.querySelectorAll('[data-rollback]').forEach((btn) => {
      btn.addEventListener('click', () => openRollback(btn.dataset.kind, btn.dataset.name));
    });
    list.querySelectorAll('[data-retire]').forEach((btn) => {
      btn.addEventListener('click', () => setStatus(btn.dataset.kind, btn.dataset.name, btn.dataset.status));
    });
  }

  function bytesLabel(n) {
    if (!n) return 'empty';
    if (n < 1024) return n + ' B';
    return (Math.round(n / 1024 * 10) / 10) + ' KB';
  }

  function card(o) {
    const rate = o.success_rate < 0 ? '—' : o.success_rate + '%';
    const usage = o.uses ? o.uses + ' uses · ' + o.failures + ' failed' : 'never used';
    const actions = [];
    if (o.version > 1) {
      actions.push('<button class="organ-btn" data-rollback data-kind="' + esc(o.kind) + '" data-name="' + esc(o.name) + '">versions</button>');
    }
    if (o.status === 'active') {
      actions.push('<button class="organ-btn danger" data-retire data-kind="' + esc(o.kind) + '" data-name="' + esc(o.name) + '" data-status="quarantined">quarantine</button>');
    } else {
      actions.push('<button class="organ-btn" data-retire data-kind="' + esc(o.kind) + '" data-name="' + esc(o.name) + '" data-status="active">re-activate</button>');
    }
    const status = String(o.status || 'active');
    const statusCls = STATUS_CHIP_CLASS[status] ? ' ' + STATUS_CHIP_CLASS[status] : '';
    const verdict = esc(VERDICT_LABEL[o.verdict] || o.verdict || 'unknown');
    return '<article class="organ-card v-' + esc(o.verdict) + '" tabindex="0"' +
      ' aria-label="' + esc(o.kind) + ': ' + esc(o.name) + ' v' + o.version + ', ' + verdict + ', ' + esc(status) + '">' +
      '<div class="organ-top"><span class="organ-kind">' + esc(o.kind) + '</span>' +
      '<b class="organ-name">' + esc(o.name) + '</b>' +
      '<span class="organ-ver">v' + o.version + '</span>' +
      '<span class="run-badge' + statusCls + '">' + esc(status) + '</span>' +
      '<span class="organ-verdict run-badge">' + verdict + '</span></div>' +
      '<div class="organ-meta"><span class="organ-rate">' + esc(rate) + ' success</span>' +
      '<span>' + esc(usage) + '</span>' +
      (o.reverts ? '<span>' + o.reverts + ' rollback' + (o.reverts === 1 ? '' : 's') + '</span>' : '') +
      '</div>' +
      '<div class="organ-actions">' + actions.join('') + '</div></article>';
  }

  async function openRollback(kind, name) {
    const panel = $('#organ-versions');
    if (!panel) return;
    panel.hidden = false;
    panel.innerHTML = '<div class="organ-empty">loading versions…</div>';
    try {
      const res = await fetch('/api/organs/versions?kind=' + encodeURIComponent(kind) + '&name=' + encodeURIComponent(name), { cache: 'no-store' });
      const data = await res.json();
      if (!res.ok) throw new Error(data.error || 'failed');
      const rows = (data.versions || []).map((v) =>
        '<div class="organ-version' + (v.current ? ' current' : '') + '">' +
        '<span>v' + v.version + (v.current ? ' · current' : '') + '</span>' +
        '<span class="organ-bytes">' + bytesLabel(v.bytes) + '</span>' +
        (v.current ? '' : '<button class="organ-btn" data-restore="' + v.version + '">restore</button>') +
        '</div>').join('');
      panel.innerHTML =
        '<div class="organ-versions-head"><b>' + esc(kind) + ' · ' + esc(name) + '</b>' +
        '<button class="organ-btn" id="organ-versions-close">close</button></div>' + rows;
      $('#organ-versions-close').addEventListener('click', () => { panel.hidden = true; });
      panel.querySelectorAll('[data-restore]').forEach((btn) => {
        btn.addEventListener('click', () => rollback(kind, name, Number(btn.dataset.restore)));
      });
    } catch (e) {
      panel.innerHTML = '<div class="organ-empty">' + esc(e.message) + '</div>';
    }
  }

  async function rollback(kind, name, version) {
    // Rolling back live code is deliberate: say what will happen first.
    if (!confirm('Restore ' + kind + ' "' + name + '" to v' + version + '?\n\nThe current version stays archived, so this can be undone.')) return;
    try {
      const res = await fetch('/api/organs/rollback', {
        method: 'POST', headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ kind, name, version, confirm: true }),
      });
      const data = await res.json();
      if (!res.ok) throw new Error(data.error || 'rollback failed');
      if (window.evolvToast) window.evolvToast('↩ restored ' + esc(name) + ' to v' + version + ' (now v' + data.asset.version + ')');
      $('#organ-versions').hidden = true;
      load();
    } catch (e) {
      if (window.evolvToast) window.evolvToast('⚠️ ' + esc(e.message));
    }
  }

  async function setStatus(kind, name, status) {
    try {
      const res = await fetch('/api/organs/status', {
        method: 'POST', headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ kind, name, status }),
      });
      const data = await res.json();
      if (!res.ok) throw new Error(data.error || 'failed');
      if (window.evolvToast) window.evolvToast(status === 'active' ? '✅ ' + esc(name) + ' re-activated' : '⏸ ' + esc(name) + ' quarantined');
      load();
    } catch (e) {
      if (window.evolvToast) window.evolvToast('⚠️ ' + esc(e.message));
    }
  }

  function mount() {
    const panel = $('#organs-modal');
    // Keyboard access on rows: each organ card is focusable; Enter/Space on the
    // card itself (not a nested button) steps focus into its first action.
    const list = $('#organs-list');
    if (list) list.addEventListener('keydown', (e) => {
      if (e.key !== 'Enter' && e.key !== ' ') return;
      const row = e.target;
      if (row !== list && row.classList && row.classList.contains('organ-card')) {
        const first = row.querySelector('button');
        if (first) {
          e.preventDefault();
          first.focus();
        }
      }
    });
    const openers = document.querySelectorAll('[data-open-organs]');
    openers.forEach((btn) => btn.addEventListener('click', () => {
      panel.classList.add('open');
      load();
    }));
    const close = $('#organs-close');
    if (close) close.addEventListener('click', () => panel.classList.remove('open'));
    if (panel) panel.addEventListener('click', (e) => { if (e.target === panel) panel.classList.remove('open'); });
    document.addEventListener('keydown', (e) => {
      if (e.key === 'Escape' && panel && panel.classList.contains('open')) panel.classList.remove('open');
    });
  }

  if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', mount);
  else mount();
})();
