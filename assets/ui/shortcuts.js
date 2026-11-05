/* EVOLV keyboard shortcuts — a tiny, zero-dependency key layer.
   window.EVOLVKeys.register('mod+k', fn, { label, allowInInput, section })
   Combos: 'mod+k', 'mod+shift+p', '?', sequences like 'g r' (two keys,
   1200 ms apart). mod = Cmd on macOS, Ctrl elsewhere (navigator.platform,
   override with EVOLVKeys.setPlatform()). Self-activating on include;
   evaluates safely even where document is undefined. */
(() => {
  'use strict';

  // ---- environment guards (must never throw in a bare VM) ------------------
  const win = typeof window !== 'undefined' ? window : undefined;
  const doc = (win && typeof win.document !== 'undefined' && win.document)
    ? win.document
    : (typeof document !== 'undefined' ? document : undefined);
  const nav = win && typeof win.navigator !== 'undefined' && win.navigator
    ? win.navigator
    : (typeof navigator !== 'undefined' ? navigator : undefined);
  const hasDOM = !!(doc && typeof doc.addEventListener === 'function');

  // ---- platform detection ---------------------------------------------------
  let platformOverride = null;
  const looksMac = (s) => /mac|iphone|ipad|ipod/i.test(String(s || ''));
  const isMac = () => {
    if (platformOverride !== null) return looksMac(platformOverride);
    return !!(nav && nav.platform && looksMac(nav.platform));
  };

  const STATE = {
    regs: [],            // registrations in order
    seqBuf: null,        // { reg, index, timer } while inside a sequence
    seqTimer: 0,
    helpOpen: false,
    overlay: null,
    styleInjected: false,
  };
  const SEQ_GAP_MS = 1200;
  const STYLE_ID = 'evolv-keys-style';
  const CLASS = 'evolv-keys'; // every overlay class is prefixed `evolv-keys-`

  // ---- combo parsing --------------------------------------------------------
  const KEY_ALIASES = {
    esc: 'escape', return: 'enter', del: 'delete', ins: 'insert',
    space: 'space', spacebar: 'space',
    up: 'arrowup', down: 'arrowdown', left: 'arrowleft', right: 'arrowright',
    plus: '+', minus: '-', comma: ',', period: '.', slash: '/', quote: "'",
  };
  const MOD_TOKENS = {
    mod: 'mod', ctrl: 'ctrl', control: 'ctrl', meta: 'meta', cmd: 'meta',
    command: 'meta', alt: 'alt', option: 'alt', shift: 'shift',
  };

  function normalizeKeyName(raw) {
    const k = String(raw).toLowerCase();
    if (k in KEY_ALIASES) return KEY_ALIASES[k];
    return k;
  }

  // Parse one step like 'mod+shift+p' → { mod, ctrl, meta, alt, shift, key }
  // Modifier tokens are kept RAW here; `mod` is resolved to ctrl/meta per the
  // current platform at match time, so setPlatform() affects live behavior.
  function parseStep(step) {
    const parts = String(step).split('+');
    const out = { mod: false, ctrl: false, meta: false, alt: false, shift: false, key: null };
    for (let i = 0; i < parts.length; i++) {
      const p = parts[i].trim();
      if (!p) return null; // e.g. 'ctrl++' — use 'ctrl+plus' instead
      const mod = MOD_TOKENS[p];
      if (mod) {
        out[mod] = true;
        continue;
      }
      if (out.key !== null) return null; // two non-modifier tokens in one step
      out.key = normalizeKeyName(p);
    }
    if (out.key === null) return null;
    return out;
  }

  // Parse a full combo: 'mod+k' → [step]; 'g r' → [step, step]
  function parseCombo(combo) {
    if (typeof combo !== 'string') return null;
    const names = combo.trim().toLowerCase().split(/\s+/);
    if (!names[0]) return null;
    const steps = [];
    for (let i = 0; i < names.length; i++) {
      const s = parseStep(names[i]);
      if (!s) return null;
      steps.push(s);
    }
    return steps;
  }

  const comboKey = (combo) => combo.trim().toLowerCase().replace(/\s+/g, ' ');
  const isSequence = (steps) => steps.length > 1;

  // ---- event ↔ step matching ------------------------------------------------
  function eventKey(e) {
    let k = e && typeof e.key === 'string' ? e.key : '';
    if (k === ' ') k = 'space';
    return k.toLowerCase();
  }

  // '?' is Shift+'/' — for single non-alphanumeric chars, Shift is implied.
  const shiftIsImplied = (key) => key.length === 1 && !/[a-z0-9]/.test(key);

  // Resolve raw tokens to concrete modifiers for the CURRENT platform.
  function resolvedMods(step) {
    const mac = isMac();
    return {
      ctrl: step.ctrl || (!mac && step.mod),
      meta: step.meta || (mac && step.mod),
      alt: step.alt,
      shift: step.shift,
    };
  }

  function stepMatches(step, e) {
    if (eventKey(e) !== step.key) return false;
    const m = resolvedMods(step);
    if (e.ctrlKey !== m.ctrl || e.metaKey !== m.meta || e.altKey !== m.alt) return false;
    if (shiftIsImplied(step.key)) return true;
    return e.shiftKey === m.shift;
  }

  // In input/textarea/select/contenteditable: ignore unless allowInInput,
  // unless the combo itself carries mod/ctrl/meta (always deliberate).
  function stepWantsInInput(step) {
    return step.mod || step.ctrl || step.meta;
  }

  function isEditableTarget(t) {
    if (!t || typeof t !== 'object') return false;
    const tag = (t.tagName || '').toLowerCase();
    if (tag === 'input' || tag === 'textarea' || tag === 'select') return true;
    return t.isContentEditable === true;
  }

  // ---- registration ----------------------------------------------------------
  function register(combo, handler, opts) {
    const steps = parseCombo(combo);
    if (!steps || typeof handler !== 'function') {
      return { unregister() {} };
    }
    const options = opts || {};
    const reg = {
      combo: comboKey(combo),
      steps,
      seq: isSequence(steps),
      label: typeof options.label === 'string' && options.label ? options.label : comboKey(combo),
      allowInInput: options.allowInInput === true,
      section: typeof options.section === 'string' && options.section ? options.section : 'General',
      builtin: options._builtin === true,
      handler,
      remove() {
        const idx = STATE.regs.indexOf(reg);
        if (idx !== -1) {
          STATE.regs.splice(idx, 1);
          if (STATE.seqBuf && STATE.seqBuf.reg === reg) clearSeq();
        }
      },
    };

    // Never register the exact same combo twice (latest wins). A user
    // registration therefore replaces the built-in on the same combo, so the
    // '?' default never double-fires with a custom help binding.
    for (let i = STATE.regs.length - 1; i >= 0; i--) {
      if (STATE.regs[i].combo === reg.combo) STATE.regs[i].remove();
    }

    STATE.regs.push(reg);
    return {
      unregister() { reg.remove(); },
    };
  }

  function list() {
    return STATE.regs.map((r) => ({
      combo: r.combo,
      label: r.label,
      section: r.section,
      allowInInput: r.allowInInput,
    }));
  }

  // ---- sequence tracking -----------------------------------------------------
  function clearSeq() {
    if (STATE.seqTimer) {
      clearTimeout(STATE.seqTimer);
      STATE.seqTimer = 0;
    }
    STATE.seqBuf = null;
  }

  // ---- help overlay ----------------------------------------------------------
  const CSS = `
.evolv-keys-overlay{position:fixed;inset:0;z-index:9999;display:flex;align-items:center;
  justify-content:center;background:rgba(0,0,0,.45);padding:24px;}
.evolv-keys-panel{background:var(--bg3,#1c1c20);border:1px solid var(--line,#333);
  border-radius:var(--r-md,10px);color:var(--text,#e8e8ea);max-width:560px;width:100%;
  max-height:min(70vh,560px);overflow-y:auto;box-shadow:0 18px 48px rgba(0,0,0,.5);
  font-size:14px;line-height:1.45;transition:opacity .12s ease,transform .12s ease;}
.evolv-keys-head{display:flex;align-items:center;justify-content:space-between;
  padding:14px 18px;border-bottom:1px solid var(--line,#333);
  position:sticky;top:0;background:var(--bg3,#1c1c20);}
.evolv-keys-title{font-weight:600;font-size:15px;}
.evolv-keys-close{background:none;border:1px solid var(--line,#333);color:var(--dim,#999);
  border-radius:6px;padding:3px 9px;cursor:pointer;font-size:12px;}
.evolv-keys-close:hover{color:var(--text,#e8e8ea);border-color:var(--dim,#999);}
.evolv-keys-body{padding:10px 18px 18px;}
.evolv-keys-section{color:var(--dim,#999);font-size:11px;text-transform:uppercase;
  letter-spacing:.08em;margin:14px 0 6px;}
.evolv-keys-section:first-child{margin-top:6px;}
.evolv-keys-row{display:flex;align-items:center;justify-content:space-between;gap:16px;
  padding:5px 0;}
.evolv-keys-label{flex:1;}
.evolv-keys-keys{display:inline-flex;gap:4px;align-items:center;white-space:nowrap;}
.evolv-keys-kbd{display:inline-block;min-width:20px;text-align:center;padding:2px 6px;
  font:600 11px/1.3 ui-monospace,SFMono-Regular,Menlo,Consolas,monospace;color:var(--text,#e8e8ea);
  background:var(--bg4,#26262b);border:1px solid var(--line,#333);
  border-bottom-width:2px;border-radius:5px;box-shadow:0 1px 0 rgba(0,0,0,.25);}
.evolv-keys-dash{color:var(--dim,#999);font-size:11px;}
.evolv-keys-empty{color:var(--dim,#999);padding:8px 0;}
.evolv-keys-foot{padding:10px 18px;border-top:1px solid var(--line,#333);
  color:var(--dim,#999);font-size:11px;position:sticky;bottom:0;background:var(--bg3,#1c1c20);}
@media (prefers-reduced-motion: reduce){
  .evolv-keys-panel{transition:none !important;}
}`;
  // 'mod' renders per platform: ⌘ on macOS, Ctrl elsewhere.
  function tokenLabel(token) {
    const t = String(token).toLowerCase();
    if (t === 'mod') return isMac() ? '⌘' : 'Ctrl';
    if (t === 'meta' || t === 'cmd' || t === 'command') return '⌘';
    if (t === 'ctrl' || t === 'control') return 'Ctrl';
    if (t === 'alt' || t === 'option') return isMac() ? '⌥' : 'Alt';
    if (t === 'shift') return 'Shift';
    return token;
  }

  function buildKeyCaps(combo) {
    const wrap = doc.createElement('span');
    wrap.className = CLASS + '-keys';
    const steps = combo.split(' ');
    for (let i = 0; i < steps.length; i++) {
      if (i > 0) {
        const dash = doc.createElement('span');
        dash.className = CLASS + '-dash';
        dash.textContent = 'then';
        wrap.appendChild(dash);
      }
      const tokens = steps[i].split('+');
      for (let j = 0; j < tokens.length; j++) {
        const kbd = doc.createElement('kbd');
        kbd.className = CLASS + '-kbd';
        kbd.textContent = tokenLabel(tokens[j]);
        wrap.appendChild(kbd);
      }
    }
    return wrap;
  }

  function injectStyle() {
    if (STATE.styleInjected || !doc || typeof doc.getElementById !== 'function') return;
    if (doc.getElementById(STYLE_ID)) { STATE.styleInjected = true; return; }
    const head = doc.head || doc.getElementsByTagName('head')[0];
    if (!head || typeof head.appendChild !== 'function') return;
    const tag = doc.createElement('style');
    tag.id = STYLE_ID;
    tag.textContent = CSS;
    head.appendChild(tag);
    STATE.styleInjected = true;
  }

  function buildOverlay() {
    if (!doc || typeof doc.createElement !== 'function') return null;
    injectStyle();
    const overlay = doc.createElement('div');
    overlay.className = CLASS + '-overlay';
    overlay.setAttribute('role', 'dialog');
    overlay.setAttribute('aria-modal', 'true');
    overlay.setAttribute('aria-label', 'Keyboard shortcuts');

    const panel = doc.createElement('div');
    panel.className = CLASS + '-panel';

    const head = doc.createElement('div');
    head.className = CLASS + '-head';
    const title = doc.createElement('span');
    title.className = CLASS + '-title';
    title.textContent = 'Keyboard Shortcuts';
    const close = doc.createElement('button');
    close.type = 'button';
    close.className = CLASS + '-close';
    close.textContent = 'Esc ✕';
    close.setAttribute('aria-label', 'Close shortcuts help');
    head.appendChild(title);
    head.appendChild(close);

    const body = doc.createElement('div');
    body.className = CLASS + '-body';

    const foot = doc.createElement('div');
    foot.className = CLASS + '-foot';
    foot.textContent = 'Press Esc or click outside to close';

    panel.appendChild(head);
    panel.appendChild(body);
    panel.appendChild(foot);
    overlay.appendChild(panel);

    // click anywhere on the backdrop closes; clicks inside the panel don't
    overlay.addEventListener('click', () => showHelp(false));
    panel.addEventListener('click', (e) => {
      if (e && typeof e.stopPropagation === 'function') e.stopPropagation();
      if (e && e.target === close) showHelp(false);
    });

    overlay._body = body;
    return overlay;
  }

  function renderHelp(body) {
    if (!body) return;
    while (body.firstChild && typeof body.removeChild === 'function') body.removeChild(body.firstChild);
    const rows = list();
    if (!rows.length) {
      const empty = doc.createElement('div');
      empty.className = CLASS + '-empty';
      empty.textContent = 'No shortcuts registered.';
      body.appendChild(empty);
      return;
    }
    const bySection = new Map();
    for (const r of rows) {
      if (!bySection.has(r.section)) bySection.set(r.section, []);
      bySection.get(r.section).push(r);
    }
    for (const [section, items] of bySection) {
      const h = doc.createElement('div');
      h.className = CLASS + '-section';
      h.textContent = section;
      body.appendChild(h);
      for (const item of items) {
        const row = doc.createElement('div');
        row.className = CLASS + '-row';
        const label = doc.createElement('span');
        label.className = CLASS + '-label';
        label.textContent = item.label;
        row.appendChild(label);
        row.appendChild(buildKeyCaps(item.combo));
        body.appendChild(row);
      }
    }
  }

  function showHelp(open) {
    if (!hasDOM) return false;
    if (open && !STATE.helpOpen) {
      if (!STATE.overlay) STATE.overlay = buildOverlay();
      if (!STATE.overlay) return false;
      renderHelp(STATE.overlay._body);
      if (typeof doc.body.appendChild === 'function') doc.body.appendChild(STATE.overlay);
      STATE.helpOpen = true;
      clearSeq();
      return true;
    }
    if (!open && STATE.helpOpen) {
      if (STATE.overlay && STATE.overlay.parentNode && typeof STATE.overlay.parentNode.removeChild === 'function') {
        STATE.overlay.parentNode.removeChild(STATE.overlay);
      }
      STATE.helpOpen = false;
      return false;
    }
    return STATE.helpOpen;
  }

  function help() {
    return showHelp(!STATE.helpOpen);
  }

  // ---- the single document-level keydown listener ----------------------------
  function fire(reg, e) {
    try {
      reg.handler(e);
    } catch (err) {
      if (typeof console !== 'undefined' && console.warn) console.warn('[EVOLVKeys] handler failed for', reg.combo, err);
    }
  }

  function findDirectMatch(e, inEditable) {
    for (let i = 0; i < STATE.regs.length; i++) {
      const r = STATE.regs[i];
      if (r.seq) continue;
      if (inEditable && !r.allowInInput && !r.steps.some(stepWantsInInput)) continue;
      if (stepMatches(r.steps[0], e)) return r;
    }
    return null;
  }

  function trySequence(e, inEditable) {
    // Plain-key sequences: no ctrl/meta/alt held, help not open.
    if (e.ctrlKey || e.metaKey || e.altKey || STATE.helpOpen) { clearSeq(); return false; }
    const allowed = (r) => inEditable ? r.allowInInput : true;
    const matchesStep = (r, idx) => !r.steps[idx].ctrl && !r.steps[idx].meta
      && !r.steps[idx].alt && stepMatches(r.steps[idx], e);

    // Continue an open sequence.
    if (STATE.seqBuf) {
      if (matchesStep(STATE.seqBuf.reg, STATE.seqBuf.index)) {
        STATE.seqBuf.index += 1;
        armSeqTimer();
        if (STATE.seqBuf.index >= STATE.seqBuf.reg.steps.length) {
          const done = STATE.seqBuf.reg;
          clearSeq();
          fire(done, e);
          return true;
        }
        return true; // mid-sequence: swallow the key
      }
      clearSeq();
    }

    // Start a fresh sequence.
    if (e.shiftKey && !shiftIsImplied(eventKey(e))) return false;
    for (let i = 0; i < STATE.regs.length; i++) {
      const r = STATE.regs[i];
      if (!r.seq || !allowed(r)) continue;
      if (matchesStep(r, 0)) {
        STATE.seqBuf = { reg: r, index: 1 };
        armSeqTimer();
        if (STATE.seqBuf.index >= r.steps.length) { // defensive: 1-step seqs aren't sequences
          clearSeq();
          fire(r, e);
        }
        return true;
      }
    }
    return false;
  }

  function armSeqTimer() {
    if (STATE.seqTimer) clearTimeout(STATE.seqTimer);
    STATE.seqTimer = setTimeout(clearSeq, SEQ_GAP_MS);
  }

  function onKeyDown(e) {
    if (!e || typeof e !== 'object') return;
    const key = eventKey(e);
    const inEditable = isEditableTarget(e.target);

    // Escape: always resets sequences; closes help and swallows the event.
    if (key === 'escape') {
      const hadSeq = !!STATE.seqBuf;
      clearSeq();
      if (STATE.helpOpen) { showHelp(false); prevent(e); return; }
      if (hadSeq) return; // seq abort needs no preventDefault
      // fall through: Escape may itself be a registered combo
    } else {
      clearOnNonSequence(key, inEditable, e);
    }

    const direct = findDirectMatch(e, inEditable);
    if (direct) {
      clearSeq(); // a completed combo invalidates any pending sequence
      prevent(e);
      fire(direct, e);
      return;
    }
    if (trySequence(e, inEditable)) {
      prevent(e);
      return;
    }
    // No registration matched: never swallow — let the app see the event.
  }

  function clearOnNonSequence(key, inEditable, e) {
    // A pending sequence dies on any key that isn't its next plain step;
    // trySequence below handles the rest. Also stop tracking if a modifier-
    // carrying keydown happens (sequence steps are plain keys).
    if (STATE.seqBuf && (e.ctrlKey || e.metaKey || e.altKey)) clearSeq();
    if (STATE.seqBuf && key === 'tab') clearSeq(); // don't trap Tab mid-sequence
  }

  function prevent(e) {
    if (e && typeof e.preventDefault === 'function') e.preventDefault();
    if (e && typeof e.stopPropagation === 'function') e.stopPropagation();
  }

  // ---- built-in registrations (each guarded on the real DOM target) ----------
  function registerDefaults() {
    if (!hasDOM) return;
    const id = (x) => doc.getElementById(x);

    if (id('input') && !STATE.regs.some((r) => r.combo === 'mod+k')) {
      register('mod+k', () => {
        const input = id('input');
        if (input && typeof input.focus === 'function') input.focus();
      }, { label: 'Focus chat input', section: 'General', _builtin: true });
    }
    if (!STATE.regs.some((r) => r.combo === '?')) {
      register('?', () => { help(); }, { label: 'Toggle shortcut help', section: 'General', _builtin: true });
    }
    if (id('btn-sidebar') && !STATE.regs.some((r) => r.combo === 'mod+shift+l')) {
      register('mod+shift+l', () => {
        const btn = id('btn-sidebar');
        if (btn && typeof btn.click === 'function') btn.click();
      }, { label: 'Toggle sidebar', section: 'General', _builtin: true });
    }
  }

  // ---- public API ------------------------------------------------------------
  const api = {
    register,
    list,
    help,
    isMac,
    setPlatform(name) { platformOverride = name == null ? null : String(name); },
    defaults: registerDefaults,
  };

  // self-activation
  if (hasDOM) {
    doc.addEventListener('keydown', onKeyDown, true); // capture: see events first
    registerDefaults();
  }

  if (win) win.EVOLVKeys = api; // guarded: bare VM may lack window entirely
})();
