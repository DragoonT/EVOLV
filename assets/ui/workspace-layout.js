/* Editable workspace layout. Existing panels and their event handlers stay mounted. */
(() => {
  const sidebar = document.getElementById('sidebar');
  const app = document.getElementById('app');
  if (!sidebar || !app) return;
  const right = document.createElement('aside');
  right.id = 'workspace-inspector';
  right.setAttribute('aria-label', 'Workspace inspector');
  right.innerHTML = '<div class="inspector-title"><span>WORKSPACE</span><small>02 / INSPECT</small></div><div id="inspector-panels"></div>';
  app.appendChild(right);
  const target = right.querySelector('#inspector-panels');
  const navigation = sidebar.querySelector('.sb-bottom');
  navigation.id = 'navigation-sec';
  navigation.classList.add('sb-sec');
  const navHead = document.createElement('div'); navHead.className = 'sb-head'; navHead.textContent = 'Navigation'; navigation.prepend(navHead);
  const model = document.createElement('section'); model.id = 'model-sec'; model.className = 'sb-sec';
  model.innerHTML = '<div class="sb-head">Model status</div>';
  model.appendChild(document.getElementById('provider-pill')); target.appendChild(model);
  const ids = ['mind-sec', 'plan-sec', 'organs-sec', 'evo-sec', 'navigation-sec', 'model-sec'];
  let saved = {};
  try { saved = JSON.parse(localStorage.getItem('evolv.panel-layout.v1') || '{}') || {}; } catch (_) {}
  const persist = () => {
    const data = {};
    ids.forEach(id => { const el = document.getElementById(id); if (el) data[id] = { side: el.parentElement === target ? 'right' : 'left', order: [...el.parentElement.children].indexOf(el), collapsed: el.classList.contains('panel-collapsed'), locked: el.dataset.locked === 'true', size: el.dataset.panelSize || '' }; });
    try { localStorage.setItem('evolv.panel-layout.v1', JSON.stringify(data)); } catch (_) {}
  };
  ids.forEach(id => {
    const panel = document.getElementById(id), head = panel && panel.querySelector('.sb-head');
    if (!head) return;
    panel.dataset.panelId = id;
    const controls = document.createElement('span'); controls.className = 'panel-controls';
    const toggle = document.createElement('button'); toggle.type = 'button'; toggle.className = 'panel-control'; toggle.title = 'Collapse or expand panel'; toggle.textContent = '−';
    const move = document.createElement('button'); move.type = 'button'; move.className = 'panel-control'; move.title = 'Move panel to other sidebar'; move.textContent = '⇄'; move.setAttribute('aria-label', 'Move ' + id.replace('-sec', '') + ' to other sidebar');
    const update = () => { const collapsed = panel.classList.contains('panel-collapsed'); toggle.textContent = collapsed ? '+' : '−'; toggle.setAttribute('aria-expanded', String(!collapsed)); toggle.setAttribute('aria-label', (collapsed ? 'Expand ' : 'Collapse ') + id.replace('-sec', '')); };
    toggle.addEventListener('click', () => { panel.classList.toggle('panel-collapsed'); update(); persist(); });
    move.addEventListener('click', () => { if (panel.parentElement === target) sidebar.appendChild(panel); else target.appendChild(panel); persist(); });
    controls.append(toggle, move); head.appendChild(controls);
    const grip = document.createElement('button'); grip.type = 'button'; grip.className = 'panel-control panel-grip'; grip.textContent = '⠿'; grip.draggable = true;
    grip.setAttribute('aria-label', 'Reorder ' + id.replace('-sec', '') + ': Alt plus up or down');
    grip.title = 'Drag to reorder; Alt + Up/Down to move with keyboard';
    head.prepend(grip);
    const lock = document.createElement('button'); lock.type = 'button'; lock.className = 'panel-control'; lock.textContent = '○'; lock.title = 'Lock panel position'; lock.setAttribute('aria-label', 'Lock ' + id.replace('-sec', '') + ' position');
    controls.append(lock);
    const setLocked = value => { panel.dataset.locked = String(value); grip.draggable = !value; move.disabled = value; lock.textContent = value ? '●' : '○'; lock.setAttribute('aria-pressed', String(value)); };
    lock.addEventListener('click', () => { setLocked(panel.dataset.locked !== 'true'); persist(); });
    const resizer = document.createElement('div'); resizer.className = 'panel-resizer'; resizer.setAttribute('role', 'separator'); resizer.setAttribute('aria-label', 'Resize ' + id.replace('-sec', '') + ' panel');
    panel.appendChild(resizer);
    const savedSize = Number(saved[id]?.size);
    if (savedSize >= 96 && savedSize <= 720) { panel.style.setProperty('--panel-size', savedSize + 'px'); panel.dataset.panelSize = String(savedSize); }
    resizer.addEventListener('pointerdown', e => {
      if (panel.dataset.locked === 'true') return;
      e.preventDefault(); resizer.setPointerCapture(e.pointerId);
      const startY = e.clientY, start = panel.getBoundingClientRect().height;
      const move = ev => { const size = Math.max(96, Math.min(720, Math.round(start + ev.clientY - startY))); panel.style.setProperty('--panel-size', size + 'px'); panel.dataset.panelSize = String(size); };
      const end = () => { resizer.removeEventListener('pointermove', move); resizer.removeEventListener('pointerup', end); persist(); };
      resizer.addEventListener('pointermove', move); resizer.addEventListener('pointerup', end);
    });
    setLocked(saved[id]?.locked === true);
    grip.addEventListener('keydown', e => {
      if (panel.dataset.locked === 'true' || !e.altKey || !['ArrowUp', 'ArrowDown'].includes(e.key)) return;
      e.preventDefault();
      const siblings = [...panel.parentElement.children].filter(el => el.dataset.panelId);
      const index = siblings.indexOf(panel), other = siblings[index + (e.key === 'ArrowUp' ? -1 : 1)];
      if (!other || other.dataset.locked === 'true') return;
      panel.parentElement.insertBefore(panel, e.key === 'ArrowUp' ? other : other.nextSibling);
      persist(); grip.focus();
    });
    grip.addEventListener('dragstart', e => { e.dataTransfer.setData('text/x-evolv-panel', id); e.dataTransfer.effectAllowed = 'move'; });
    panel.addEventListener('dragover', e => { if ([...e.dataTransfer.types].includes('text/x-evolv-panel')) { e.preventDefault(); panel.classList.add('panel-drop'); } });
    panel.addEventListener('dragleave', () => panel.classList.remove('panel-drop'));
    panel.addEventListener('drop', e => {
      e.preventDefault(); panel.classList.remove('panel-drop');
      const sourceId = e.dataTransfer.getData('text/x-evolv-panel');
      if (!ids.includes(sourceId) || sourceId === id || panel.dataset.locked === 'true' || document.getElementById(sourceId).dataset.locked === 'true') return;
      panel.parentElement.insertBefore(document.getElementById(sourceId), panel); persist();
    });
    grip.addEventListener('dragend', () => document.querySelectorAll('.panel-drop').forEach(el => el.classList.remove('panel-drop')));
    if (saved[id] && saved[id].collapsed === true) panel.classList.add('panel-collapsed');
    if (saved[id] && saved[id].side === 'right') target.appendChild(panel);
    else if (saved[id] && saved[id].side === 'left') sidebar.appendChild(panel);
    update();
  });
  if (!Object.keys(saved).length) ['plan-sec', 'evo-sec'].forEach(id => { const el = document.getElementById(id); if (el) target.appendChild(el); });
  for (const dock of [sidebar, target]) {
    [...dock.children].filter(el => el.dataset.panelId).sort((a,b) => (Number(saved[a.id]?.order) || 0) - (Number(saved[b.id]?.order) || 0)).forEach(el => dock.appendChild(el));
  }
  // Narrow screens keep every compartment reachable in the existing mobile drawer.
  const narrow = matchMedia('(max-width: 700px)');
  const desktopSides = new Map();
  function responsive() {
    if (narrow.matches) ids.forEach(id => { const el = document.getElementById(id); if (el.parentElement === target) { desktopSides.set(id, true); sidebar.appendChild(el); } });
    else { desktopSides.forEach((_, id) => target.appendChild(document.getElementById(id))); desktopSides.clear(); }
    document.querySelectorAll('.panel-controls button:nth-child(2)').forEach(b => { b.disabled = narrow.matches || b.closest('[data-panel-id]').dataset.locked === 'true'; });
  }
  narrow.addEventListener('change', responsive); responsive();
  const manager = document.createElement('div'); manager.className = 'layout-manager';
  manager.innerHTML = '<label for="layout-name">Workspace layouts</label><div class="layout-presets"><button type="button" data-preset="chat">Chat Focus</button><button type="button" data-preset="coding">Coding</button><button type="button" data-preset="evolution">Monitoring</button></div><input id="layout-name" maxlength="60" placeholder="Name this arrangement"><button type="button" data-action="save">Save layout</button><select aria-label="Saved layouts"><option value="">Choose layout…</option></select><button type="button" data-action="load">Load layout</button><span role="status"></span>';
  const settingsBody = document.querySelector('#drawer .db');
  if (settingsBody) {
    const heading = document.createElement('div');
    heading.className = 'settings-section-heading';
    heading.innerHTML = '<span>Workspace</span><small>panels and saved layouts</small>';
    settingsBody.appendChild(heading);
    settingsBody.appendChild(manager);
  }
  const nameInput = manager.querySelector('input'), select = manager.querySelector('select'), status = manager.querySelector('[role="status"]');
  let layouts = {};
  try { const value = JSON.parse(localStorage.getItem('evolv.named-layouts.v1') || '{}'); if (value && typeof value === 'object' && !Array.isArray(value)) layouts = value; } catch (_) {}
  const refreshOptions = () => { select.replaceChildren(new Option('Choose layout…', '')); Object.keys(layouts).sort().forEach(name => select.append(new Option(name, name))); };
  refreshOptions();
  const applyLayout = layout => {
    ids.slice().sort((a,b) => (Number(layout[a]?.order)||0)-(Number(layout[b]?.order)||0)).forEach(id => {
      const value = layout[id]; if (!value) return;
      const panel = document.getElementById(id), buttons = panel.querySelector('.panel-controls').children;
      if (panel.classList.contains('panel-collapsed') !== (value.collapsed === true)) buttons[0].click();
      (value.side === 'right' ? target : sidebar).appendChild(panel);
      if (value.size) { panel.style.setProperty('--panel-size', value.size + 'px'); panel.dataset.panelSize = String(value.size); }
    });
    persist(); responsive();
  };
  const presets = {
    chat: { 'mind-sec': {side:'left',order:0}, 'navigation-sec': {side:'left',order:1}, 'organs-sec': {side:'left',order:2,collapsed:true}, 'plan-sec': {side:'right',order:0,collapsed:true}, 'evo-sec': {side:'right',order:1,collapsed:true}, 'model-sec': {side:'right',order:2} },
    coding: { 'mind-sec': {side:'left',order:0}, 'navigation-sec': {side:'left',order:1}, 'model-sec': {side:'right',order:0}, 'plan-sec': {side:'right',order:1}, 'organs-sec': {side:'right',order:2}, 'evo-sec': {side:'right',order:3} },
    evolution: { 'mind-sec': {side:'left',order:0,collapsed:true}, 'organs-sec': {side:'left',order:1}, 'navigation-sec': {side:'left',order:2}, 'evo-sec': {side:'right',order:0}, 'model-sec': {side:'right',order:1}, 'plan-sec': {side:'right',order:2} }
  };
  manager.querySelectorAll('[data-preset]').forEach(button => button.onclick = () => { applyLayout(presets[button.dataset.preset]); status.textContent = button.textContent + ' layout applied.'; });
  manager.querySelector('[data-action="save"]').onclick = () => {
    const name = nameInput.value.trim(); if (!name) { status.textContent = 'Enter a layout name.'; return; }
    persist();
    try { layouts = {...layouts, [name]: JSON.parse(localStorage.getItem('evolv.panel-layout.v1'))}; localStorage.setItem('evolv.named-layouts.v1', JSON.stringify(layouts)); refreshOptions(); select.value = name; status.textContent = 'Layout saved.'; } catch (_) { status.textContent = 'Could not save layout.'; }
  };
  manager.querySelector('[data-action="load"]').onclick = () => {
    const layout = layouts[select.value]; if (!layout || typeof layout !== 'object') { status.textContent = 'Choose a saved layout.'; return; }
    applyLayout(layout); status.textContent = 'Layout loaded.';
  };
})();
