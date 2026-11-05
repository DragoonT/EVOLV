/* EVOLV — slash-command palette (zero-dependency, plain <script>, no modules).
 *
 * Contract (wired up by app.js):
 *   EVOLVPalette.attach(inputEl, opts) -> handle { destroy(), isOpen() }
 *     opts = {
 *       getCommands: function -> [{ name, description, args?, hint? }],
 *       onSubmit:    function(cmdName, restText) -> true if consumed,
 *                                                  false/undefined to fall back
 *     }
 *   EVOLVPalette.fuzz(query, names) -> names sorted best-first (pure).
 *
 * Integration notes for the wiring side:
 *   - Attach this palette BEFORE adding the app's own Enter-to-send keydown
 *     handler on the same input; accepted palette keys get
 *     ev.__evolvPaletteHandled = true and `handle.isOpen()` reports state,
 *     so the app handler can defer.
 *   - Ranking tiers: exact prefix > word-boundary prefix > contiguous
 *     substring > sparse subsequence. Case-insensitive. Ties break on
 *     shorter name, then the caller's list order (stable, so "/ne" can
 *     rank "/new" first when the app lists it before "/net").
 *
 * The file evaluates in a bare JS VM (document undefined) without throwing:
 * every DOM touch lives inside attach() and its closures.
 */
