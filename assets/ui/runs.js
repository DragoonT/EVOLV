/* EVOLV durable execution runs — an interactive inspector backed by /api/runs.
   The backend owns all lifecycle truth; this file only reflects it.

   Layout contract with index.html / app.js (do not rename):
   - ids: runs-panel, runs-count, runs-list, run-modal, run-modal-title,
     run-modal-status, run-modal-meta, run-modal-refresh, run-modal-close,
     run-objective-box, run-objective-text, run-stats-grid,
     run-tab-tasks-count, run-tab-events-count, run-tasks-list,
     run-events-list, run-view-tasks, run-view-journal
   - attrs: [data-run-tab] tab buttons */
(function () {
  'use strict';

  const phaseLabels = {
    queued: 'queued', planning: 'planning', running: 'running', waiting: 'waiting',
    verifying: 'verifying', paused: 'paused', completed: 'completed',
    failed: 'failed', cancelled: 'cancelled',
  };

  /* Scan order for the runs list: active work first, then needs-attention,
     then queued, terminal states last. Statuses not listed fall into "other". */
  const statusGroups = [
    { key: 'active',    label: 'Active',            statuses: ['running', 'planning', 'verifying'] },
    { key: 'waiting',   label: 'Waiting',           statuses: ['waiting', 'paused'] },
    { key: 'queued',    label: 'Queued',            statuses: ['queued'] },
    { key: 'completed', label: 'Completed',         statuses: ['completed'] },
    { key: 'ended',     label: 'Failed / Cancelled', statuses: ['failed', 'cancelled'] },
    { key: 'other',     label: 'Other',             statuses: [] },
  ];
  const groupOfStatus = (() => {
    const byStatus = {};
    for (const g of statusGroups) for (const s of g.statuses) byStatus[s] = g.key;
    return (status) => byStatus[status] || 'other';
  })();

  const esc = (s) => String(s == null ? '' : s).replace(/[&<>"']/g, (c) => ({
    '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;'
  }[c]));

  let currentRunId = null;

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

  /* ---- time formatting: one shared pair of formatters -------------------
     timeAgo(iso)  → every relative timestamp in this file goes through here.
     formatDateTime(iso) → absolute timestamps ("Started …", hover titles). */

  function timeAgo(iso) {
    if (!iso) return '';
    const t = new Date(iso).getTime();
    if (isNaN(t)) return iso;
    const diff = Math.max(0, Math.floor((Date.now() - t) / 1000));
    if (diff < 10) return 'just now';
    if (diff < 60) return diff + 's ago';
    if (diff < 3600) return Math.floor(diff / 60) + 'm ago';
    if (diff < 86400) return Math.floor(diff / 3600) + 'h ago';
    return Math.floor(diff / 86400) + 'd ago';
  }

  function formatDateTime(iso) {
    if (!iso) return '';
    const d = new Date(iso);
    if (isNaN(d.getTime())) return iso;
    return d.toLocaleString([], {
      month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit', second: '2-digit'
    });
  }

  /* A relative-time span that keeps the absolute time as its hover title. */
  function relTime(iso) {
    const text = timeAgo(iso);
    const span = el('span', null, text);
    if (text && text !== iso) {
      const abs = formatDateTime(iso);
      if (abs) span.title = abs;
    }
    return span;
  }

  /* ---- consistent idle states -------------------------------------------
     One helper for every loading / empty / error box this panel renders, so
     the wording and semantics stay uniform. Reuses the existing style.css
     classes run-loading / run-empty (compact mode reuses runs-empty). */
  function stateBox(kind, text, compact) {
    const cls = compact ? 'runs-empty' : (kind === 'loading' ? 'run-loading' : 'run-empty');
    const box = el('div', cls, text);
    box.setAttribute('role', kind === 'error' ? 'alert' : 'status');
    return box;
  }

  /* ---- runs list (sidebar panel) ---------------------------------------- */

  function render(container, runs) {
    container.replaceChildren();
    if (!runs.length) {
      container.append(stateBox('empty', 'No durable runs yet. Long assignments create one.', true));
      return;
    }

    // Bucket runs by scan group; original order preserved inside each group.
    const buckets = new Map();
    for (const run of runs) {
      const key = groupOfStatus(run.status);
      if (!buckets.has(key)) buckets.set(key, []);
      buckets.get(key).push(run);
    }

    for (const g of statusGroups) {
      const group = buckets.get(g.key);
      if (!group || !group.length) continue;

      const section = el('div', 'runs-group');
      section.style.display = 'flex';
      section.style.flexDirection = 'column';
      section.style.gap = '6px';

      const head = el('div', 'runs-group-head');
      head.style.display = 'flex';
      head.style.alignItems = 'center';
      head.style.justifyContent = 'space-between';
      head.style.margin = '6px 2px 1px';
      head.style.color = 'var(--dim)';
      head.style.font = '600 9px/1 var(--mono)';
      head.style.textTransform = 'uppercase';
      head.style.letterSpacing = '.08em';
      head.append(el('span', null, g.label));
      const count = el('span', 'runs-group-count', String(group.length));
      count.style.font = '600 9px/1 var(--mono)';
      count.style.border = '1px solid var(--line-soft)';
      count.style.borderRadius = '999px';
      count.style.padding = '1px 6px';
      head.append(count);
      section.append(head);

      for (const run of group) section.append(runCard(run));
      container.append(section);
    }
  }

  function runCard(run) {
    const card = el('article', 'run-card run-' + run.status);
    card.tabIndex = 0;
    card.setAttribute('role', 'button');
    card.setAttribute('aria-label', 'Open details for run ' + (run.title || run.id));

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
    const started = relTime(run.created_at || run.updated_at);
    if (started.textContent) {
      started.textContent = 'started ' + started.textContent;
      meta.append(started);
    }
    if (run.worktree && run.worktree.active) {
      const wt = run.worktree;
      const tag = el('span', 'run-worktree-tag', '🌿 ' + (wt.branch || 'isolated'));
      if (wt.merged) tag.textContent += ' (merged)';
      meta.append(tag);
    }
    card.append(meta);

    const actions = el('div', 'run-actions');
    if (run.status === 'running' || run.status === 'planning' || run.status === 'queued') {
      const cancelBtn = el('button', 'run-btn run-btn-danger', 'Cancel');
      cancelBtn.type = 'button';
      cancelBtn.onclick = async (ev) => {
        ev.stopPropagation();
        cancelBtn.disabled = true;
        try {
          await fetch('/api/runs/cancel', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ id: run.id }),
          });
          load(card.closest('.runs-list') || card.parentElement);
        } catch (e) {
          cancelBtn.disabled = false;
        }
      };
      actions.append(cancelBtn);
    } else if (run.status === 'failed' || run.status === 'cancelled' || (p.failed && p.failed > 0)) {
      const retryBtn = el('button', 'run-btn', 'Retry');
      retryBtn.type = 'button';
      retryBtn.onclick = async (ev) => {
        ev.stopPropagation();
        retryBtn.disabled = true;
        try {
          await fetch('/api/runs/retry', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ id: run.id }),
          });
          load(card.closest('.runs-list') || card.parentElement);
        } catch (e) {
          retryBtn.disabled = false;
        }
      };
      actions.append(retryBtn);
    }
    if (actions.children.length) card.append(actions);

    const open = () => openRun(run.id);
    card.addEventListener('click', open);
    // Keyboard parity: Enter and Space trigger the exact same handler as click.
    card.addEventListener('keydown', (e) => {
      if (e.key === 'Enter' || e.key === ' ') {
        e.preventDefault();
        open();
      }
    });
    return card;
  }

  /* ---- run detail modal -------------------------------------------------- */

  async function openRun(runId) {
    if (!runId) return;
    currentRunId = runId;
    const modal = document.getElementById('run-modal');
    if (!modal) return;

    modal.classList.add('open');
    const titleEl = document.getElementById('run-modal-title');
    const statusEl = document.getElementById('run-modal-status');
    const metaEl = document.getElementById('run-modal-meta');
    const tasksList = document.getElementById('run-tasks-list');
    const eventsList = document.getElementById('run-events-list');

    if (titleEl) titleEl.textContent = 'Loading run…';
    if (statusEl) {
      statusEl.className = 'run-badge';
      statusEl.textContent = '…';
    }
    if (metaEl) metaEl.textContent = 'Fetching state from /api/runs/detail…';
    if (tasksList) tasksList.replaceChildren(stateBox('loading', 'Loading task graph…'));
    if (eventsList) eventsList.replaceChildren(stateBox('loading', 'Loading journal events…'));

    try {
      const [detailRes, eventsRes] = await Promise.all([
        fetch('/api/runs/detail?id=' + encodeURIComponent(runId), { cache: 'no-store' }),
        fetch('/api/runs/events?id=' + encodeURIComponent(runId), { cache: 'no-store' }),
      ]);

      if (!detailRes.ok) throw new Error('HTTP ' + detailRes.status + ' on detail');
      const detail = await detailRes.json();
      let events = [];
      if (eventsRes.ok) {
        const eventsData = await eventsRes.json();
        events = eventsData.events || [];
      }

      renderRunDetail(detail, events);
    } catch (err) {
      if (tasksList) tasksList.replaceChildren(stateBox('error', 'Failed to load run: ' + err.message));
      if (eventsList) eventsList.replaceChildren(stateBox('error', 'Journal unavailable.'));
    }
  }

  function renderRunDetail(detail, events) {
    const run = detail.run || {};
    const progress = detail.progress || {};
    const blockedIds = new Set(detail.blocked || []);
    const readyTasks = new Set((detail.ready || []).map((t) => t.id));

    // Header info
    const titleEl = document.getElementById('run-modal-title');
    const statusEl = document.getElementById('run-modal-status');
    const metaEl = document.getElementById('run-modal-meta');
    if (titleEl) titleEl.textContent = run.title || run.id;
    if (statusEl) {
      statusEl.className = 'run-badge run-' + run.status;
      statusEl.textContent = phaseLabels[run.status] || run.status;
    }
    if (metaEl) {
      const parts = ['ID: ' + run.id];
      if (run.created_at) parts.push('Started ' + formatDateTime(run.created_at));
      if (run.closed_at) {
        parts.push('Finished ' + formatDateTime(run.closed_at));
      } else if (run.updated_at) {
        parts.push('Updated ' + timeAgo(run.updated_at));
      }
      metaEl.textContent = parts.join(' · ');
    }

    // Objective callout
    const objBox = document.getElementById('run-objective-box');
    const objText = document.getElementById('run-objective-text');
    if (objBox && objText) {
      if (run.objective) {
        objBox.hidden = false;
        objText.textContent = run.objective;
      } else {
        objBox.hidden = true;
      }
    }

    // Stats grid
    const statsGrid = document.getElementById('run-stats-grid');
    if (statsGrid) {
      const total = progress.total || (run.tasks || []).length || 0;
      const completed = progress.completed || 0;
      const failed = progress.failed || 0;
      const active = (progress.running || 0) + (progress.verifying || 0) + (progress.planning || 0);
      const blocked = blockedIds.size;
      const ready = readyTasks.size;

      statsGrid.innerHTML = `
        <span class="run-stat-item">Tasks: <b>${total}</b></span>
        <span class="run-stat-item stat-ok">Completed: <b>${completed}</b></span>
        ${failed ? `<span class="run-stat-item stat-err">Failed: <b>${failed}</b></span>` : ''}
        ${active ? `<span class="run-stat-item stat-active">Active: <b>${active}</b></span>` : ''}
        ${ready ? `<span class="run-stat-item">Ready: <b>${ready}</b></span>` : ''}
        ${blocked ? `<span class="run-stat-item stat-err">Blocked: <b>${blocked}</b></span>` : ''}
      `;
    }

    // Tab counts
    const tabTasksCount = document.getElementById('run-tab-tasks-count');
    const tabEventsCount = document.getElementById('run-tab-events-count');
    if (tabTasksCount) tabTasksCount.textContent = String((run.tasks || []).length);
    if (tabEventsCount) tabEventsCount.textContent = String(events.length);

    // Render Tasks list
    const tasksList = document.getElementById('run-tasks-list');
    if (tasksList) {
      const tasks = run.tasks || [];
      if (!tasks.length) {
        tasksList.replaceChildren(stateBox('empty', 'No tasks defined in this run.'));
      } else {
        tasksList.innerHTML = tasks.map((task) => renderTaskCard(task, blockedIds, readyTasks)).join('');
        // Attach copy buttons for verification output
        tasksList.querySelectorAll('.run-copy-btn').forEach((btn) => {
          btn.addEventListener('click', (e) => {
            e.stopPropagation();
            const text = btn.dataset.copyText || '';
            navigator.clipboard.writeText(text).then(() => {
              const orig = btn.textContent;
              btn.textContent = 'Copied!';
              setTimeout(() => { btn.textContent = orig; }, 1500);
            });
          });
        });
      }
    }

    // Render Journal events
    const eventsList = document.getElementById('run-events-list');
    if (eventsList) {
      if (!events.length) {
        eventsList.replaceChildren(stateBox('empty', 'No journal events recorded for this run.'));
      } else {
        eventsList.innerHTML = events.map((ev) => renderEventRow(ev)).join('');
      }
    }
  }

  function renderTaskCard(task, blockedIds, readyTasks) {
    const isBlocked = blockedIds.has(task.id);
    const isReady = readyTasks.has(task.id);
    const deps = task.depends_on || [];

    const metaItems = [];
    if (task.priority) metaItems.push('P' + task.priority);
    if (task.phase) metaItems.push(esc(task.phase));
    if (task.attempts && task.attempts > 0) metaItems.push(task.attempts + ' attempt' + (task.attempts === 1 ? '' : 's'));
    if (isBlocked) metaItems.push('<b style="color:var(--danger)">BLOCKED</b>');
    else if (isReady) metaItems.push('<b style="color:var(--ok)">READY</b>');

    let depsHtml = '';
    if (deps.length) {
      depsHtml = `<div class="run-task-dep">Depends on: ${deps.map((d) => `<code>${esc(d)}</code>`).join(' ')}</div>`;
    }

    let errorHtml = '';
    if (task.last_error) {
      errorHtml = `<div class="run-task-error">⚠️ ${esc(task.last_error)}</div>`;
    }

    let verifyHtml = '';
    if (task.verification) {
      const v = task.verification;
      const passedClass = v.passed ? 'passed' : 'failed';
      const statusText = v.passed ? '✓ PASSED' : '✕ FAILED (exit ' + v.exit_code + ')';
      const durText = v.duration_ms ? ' · ' + v.duration_ms + 'ms' : '';
      const cmdText = v.command ? esc(v.command) : esc(v.kind || 'check');

      let outputBlock = '';
      if (v.output) {
        outputBlock = `
          <pre class="run-term-output"><code>${esc(v.output.trim())}</code></pre>
        `;
      }

      verifyHtml = `
        <div class="run-verify-box">
          <div class="run-verify-head">
            <span class="run-verify-tag ${passedClass}">${statusText}${durText}</span>
            <span class="run-verify-cmd">${cmdText}</span>
            ${v.output ? `<button class="run-copy-btn" type="button" data-copy-text="${esc(v.output)}">Copy output</button>` : ''}
          </div>
          ${outputBlock}
        </div>
      `;
    }

    return `
      <article class="run-task-card task-${esc(task.status)}">
        <div class="run-task-head">
          <div class="run-task-title-group">
            <span class="run-task-title">${esc(task.title || task.id)}</span>
            <code class="run-task-id">${esc(task.id)}</code>
          </div>
          <span class="run-badge run-${esc(task.status)}">${esc(phaseLabels[task.status] || task.status)}</span>
        </div>
        ${task.objective ? `<div class="run-task-obj">${esc(task.objective)}</div>` : ''}
        ${depsHtml}
        ${errorHtml}
        ${metaItems.length ? `<div class="run-task-meta-row">${metaItems.join(' · ')}</div>` : ''}
        ${verifyHtml}
      </article>
    `;
  }

  function renderEventRow(ev) {
    const seq = '#' + ev.seq;
    const time = timeAgo(ev.at);
    const kindClass = 'k-' + (ev.kind || '').replace('.', '-');
    const taskTag = ev.task ? `<span class="event-task-tag">${esc(ev.task)}</span>` : '';

    let desc = '';
    const data = ev.data;
    if (data && typeof data === 'object') {
      if (ev.kind === 'task.moved') {
        desc = `${esc(data.from || 'unknown')} → ${esc(data.to || 'unknown')}`;
      } else if (ev.kind === 'verify.added') {
        const passed = data.passed ? 'PASSED' : 'FAILED';
        desc = `${esc(data.kind || 'verify')} ${passed} ${data.command ? `(${esc(data.command)})` : ''}`;
      } else if (ev.kind === 'task.error') {
        desc = esc(data.error || 'task error');
      } else if (ev.kind === 'task.attempt') {
        desc = `attempt #${esc(data.attempt || 1)}`;
      } else if (ev.kind === 'run.created') {
        desc = `Run initialized: ${esc(data.title || '')}`;
      } else if (ev.kind === 'run.closed') {
        desc = `Run closed with status ${esc(data.status || 'closed')}`;
      } else if (ev.kind === 'checkpoint') {
        desc = `Checkpoint snapshot at seq #${esc(data.seq || ev.seq)}`;
      } else {
        desc = esc(JSON.stringify(data));
      }
    } else if (typeof data === 'string') {
      desc = esc(data);
    }

    return `
      <div class="run-event-row">
        <span class="event-seq">${esc(seq)}</span>
        <span class="event-kind ${esc(kindClass)}">${esc(ev.kind || 'event')}</span>
        ${taskTag || '<span class="event-seq">—</span>'}
        <span class="event-desc" title="${esc(desc)}${desc ? ' · ' + esc(formatDateTime(ev.at)) : ''}">${desc || esc(time)}</span>
      </div>
    `;
  }

  function closeRun() {
    currentRunId = null;
    const modal = document.getElementById('run-modal');
    if (modal) modal.classList.remove('open');
  }

  /* ---- panel lifecycle ---------------------------------------------------- */

  async function load(container) {
    // Only show a loading box on the very first paint; silent refreshes
    // afterwards must not flash or wipe the list.
    if (!container.childNodes.length) {
      container.append(stateBox('loading', 'Loading runs…', true));
    }
    try {
      const res = await fetch('/api/runs', { cache: 'no-store' });
      if (!res.ok) throw new Error('HTTP ' + res.status);
      const data = await res.json();
      const runs = (data.runs || []).filter((r) => r.title);
      render(container, runs);

      const count = document.getElementById('runs-count');
      const busy = runs.filter((r) => r.status === 'running' || r.status === 'planning' || r.status === 'verifying').length;
      if (container.closest('.sb-sec')) {
        container.closest('.sb-sec').hidden = runs.length === 0;
      }
      if (count) count.textContent = busy ? busy + ' active' : String(runs.length);
    } catch (e) {
      // Backend unreachable: preserve the established quiet-hide behavior for
      // the sidebar section, but leave a trace for honest debugging.
      console.warn('[runs] /api/runs unavailable:', e && e.message);
      const panel = container.closest('.sb-sec');
      if (panel) panel.hidden = true;
      container.replaceChildren();
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

    // Modal controls
    const modal = document.getElementById('run-modal');
    const closeBtn = document.getElementById('run-modal-close');
    const refreshBtn = document.getElementById('run-modal-refresh');

    if (closeBtn) closeBtn.addEventListener('click', closeRun);
    if (refreshBtn) {
      refreshBtn.addEventListener('click', () => {
        if (currentRunId) openRun(currentRunId);
      });
    }

    if (modal) {
      modal.addEventListener('click', (e) => {
        if (e.target === modal) closeRun();
      });
    }

    document.addEventListener('keydown', (e) => {
      if (e.key === 'Escape' && modal && modal.classList.contains('open')) {
        closeRun();
      }
    });

    // Tab switching
    const tabBtns = document.querySelectorAll('[data-run-tab]');
    tabBtns.forEach((btn) => {
      btn.addEventListener('click', () => {
        tabBtns.forEach((b) => b.classList.remove('active'));
        btn.classList.add('active');
        const tab = btn.dataset.runTab;
        const tasksView = document.getElementById('run-view-tasks');
        const journalView = document.getElementById('run-view-journal');
        if (tasksView && journalView) {
          tasksView.hidden = tab !== 'tasks';
          journalView.hidden = tab !== 'journal';
        }
      });
    });

    // Allow clicking the runs count badge in sidebar to open the first run if available
    const countBadge = document.getElementById('runs-count');
    if (countBadge) {
      countBadge.style.cursor = 'pointer';
      countBadge.addEventListener('click', (e) => {
        e.stopPropagation();
        const firstCard = list.querySelector('.run-card');
        if (firstCard) firstCard.click();
      });
    }
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', mount);
  } else {
    mount();
  }
})();
