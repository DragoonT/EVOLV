// EVOLV's editable appearance panel. All choices are local to this browser.
(() => {
  const key = 'evolv.appearance.v1';
  const palettes = {
    obsidian: { label: 'Obsidian', bg: '#14161d', bg2: '#101116', bg3: '#1d202a', bg4: '#292d3a', line: '#353947', text: '#f0f0f6', dim: '#b0b4c3', faint: '#858a9b', accent: '#a995ff' },
    arctic: { label: 'Arctic', bg: '#f5f8fc', bg2: '#e9eff8', bg3: '#ffffff', bg4: '#dfe8f4', line: '#cbd6e5', text: '#172235', dim: '#52637b', faint: '#71829a', accent: '#4f80f7' },
    graphite: { label: 'Graphite', bg: '#181c1b', bg2: '#121715', bg3: '#222825', bg4: '#303a33', line: '#39453b', text: '#ecede4', dim: '#b1bdb0', faint: '#8d9a8c', accent: '#c9ef72' },
    nord: { label: 'Nord', bg: '#242c3b', bg2: '#1c2431', bg3: '#303b4d', bg4: '#3c495d', line: '#4c5b70', text: '#edf3f5', dim: '#b9cbd4', faint: '#90a7b6', accent: '#8ccddc' },
    forest: { label: 'Forest', bg: '#13231c', bg2: '#0d1b15', bg3: '#1d3025', bg4: '#294331', line: '#36543f', text: '#e5f3e8', dim: '#a9cbb2', faint: '#83a88d', accent: '#8cd7a6' },
    rose: { label: 'Rose', bg: '#261d29', bg2: '#1d1720', bg3: '#332736', bg4: '#453248', line: '#59445a', text: '#f8ebf4', dim: '#d4b4cc', faint: '#ac8da7', accent: '#e28ac1' }
  };
  const accents = ['#a995ff', '#5b9bff', '#2dbba8', '#e9a95b', '#de7094', '#e5d65d', '#f3f3f3'];
  const defaults = { preset: 'graphite', accent: '', density: 'comfortable', radius: 8 };
  let saved = {};
  try { saved = JSON.parse(localStorage.getItem(key) || '{}') || {}; } catch (_) {}
  const state = { ...defaults, ...saved };
  if (!palettes[state.preset]) state.preset = defaults.preset;
  if (!['compact', 'comfortable', 'spacious'].includes(state.density)) state.density = defaults.density;
  if (!/^#[0-9a-fA-F]{6}$/.test(state.accent)) state.accent = '';
  state.radius = Math.max(0, Math.min(24, Number(state.radius) || 0));
  const panel = document.createElement('section');
  panel.className = 'appearance-playground';
  panel.setAttribute('aria-label', 'Appearance playground');
  panel.innerHTML = `<div class="ap-head"><div><h3>Appearance playground</h3><p>Shape the workspace. Changes appear instantly.</p></div><span>LIVE</span></div>
    <div class="ap-grid" role="group" aria-label="Theme preset"></div>
    <div class="ap-subhead">Accent color</div><div class="ap-accents" role="group" aria-label="Accent color"></div>
    <label class="ap-custom-label">Custom color <input class="ap-custom" type="color" aria-label="Custom accent color"></label>
    <div class="ap-subhead">Interface density</div><div class="ap-density" role="group" aria-label="Interface density"></div>
    <label class="ap-range-label" for="ap-radius">Corner radius <output id="ap-radius-output"></output></label>
    <input id="ap-radius" class="ap-radius" type="range" min="0" max="24" step="1">
    <div class="ap-preview"><div class="ap-preview-head"><span class="ap-preview-mark">✦</span><div><strong>EVOLV workspace</strong><small>Live appearance preview</small></div><i></i></div><div class="ap-preview-card"><span>System overview</span><div><b>Run evolution</b><em>Inspect genome</em></div></div></div>
    <button class="ap-reset" type="button">Restore default appearance</button>`;
  const db = document.querySelector('#drawer .db');
  if (!db) return;
  const section = (label, detail) => {
    const heading = document.createElement('div');
    heading.className = 'settings-section-heading';
    heading.innerHTML = `<span>${label}</span><small>${detail}</small>`;
    return heading;
  };
  db.insertBefore(panel, db.firstChild);
  panel.insertAdjacentElement('beforebegin', section('Appearance', 'visual system'));
  const providerField = db.querySelector('#set-provider')?.closest('.field');
  if (providerField) providerField.insertAdjacentElement('beforebegin', section('Connection', 'models and providers'));
  const genomeField = db.querySelector('#genome-path')?.closest('.field');
  if (genomeField) genomeField.insertAdjacentElement('beforebegin', section('Genome', 'local organism files'));
  const emblem = document.querySelector('.logo-mark')?.innerHTML;
  function evolveWelcomeMark() {
    if (!emblem) return;
    document.querySelectorAll('.w-mark:not(.evolv-mark)').forEach(mark => {
      mark.classList.add('evolv-mark');
      mark.setAttribute('role', 'img');
      mark.setAttribute('aria-label', 'EVOLV evolving emblem');
      mark.innerHTML = emblem;
    });
  }
  evolveWelcomeMark();
  const chat = document.querySelector('#chat-scroll');
  if (chat) new MutationObserver(evolveWelcomeMark).observe(chat, { childList: true, subtree: true });
  const grid = panel.querySelector('.ap-grid');
  Object.entries(palettes).forEach(([id, p]) => {
    const button = document.createElement('button');
    button.type = 'button'; button.className = 'ap-preset'; button.dataset.preset = id;
    button.setAttribute('aria-label', `${p.label} theme`);
    button.innerHTML = `<span class="ap-swatch" style="--ap-bg:${p.bg};--ap-card:${p.bg3};--ap-line:${p.line};--ap-accent:${p.accent}"><i></i><b></b><em></em></span><span>${p.label}</span><span class="ap-check" aria-hidden="true">✓</span>`;
    button.onclick = () => { state.preset = id; state.accent = ''; apply(); };
    grid.append(button);
  });
  const accentGroup = panel.querySelector('.ap-accents');
  accents.forEach(color => {
    const button = document.createElement('button');
    button.type = 'button'; button.className = 'ap-accent'; button.style.backgroundColor = color;
    button.dataset.color = color; button.setAttribute('aria-label', `Accent ${color}`);
    button.onclick = () => { state.accent = color; apply(); };
    accentGroup.append(button);
  });
  const custom = panel.querySelector('.ap-custom');
  custom.oninput = () => { state.accent = custom.value; apply(); };
  const density = panel.querySelector('.ap-density');
  ['compact', 'comfortable', 'spacious'].forEach(value => {
    const button = document.createElement('button'); button.type = 'button'; button.textContent = value;
    button.dataset.density = value;
    button.onclick = () => { state.density = value; apply(); };
    density.append(button);
  });
  const slider = panel.querySelector('.ap-radius');
  slider.oninput = () => { state.radius = Number(slider.value); apply(); };
  panel.querySelector('.ap-reset').onclick = () => { Object.assign(state, defaults); apply(); };
  let lastPaper = document.body.classList.contains('paper');
  function apply() {
    const p = palettes[state.preset];
    const accent = state.accent || p.accent;
    if (typeof applyTheme === 'function') applyTheme(state.preset === 'arctic' ? 'paper' : 'dark');
    lastPaper = document.body.classList.contains('paper');
    for (const name of ['bg', 'bg2', 'bg3', 'bg4', 'line', 'text', 'dim', 'faint']) document.body.style.setProperty(`--${name}`, p[name]);
    document.body.style.setProperty('--line-soft', p.line);
    document.body.style.setProperty('--accent', accent);
    const rgb = [1, 3, 5].map(i => parseInt(accent.slice(i, i + 2), 16));
    document.body.style.setProperty('--accent-soft', `rgba(${rgb.join(',')},.16)`);
    document.body.style.setProperty('--radius', `${state.radius}px`);
    document.body.dataset.density = state.density;
    document.body.dataset.appearance = state.preset;
    grid.querySelectorAll('button').forEach(b => { const active = b.dataset.preset === state.preset; b.classList.toggle('selected', active); b.setAttribute('aria-pressed', String(active)); });
    accentGroup.querySelectorAll('button').forEach(b => { const active = b.dataset.color === accent; b.classList.toggle('selected', active); b.setAttribute('aria-pressed', String(active)); });
    density.querySelectorAll('button').forEach(b => { const active = b.dataset.density === state.density; b.classList.toggle('selected', active); b.setAttribute('aria-pressed', String(active)); });
    custom.value = accent;
    slider.value = String(state.radius);
    panel.querySelector('#ap-radius-output').textContent = `${state.radius}px`;
    localStorage.setItem(key, JSON.stringify(state));
  }
  const appearanceRoot = document.body;
  if (!appearanceRoot) return;
  new MutationObserver(() => {
    const paper = document.body.classList.contains('paper');
    if (paper === lastPaper) return;
    lastPaper = paper;
    const next = paper ? 'arctic' : 'graphite';
    if (state.preset !== next) { state.preset = next; state.accent = ''; apply(); }
  }).observe(appearanceRoot, { attributes: true, attributeFilter: ['class'] });
  apply();
})();