(function () {
  'use strict';

  /* ======================================================================
   * Pure functions — no DOM access below this line.
   * ====================================================================== */

  var TIER_EXACT = 4000;  // query is a prefix of the whole name
  var TIER_WORD = 3000;   // query starts at an in-name word boundary (-_. : / space)
  var TIER_SUBSTR = 2000; // query appears contiguously anywhere
  var TIER_SPARSE = 1000; // query chars appear in order, non-contiguous

  function clamp(v, lo, hi) {
    return v < lo ? lo : (v > hi ? hi : v);
  }

  function seq(from, len) {
    var out = [];
    for (var i = 0; i < len; i++) out.push(from + i);
    return out;
  }

  function isBoundaryChar(ch) {
    return ch === '-' || ch === '_' || ch === ' ' || ch === '.' ||
      ch === '/' || ch === ':' || ch === '+';
  }

  // Split a raw input value into command tokens.
  // Returns null when the value is not a slash command; otherwise
  // { name, rest, hasArgs } where `name` may be '' while the user types "/".
  function tokenize(value) {
    var s = String(value == null ? '' : value);
    if (s.charAt(0) !== '/') return null;
    var body = s.slice(1);
    var sp = body.indexOf(' ');
    if (sp === -1) return { name: body, rest: '', hasArgs: false };
    return { name: body.slice(0, sp), rest: body.slice(sp + 1), hasArgs: true };
  }

  // Greedy earliest-match subsequence positions, or null when impossible.
  function sparsePositions(n, q) {
    var positions = [];
    var from = 0;
    for (var i = 0; i < q.length; i++) {
      var at = n.indexOf(q.charAt(i), from);
      if (at === -1) return null;
      positions.push(at);
      from = at + 1;
    }
    return positions;
  }

  function adjacencyBonus(positions) {
    var c = 0;
    for (var i = 1; i < positions.length; i++) {
      if (positions[i] === positions[i - 1] + 1) c++;
    }
    return c;
  }

  // Score one name against one query (both case-insensitive).
  // Returns { score, positions } — positions are char indices into the
  // name for highlighting — or null when there is no match at all.
  function matchName(name, query) {
    if (!query) return { score: 0, positions: [] };
    var n = String(name).toLowerCase();
    var q = String(query).toLowerCase();
    if (!n || q.length > n.length) return null;

    var idx = n.indexOf(q);
    if (idx === 0) {
      var full = q.length === n.length;
      return {
        score: TIER_EXACT + 120 + (full ? 300 : 0),
        positions: seq(0, q.length)
      };
    }
    if (idx > 0 && isBoundaryChar(n.charAt(idx - 1))) {
      return {
        score: TIER_WORD + 120 - clamp(idx, 0, 60) * 2,
        positions: seq(idx, q.length)
      };
    }
    if (idx > 0) {
      return {
        score: TIER_SUBSTR + 90 - clamp(idx, 0, 45) * 2,
        positions: seq(idx, q.length)
      };
    }
    var positions = sparsePositions(n, q);
    if (!positions) return null;
    var span = positions[positions.length - 1] - positions[0] + 1;
    var gaps = span - q.length; // 0 = perfectly packed match
    return {
      score: TIER_SPARSE +
        60 - clamp(positions[0], 0, 30) * 2 +
        adjacencyBonus(positions) * 12 +
        clamp(20 - gaps, 0, 20) * 2,
      positions: positions
    };
  }

  // Tie-break order: higher score, then shorter name, then the caller's
  // original list order (stable — lets the app float its most common
  // commands up, so "/ne" ranks "/new" before "/net"), and alphabetical
  // only as an unreachable-in-practice final fallback.
  function rankCompare(a, b) {
    if (a.score !== b.score) return b.score - a.score;
    if (a.name.length !== b.name.length) return a.name.length - b.name.length;
    if (a.index !== b.index) return a.index - b.index;
    return a.name < b.name ? -1 : (a.name > b.name ? 1 : 0);
  }

  // Internal ranked search: [{ name, score, positions }] sorted best-first.
  // Empty query keeps the caller's original order (no highlight).
  function rank(query, names) {
    var list = Array.isArray(names) ? names : [];
    var out = [];
    if (!query) {
      for (var i = 0; i < list.length; i++) {
        if (list[i] == null) continue;
        out.push({ name: String(list[i]), score: 0, positions: [], index: i });
      }
      return out;
    }
    for (var j = 0; j < list.length; j++) {
      if (list[j] == null) continue;
      var m = matchName(String(list[j]), query);
      if (m) out.push({ name: String(list[j]), score: m.score, positions: m.positions, index: j });
    }
    out.sort(rankCompare);
    return out;
  }

  // Exported pure scorer: names sorted best-first, non-matches dropped.
  function fuzz(query, names) {
    var ranked = rank(query, names);
    var out = [];
    for (var i = 0; i < ranked.length; i++) out.push(ranked[i].name);
    return out;
  }

  /* ======================================================================
   * DOM layer — everything below only runs inside attach().
   * ====================================================================== */

  var STYLE_ID = 'evolv-palette-style';
  var ROOT_CLASS = 'evolv-palette-root';
  var OPEN_CLASS = 'evolv-palette-open';
  var ITEM_CLASS = 'evolv-palette-item';
  var ACTIVE_CLASS = 'evolv-palette-active';
  var MAX_ROWS = 8;
  var BLUR_DELAY_MS = 140;
  var styleRefs = 0;
  var uidCounter = 0;

  function styleCss() {
    return [
      '.' + ROOT_CLASS + '{position:fixed;z-index:5000;display:none;min-width:260px;',
      'background:var(--bg3);border:1px solid var(--line);border-radius:var(--r-sm,6px);',
      'box-shadow:0 10px 28px rgba(0,0,0,.45);padding:4px;font-size:13px;line-height:1.35;',
      'color:var(--text);max-height:300px;overflow-y:auto;}',
      '.' + ROOT_CLASS + '.' + OPEN_CLASS + '{display:block;}',
      '.' + ITEM_CLASS + '{display:flex;align-items:baseline;gap:8px;padding:6px 8px;',
      'border-radius:var(--r-sm,6px);cursor:pointer;white-space:nowrap;}',
      '.' + ITEM_CLASS + '.' + ACTIVE_CLASS + '{background:var(--bg4);}',
      '.evolv-palette-cmd{font-weight:600;white-space:nowrap;flex-shrink:0;}',
      '.evolv-palette-slash{color:var(--faint);font-weight:400;}',
      '.evolv-palette-hl{color:var(--accent);font-weight:800;}',
      '.evolv-palette-desc{color:var(--dim);font-size:12px;flex:1 1 auto;',
      'overflow:hidden;text-overflow:ellipsis;}',
      '.evolv-palette-meta{color:var(--faint);font-size:11px;white-space:nowrap;',
      'flex-shrink:0;margin-left:auto;}'
    ].join('');
  }

  function ensureStyle(doc) {
    var el = doc.getElementById(STYLE_ID);
    if (!el) {
      el = doc.createElement('style');
      el.id = STYLE_ID;
      el.type = 'text/css';
      el.appendChild(doc.createTextNode(styleCss()));
      doc.head.appendChild(el);
    }
    styleRefs++;
    return el;
  }

  function releaseStyle(doc, styleEl) {
    styleRefs--;
    if (styleRefs <= 0 && styleEl && styleEl.parentNode) {
      styleEl.parentNode.removeChild(styleEl);
    }
  }

  function clearChildren(el) {
    while (el.firstChild) el.removeChild(el.firstChild);
  }

  function setCaretToEnd(el) {
    try {
      var len = (el.value || '').length;
      el.setSelectionRange(len, len);
    } catch (err) { /* not focusable / not a text field */ }
  }

  // Resolve timer functions (browser globals; fall back to the window
  // object; degrade to immediate hide when no timers exist at all).
  function getTimers(winObj) {
    if (typeof setTimeout === 'function' && typeof clearTimeout === 'function') {
      return { set: setTimeout, clear: clearTimeout };
    }
    if (winObj && typeof winObj.setTimeout === 'function' &&
        typeof winObj.clearTimeout === 'function') {
      return {
        set: winObj.setTimeout.bind(winObj),
        clear: winObj.clearTimeout.bind(winObj)
      };
    }
    return null;
  }

  function fireInputEvent(el) {
    try {
      el.dispatchEvent(new Event('input', { bubbles: true }));
    } catch (err) { /* very old engines: skip */ }
  }

  function attach(inputEl, opts) {
    if (!inputEl || typeof inputEl.addEventListener !== 'function' ||
        typeof inputEl.setAttribute !== 'function') {
      if (typeof console !== 'undefined' && console.warn) {
        console.warn('EVOLVPalette.attach: invalid input element');
      }
      return { destroy: function () {}, isOpen: function () { return false; } };
    }
    opts = opts || {};
    var doc = inputEl.ownerDocument ||
      (typeof document !== 'undefined' ? document : null);
    var win = (doc && doc.defaultView) ||
      (typeof window !== 'undefined' ? window : null);
    if (!doc || !doc.body) {
      return { destroy: function () {}, isOpen: function () { return false; } };
    }

    var getCommands = typeof opts.getCommands === 'function' ? opts.getCommands : null;
    var onSubmit = typeof opts.onSubmit === 'function' ? opts.onSubmit : null;
    var timers = getTimers(win);

    var styleEl = ensureStyle(doc);
    var rootId = 'evolv-palette-' + (++uidCounter);

    var rootEl = doc.createElement('div');
    rootEl.className = ROOT_CLASS;
    rootEl.id = rootId;
    rootEl.setAttribute('role', 'listbox');
    rootEl.setAttribute('aria-label', 'Slash commands');
    doc.body.appendChild(rootEl);

    inputEl.setAttribute('role', 'combobox');
    inputEl.setAttribute('aria-autocomplete', 'list');
    inputEl.setAttribute('aria-controls', rootId);
    inputEl.setAttribute('aria-expanded', 'false');

    var open = false;
    var items = [];          // current ranked command rows
    var activeIndex = 0;
    var hideTimer = null;

    function safeCommands() {
      try {
        var list = getCommands ? getCommands() : [];
        return Array.isArray(list) ? list : [];
      } catch (err) {
        return [];
      }
    }

    function updateActiveAria() {
      if (open && items.length) {
        inputEl.setAttribute('aria-activedescendant', optionId(activeIndex));
      } else {
        inputEl.removeAttribute('aria-activedescendant');
      }
    }

    function optionId(i) {
      return rootId + '-opt-' + i;
    }

    function setActive(i, scroll) {
      var rows = rootEl.childNodes;
      if (!rows.length) return;
      activeIndex = clamp(i, 0, rows.length - 1);
      for (var k = 0; k < rows.length; k++) {
        var row = rows[k];
        var on = k === activeIndex;
        if (on) row.classList.add(ACTIVE_CLASS);
        else row.classList.remove(ACTIVE_CLASS);
        row.setAttribute('aria-selected', on ? 'true' : 'false');
        if (on && scroll !== false && row.scrollIntoView) {
          row.scrollIntoView({ block: 'nearest' });
        }
      }
      updateActiveAria();
    }

    // Build the visible rows from `ranked` (already sorted, highlight ready).
    function renderRows(ranked) {
      clearChildren(rootEl);
      var shown = ranked.slice(0, MAX_ROWS);
      items = shown;
      for (var i = 0; i < shown.length; i++) {
        var cmd = shown[i];
        var row = doc.createElement('div');
        row.className = ITEM_CLASS;
        row.id = optionId(i);
        row.setAttribute('role', 'option');
        row.setAttribute('aria-selected', 'false');

        var cmdEl = doc.createElement('span');
        cmdEl.className = 'evolv-palette-cmd';
        var slashEl = doc.createElement('span');
        slashEl.className = 'evolv-palette-slash';
        slashEl.textContent = '/';
        cmdEl.appendChild(slashEl);
        // Highlight matched chars (accent + bold) using score positions.
        var marks = cmd.positions || [];
        var markSet = {};
        for (var p = 0; p < marks.length; p++) markSet[marks[p]] = true;
        var run = '';
        var flush = function () {
          if (!run) return;
          var t = doc.createTextNode(run);
          cmdEl.appendChild(t);
          run = '';
        };
        var name = String(cmd.name || '');
        for (var c = 0; c < name.length; c++) {
          if (markSet[c]) {
            flush();
            var hl = doc.createElement('span');
            hl.className = 'evolv-palette-hl';
            hl.textContent = name.charAt(c);
            cmdEl.appendChild(hl);
          } else {
            run += name.charAt(c);
          }
        }
        flush();
        row.appendChild(cmdEl);

        var desc = cmd.description ? String(cmd.description) : '';
        if (desc) {
          var descEl = doc.createElement('span');
          descEl.className = 'evolv-palette-desc';
          descEl.textContent = desc;
          row.appendChild(descEl);
        }

        var meta = [];
        if (cmd.args) meta.push(String(cmd.args));
        if (cmd.hint) meta.push(String(cmd.hint));
        if (meta.length) {
          var metaEl = doc.createElement('span');
          metaEl.className = 'evolv-palette-meta';
          metaEl.textContent = meta.join(' ');
          row.appendChild(metaEl);
        }

        rootEl.appendChild(row);
      }
      if (shown.length) setActive(0, false);
    }

    function position() {
      if (!open) return;
      var rect = inputEl.getBoundingClientRect();
      var vw = win ? win.innerWidth : rect.left + rect.width;
      var vh = win ? win.innerHeight : rect.bottom;
      var width = Math.max(260, rect.width);
      rootEl.style.width = width + 'px';
      var h = rootEl.offsetHeight;
      var below = vh - rect.bottom;
      var top;
      if (below >= h + 16) {
        top = rect.bottom + 6;
      } else {
        top = rect.top - h - 6;
        if (top < 8) top = 8;
      }
      var left = rect.left;
      if (left + width > vw - 8) left = vw - 8 - width;
      if (left < 8) left = 8;
      rootEl.style.top = top + 'px';
      rootEl.style.left = left + 'px';
    }

    function show() {
      if (hideTimer && timers) timers.clear(hideTimer);
      hideTimer = null;
      if (!open) {
        open = true;
        rootEl.classList.add(OPEN_CLASS);
      }
      // Assert state unconditionally: aria can be stale if hide() ran while
      // the palette was logically open (e.g. a second writer on the input).
      inputEl.setAttribute('aria-expanded', 'true');
      updateActiveAria();
      position();
    }

    function hide() {
      if (hideTimer && timers) timers.clear(hideTimer);
      hideTimer = null;
      if (!open) return;
      open = false;
      items = [];
      rootEl.classList.remove(OPEN_CLASS);
      inputEl.setAttribute('aria-expanded', 'false');
      updateActiveAria();
    }

    // Re-rank commands against the current input value; show/hide as needed.
    function refresh() {
      var t = tokenize(inputEl.value);
      if (!t) { hide(); return; }
      var cmds = safeCommands();
      var ranked = rank(t.name, cmds.map(function (c) {
        return c && c.name != null ? c.name : '';
      }));
      // Keep original command objects aligned with ranked results.
      if (ranked.length) {
        var byName = {};
        for (var i = 0; i < cmds.length; i++) {
          if (cmds[i] && cmds[i].name != null) byName[String(cmds[i].name)] = cmds[i];
        }
        for (var j = 0; j < ranked.length; j++) {
          var extra = byName[ranked[j].name] || {};
          ranked[j].description = extra.description;
          ranked[j].args = extra.args;
          ranked[j].hint = extra.hint;
        }
      }
      if (!ranked.length) { hide(); return; }
      renderRows(ranked);
      show();
    }

    function accept() {
      if (!open || !items.length) return false;
      var chosen = items[activeIndex];
      if (!chosen) return false;
      var t = tokenize(inputEl.value) || { name: '', rest: '', hasArgs: false };
      var rest = t.rest || '';
      if (onSubmit) {
        var consumed = false;
        try {
          consumed = onSubmit(chosen.name, rest) === true;
        } catch (err) {
          consumed = false;
        }
        if (consumed) {
          hide();
          return true;
        }
      }
      // Fallback: rewrite the first token to the full command + trailing space.
      inputEl.value = '/' + chosen.name + (rest ? ' ' + rest : ' ');
      setCaretToEnd(inputEl);
      fireInputEvent(inputEl);
      refresh();
      return true;
    }

    function move(delta) {
      if (!open || !items.length) return;
      var n = rootEl.childNodes.length;
      setActive((activeIndex + delta + n) % n);
    }

    function onKeyDown(e) {
      if (!open) return;
      var key = e.key;
      var handled = false;
      if (key === 'ArrowDown') { move(1); handled = true; }
      else if (key === 'ArrowUp') { move(-1); handled = true; }
      else if (key === 'Enter' || key === 'Tab') { handled = accept(); }
      else if (key === 'Escape') { hide(); handled = true; }
      if (handled) {
        e.preventDefault();
        e.stopPropagation();
        e.__evolvPaletteHandled = true;
      }
    }

    function onInput() { refresh(); }
    function onFocus() { refresh(); }
    function onBlur() {
      // Small delay so mousedown-based row selection lands before hiding.
      if (hideTimer && timers) timers.clear(hideTimer);
      hideTimer = null;
      if (timers) hideTimer = timers.set(hide, BLUR_DELAY_MS);
      else hide(); // no timers available (bare VM): hide immediately
    }
    function onReposition() { position(); }

    function rowFromEvent(e) {
      var node = e.target;
      while (node && node !== rootEl) {
        if (node.classList && node.classList.contains(ITEM_CLASS)) return node;
        node = node.parentNode;
      }
      return null;
    }

    function onRootMouseDown(e) {
      var row = rowFromEvent(e);
      if (!row) return;
      e.preventDefault();   // keep focus in the textarea (no blur race)
      e.stopPropagation();
      var idx = Array.prototype.indexOf.call(rootEl.childNodes, row);
      if (idx >= 0) setActive(idx, false);
      accept();
    }

    function onRootMouseOver(e) {
      var row = rowFromEvent(e);
      if (!row) return;
      var idx = Array.prototype.indexOf.call(rootEl.childNodes, row);
      if (idx >= 0 && idx !== activeIndex) setActive(idx, false);
    }

    inputEl.addEventListener('keydown', onKeyDown);
    inputEl.addEventListener('input', onInput);
    inputEl.addEventListener('focus', onFocus);
    inputEl.addEventListener('blur', onBlur);
    rootEl.addEventListener('mousedown', onRootMouseDown);
    rootEl.addEventListener('mouseover', onRootMouseOver);
    if (win) {
      win.addEventListener('scroll', onReposition, true);
      win.addEventListener('resize', onReposition);
    }

    function destroy() {
      inputEl.removeEventListener('keydown', onKeyDown);
      inputEl.removeEventListener('input', onInput);
      inputEl.removeEventListener('focus', onFocus);
      inputEl.removeEventListener('blur', onBlur);
      rootEl.removeEventListener('mousedown', onRootMouseDown);
      rootEl.removeEventListener('mouseover', onRootMouseOver);
      if (win) {
        win.removeEventListener('scroll', onReposition, true);
        win.removeEventListener('resize', onReposition);
      }
      hide();
      if (rootEl.parentNode) rootEl.parentNode.removeChild(rootEl);
      inputEl.removeAttribute('aria-activedescendant');
      inputEl.removeAttribute('aria-controls');
      inputEl.removeAttribute('aria-expanded');
      inputEl.removeAttribute('aria-autocomplete');
      inputEl.removeAttribute('role');
      releaseStyle(doc, styleEl);
    }

    return {
      destroy: destroy,
      isOpen: function () { return open; }
    };
  }

  var GLOBAL = typeof window !== 'undefined' ? window :
    (typeof globalThis !== 'undefined' ? globalThis : {});
  GLOBAL.EVOLVPalette = {
    attach: attach,
    fuzz: fuzz,
    tokenize: tokenize
  };
})();
