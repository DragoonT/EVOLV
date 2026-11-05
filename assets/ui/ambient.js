/* EVOLV ambient field — a quiet constellation behind the workspace.
   This replaces the genome iframe that used to render a full DNA scene behind
   every chat: same living feeling, a fraction of the cost. The field is
   accent-aware, DPR-capped, paused when the tab is hidden, and reduced to a
   single still frame when the visitor asks for reduced motion. */
(() => {
  'use strict';

  const stage = document.getElementById('ambient-stage');
  const canvas = document.getElementById('ambient-canvas');
  if (!stage || !canvas) return;
  const ctx = canvas.getContext('2d', { alpha: true });
  if (!ctx) return;

  const reducedMotion = window.matchMedia('(prefers-reduced-motion: reduce)');
  const LINK_DISTANCE = 132;
  let width = 0;
  let height = 0;
  let particles = [];
  let accent = { r: 201, g: 239, b: 114 };
  let raf = 0;
  let running = false;
  let last = 0;
  let resizeTimer = 0;

  const tint = alpha => `rgba(${accent.r},${accent.g},${accent.b},${alpha})`;
  // Particles wrap around the viewport with a 24px off-screen margin.
  const wrap = (value, span) => (value < -24 ? span + 24 : value > span + 24 ? -24 : value);

  function readAccent() {
    const raw = getComputedStyle(document.body).getPropertyValue('--accent').trim();
    const match = /^#?([0-9a-f]{6})$/i.exec(raw);
    if (!match) return;
    const n = parseInt(match[1], 16);
    accent = { r: (n >> 16) & 255, g: (n >> 8) & 255, b: n & 255 };
  }

  function seed() {
    // Density scales with the viewport but never becomes a particle storm.
    const count = Math.max(22, Math.min(76, Math.round((width * height) / 28000)));
    particles = Array.from({ length: count }, () => ({
      x: Math.random() * width,
      y: Math.random() * height,
      vx: (Math.random() - 0.5) * 0.34,
      vy: (Math.random() - 0.5) * 0.34,
      r: Math.random() * 1.35 + 0.45,
      phase: Math.random() * Math.PI * 2,
      speed: 0.15 + Math.random() * 0.4,
    }));
  }

  function resize() {
    const dpr = Math.min(window.devicePixelRatio || 1, 2);
    width = Math.max(1, window.innerWidth);
    height = Math.max(1, window.innerHeight);
    canvas.width = Math.round(width * dpr);
    canvas.height = Math.round(height * dpr);
    canvas.style.width = width + 'px';
    canvas.style.height = height + 'px';
    ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
    seed();
    draw();
  }

  function draw(step) {
    ctx.clearRect(0, 0, width, height);

    for (const p of particles) {
      if (step) {
        p.x += p.vx * step * 0.06;
        p.y += p.vy * step * 0.06;
        p.phase += p.speed * step * 0.001;
        p.x = wrap(p.x, width);
        p.y = wrap(p.y, height);
      }
    }

    for (let i = 0; i < particles.length; i++) {
      const a = particles[i];
      for (let j = i + 1; j < particles.length; j++) {
        const b = particles[j];
        const dx = a.x - b.x;
        const dy = a.y - b.y;
        const distance = Math.hypot(dx, dy);
        if (distance > LINK_DISTANCE) continue;
        const alpha = (1 - distance / LINK_DISTANCE) * 0.075;
        ctx.strokeStyle = tint(alpha);
        ctx.lineWidth = 0.6;
        ctx.beginPath();
        ctx.moveTo(a.x, a.y);
        ctx.lineTo(b.x, b.y);
        ctx.stroke();
      }
    }

    for (const p of particles) {
      const twinkle = 0.55 + 0.45 * Math.sin(p.phase);
      ctx.fillStyle = tint(0.10 + twinkle * 0.13);
      ctx.beginPath();
      ctx.arc(p.x, p.y, p.r, 0, Math.PI * 2);
      ctx.fill();
    }

    stage.classList.add('ambient-live');
  }

  function frame(now) {
    if (!running) return;
    const step = Math.min(48, Math.max(8, now - last || 16));
    last = now;
    draw(step);
    raf = requestAnimationFrame(frame);
  }

  function start() {
    if (running) return;
    running = true;
    last = performance.now();
    raf = requestAnimationFrame(frame);
  }

  function stop() {
    running = false;
    cancelAnimationFrame(raf);
  }

  function applyMotionPreference() {
    stop();
    if (reducedMotion.matches) draw();
    else if (!document.hidden) start();
  }

  function scheduleResize() {
    clearTimeout(resizeTimer);
    resizeTimer = setTimeout(resize, 140);
  }

  readAccent();
  resize();
  applyMotionPreference();

  window.addEventListener('resize', scheduleResize, { passive: true });
  document.addEventListener('visibilitychange', () => {
    if (document.hidden) stop();
    else applyMotionPreference();
  });
  window.addEventListener('pagehide', stop);

  if (typeof reducedMotion.addEventListener === 'function') {
    reducedMotion.addEventListener('change', applyMotionPreference);
  }

  // The appearance playground writes CSS variables onto <body>; re-read the
  // accent whenever the theme changes so the field changes colour with it.
  const observer = new MutationObserver(() => {
    readAccent();
    if (reducedMotion.matches) draw();
  });
  observer.observe(document.body, { attributes: true, attributeFilter: ['class', 'style', 'data-appearance'] });
})();
