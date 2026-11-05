/* EVOLV unified toasts — zero-dependency vanilla JS (plain IIFE, no build step).
 *
 * API:
 *   EVOLVToasts.show(message, opts) -> { dismiss(), el }
 *     opts = { type: 'ok'|'err'|'info' (default 'info'),
 *              timeout: ms (default 3500, 0 = sticky),
 *              actions: [{ label, onClick }] (optional, max 2 inline buttons) }
 *   EVOLVToasts.success / .error / .info — shorthand for show with the type preset.
 *
 * Coexistence: shares the existing #toasts container with app.js's legacy
 * toast() helper and appends newest at the bottom the same way, but only
 * ever manages its own .evolv-toast elements — it never touches .toast
 * ones, so both systems can run side by side (integrator may migrate later).
 *
 * Safety: guards all DOM access, so this file evaluates cleanly in a bare
 * VM where `document` is undefined (API becomes inert stubs).
 */
(function () {
  'use strict';

  var MAX_VISIBLE = 5;
  var DEFAULT_TIMEOUT = 3500;
  var EXIT_MS = 300;
  var MAX_ACTIONS = 2;
  var TYPE_ROLES = { ok: 'status', err: 'alert', info: 'status' };
  var TYPE_ICONS = { ok: '✓', err: '!', info: 'ℹ' };

  var CSS_TEXT = [
    '.evolv-toast {',
    '  display: flex; align-items: flex-start; gap: 8px;',
    '  background: var(--bg3); border: 1px solid var(--line);',
    '  border-left: 3px solid var(--accent);',
    '  color: var(--text); font-family: var(--font); font-size: var(--fs-sm);',
    '  padding: 10px 14px; border-radius: var(--r-sm); max-width: 380px;',
    '  box-shadow: 0 8px 26px rgba(0, 0, 0, 0.35);',
    '  cursor: pointer; opacity: 1; transform: translateX(0);',
    '  transition: opacity 0.25s ease, transform 0.25s ease;',
    '}',
    '.evolv-toast--enter, .evolv-toast--leave { opacity: 0; transform: translateX(24px); }',
    '.evolv-toast--ok { border-left-color: var(--ok); }',
    '.evolv-toast--err { border-left-color: var(--danger); }',
    '.evolv-toast--info { border-left-color: var(--accent); }',
    '.evolv-toast-ico { flex: none; font-weight: 700; line-height: 1.4; color: var(--dim); }',
    '.evolv-toast--ok .evolv-toast-ico { color: var(--ok); }',
    '.evolv-toast--err .evolv-toast-ico { color: var(--danger); }',
    '.evolv-toast--info .evolv-toast-ico { color: var(--accent); }',
    '.evolv-toast-msg { word-break: break-word; }',
    '.evolv-toast-acts { display: flex; gap: 6px; margin-top: 8px; }',
    '.evolv-toast-act {',
    '  background: var(--bg4); border: 1px solid var(--line); color: var(--text);',
    '  font-family: var(--font); font-size: var(--fs-xs);',
    '  padding: 3px 10px; border-radius: var(--r-full); cursor: pointer;',
    '}',
    '.evolv-toast-act:hover { border-color: var(--dim); }',
    '.evolv-toast-mount {',
    '  position: fixed; top: 16px; right: 16px; z-index: 120;',
    '  display: flex; flex-direction: column; gap: 8px; align-items: flex-end;',
    '}',
    '@media (prefers-reduced-motion: reduce) {',
    '  .evolv-toast { transition: none !important; transform: none !important; }',
    '  .evolv-toast--enter, .evolv-toast--leave { transform: none !important; }',
    '}'
  ].join('\n');

  function doc() {
    return (typeof document !== 'undefined' && document) || null;
  }

  function inertHandle() {
    return { dismiss: function () {}, el: null };
  }

  function withType(type, opts) {
    var o = {};
    if (opts) { for (var k in opts) { if (Object.prototype.hasOwnProperty.call(opts, k)) o[k] = opts[k]; } }
    o.type = type;
    return o;
  }

  /* ---- module state (browser only) ---- */
  var styleInjected = false;
  var active = []; // state objects, oldest first

  function injectStyle() {
    var d = doc();
    if (!d || styleInjected) return;
    if (d.getElementById('evolv-toast-style')) { styleInjected = true; return; }
    var s = d.createElement('style');
    s.id = 'evolv-toast-style';
    s.textContent = CSS_TEXT;
    (d.head || d.getElementsByTagName('head')[0] || d.documentElement).appendChild(s);
    styleInjected = true;
  }

  function getMount() {
    var d = doc();
    if (!d) return null;
    var el = d.getElementById('toasts');
    if (!el) {
      // index.html normally provides <div id="toasts" aria-live="polite">;
      // create an equivalent mount if it is missing.
      el = d.createElement('div');
      el.id = 'toasts';
      el.className = 'evolv-toast-mount';
      el.setAttribute('aria-live', 'polite');
      (d.body || d.getElementsByTagName('body')[0] || d.documentElement).appendChild(el);
    }
    return el;
  }

  function dismiss(state) {
    if (state.gone) return;
    state.gone = true;
    clearTimeout(state.timer);
    var i = active.indexOf(state);
    if (i >= 0) active.splice(i, 1);
    var el = state.el;
    if (!el) return;
    el.classList.remove('evolv-toast--enter');
    el.classList.add('evolv-toast--leave');
    var done = function () {
      if (el && el.parentNode) el.parentNode.removeChild(el);
    };
    if (typeof el.addEventListener === 'function') {
      el.addEventListener('transitionend', done, { once: true });
    }
    setTimeout(done, EXIT_MS + 80); // fallback (transitionend may not fire under reduced motion)
  }

  function prune() {
    while (active.length > MAX_VISIBLE) dismiss(active[0]);
  }

  function show(message, opts) {
    opts = opts || {};
    var type = Object.prototype.hasOwnProperty.call(TYPE_ROLES, opts.type) ? opts.type : 'info';
    var timeout = typeof opts.timeout === 'number' ? opts.timeout : DEFAULT_TIMEOUT;
    var d = doc();
    if (!d) return inertHandle();

    injectStyle();
    var mount = getMount();
    if (!mount) return inertHandle();

    var state = { el: null, timer: null, gone: false, remaining: timeout, startedAt: 0 };

    var el = d.createElement('div');
    el.className = 'evolv-toast evolv-toast--' + type + ' evolv-toast--enter';
    el.setAttribute('role', TYPE_ROLES[type]);
    el.addEventListener('click', function () { dismiss(state); }); // plain click dismisses

    var ico = d.createElement('span');
    ico.className = 'evolv-toast-ico';
    ico.setAttribute('aria-hidden', 'true');
    ico.textContent = TYPE_ICONS[type] || TYPE_ICONS.info;
    el.appendChild(ico);

    var body = d.createElement('div');
    body.className = 'evolv-toast-body';

    var msg = d.createElement('div');
    msg.className = 'evolv-toast-msg';
    msg.textContent = message == null ? '' : String(message); // plain text, never innerHTML
    body.appendChild(msg);

    var actions = (opts.actions && opts.actions.length) ? opts.actions.slice(0, MAX_ACTIONS) : [];
    if (actions.length) {
      var acts = d.createElement('div');
      acts.className = 'evolv-toast-acts';
      actions.forEach(function (a) {
        var b = d.createElement('button');
        b.type = 'button';
        b.className = 'evolv-toast-act';
        b.textContent = (a && a.label != null) ? String(a.label) : 'OK';
        b.addEventListener('click', function (e) {
          e.stopPropagation(); // action buttons never trigger dismiss-by-click
          if (a && typeof a.onClick === 'function') a.onClick(handle);
          dismiss(state);
        });
        acts.appendChild(b);
      });
      body.appendChild(acts);
    }
    el.appendChild(body);

    mount.appendChild(el); // newest at the bottom, matching app.js's toast()
    state.el = el;

    var handle = { el: el, dismiss: function () { dismiss(state); } };

    // auto-dismiss with hover-pause (remaining-time bookkeeping)
    function startTimer(ms) {
      clearTimeout(state.timer);
      if (timeout > 0 && ms > 0) state.timer = setTimeout(function () { dismiss(state); }, ms);
    }
    state.startedAt = Date.now();
    startTimer(timeout);
    el.addEventListener('mouseenter', function () {
      if (state.gone || timeout <= 0) return;
      clearTimeout(state.timer);
      state.remaining = Math.max(0, state.remaining - (Date.now() - state.startedAt));
    });
    el.addEventListener('mouseleave', function () {
      if (state.gone || timeout <= 0) return;
      state.startedAt = Date.now();
      startTimer(state.remaining);
    });

    active.push(state);
    prune(); // >5 visible: oldest auto-dismisses first

    // entry: subtle slide/fade via CSS transition only (next frame, so it animates)
    if (typeof requestAnimationFrame === 'function') {
      requestAnimationFrame(function () {
        requestAnimationFrame(function () { el.classList.remove('evolv-toast--enter'); });
      });
    } else {
      setTimeout(function () { el.classList.remove('evolv-toast--enter'); }, 16);
    }

    return handle;
  }

  var api = {
    show: show,
    success: function (message, opts) { return show(message, withType('ok', opts)); },
    error: function (message, opts) { return show(message, withType('err', opts)); },
    info: function (message, opts) { return show(message, withType('info', opts)); }
  };

  var root = (typeof window !== 'undefined' && window) ||
             (typeof globalThis !== 'undefined' && globalThis) || {};
  root.EVOLVToasts = api;
})();
