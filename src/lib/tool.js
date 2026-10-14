import Kit from './kit.js';
/* Shared tool behaviour, loaded after kit.js. Two pieces do the heavy lifting:
 *
 *   A. Tool.poster — the outcome of a run has to be a picture someone can send.
 *      Painting it with Canvas 2D rather than screenshotting the DOM is the
 *      whole point: the poster is a designed 1080-wide artifact with its own
 *      margins and type scale, not a crop of a phone screen.
 *
 *   B. Tool.share — a two-end flow with no backend. The config is deflated and
 *      base64url'd into the hash, so a link is the entire server.
 *
 * The rest (screens, sheet, sound, confetti, store) is the shell those two sit
 * in.
 */
const Tool = (() => {

  /* ── storage ───────────────────────────────────────────────────────────── */
  /* Private mode throws on every localStorage call, and a tool that dies in a
     WeChat webview is worse than one that forgets. Every access is guarded. */
  function store(key, fallback) {
    return {
      get() {
        try {
          const raw = localStorage.getItem(key);
          return raw == null ? structuredClone(fallback) : JSON.parse(raw);
        } catch (e) { return structuredClone(fallback) }
      },
      set(v) {
        try { localStorage.setItem(key, JSON.stringify(v)) } catch (e) { /* ignore */ }
        return v;
      },
      clear() { try { localStorage.removeItem(key) } catch (e) { /* ignore */ } }
    };
  }

  /* ── 双端编码链接 ───────────────────────────────────────────────────────── */
  const b64u = {
    enc(bytes) {
      let s = '';
      for (const b of bytes) s += String.fromCharCode(b);
      return btoa(s).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
    },
    dec(str) {
      const s = atob(str.replace(/-/g, '+').replace(/_/g, '/'));
      const out = new Uint8Array(s.length);
      for (let i = 0; i < s.length; i++) out[i] = s.charCodeAt(i);
      return out;
    }
  };

  async function squeeze(bytes, mode) {
    if (!self.CompressionStream) return null;
    const cs = new CompressionStream(mode);
    const w = cs.writable.getWriter();
    w.write(bytes); w.close();
    return new Uint8Array(await new Response(cs.readable).arrayBuffer());
  }
  async function unsqueeze(bytes, mode) {
    const ds = new DecompressionStream(mode);
    const w = ds.writable.getWriter();
    w.write(bytes); w.close();
    return new Uint8Array(await new Response(ds.readable).arrayBuffer());
  }

  const share = {
    /* One leading char says how the rest was packed, so a payload made before
       CompressionStream existed still decodes after it arrives. */
    async pack(obj) {
      const raw = new TextEncoder().encode(JSON.stringify(obj));
      const z = await squeeze(raw, 'deflate-raw').catch(() => null);
      return z && z.length < raw.length ? 'z' + b64u.enc(z) : 'p' + b64u.enc(raw);
    },
    async unpack(str) {
      if (!str) return null;
      try {
        const tag = str[0], body = b64u.dec(str.slice(1));
        const raw = tag === 'z' ? await unsqueeze(body, 'deflate-raw') : body;
        return JSON.parse(new TextDecoder().decode(raw));
      } catch (e) { return null }
    },
    /* The hash, not the query string: it never reaches a server log, and a
       10 KB question bank in a query string trips proxies that don't care about
       fragments. */
    async link(obj, param = 'd') {
      const code = await share.pack(obj);
      const u = new URL(location.href);
      u.hash = param + '=' + code;
      return u.toString();
    },
    async read(param = 'd') {
      const m = location.hash.slice(1).match(new RegExp('(?:^|&)' + param + '=([^&]+)'));
      return m ? share.unpack(m[1]) : null;
    },
    has(param = 'd') { return new RegExp('(?:^|&)' + param + '=').test(location.hash.slice(1)) }
  };

  async function copy(text) {
    try {
      await navigator.clipboard.writeText(text);
    } catch (e) {
      // Clipboard API needs a secure context; file:// previews aren't one.
      const ta = document.createElement('textarea');
      ta.value = text;
      ta.style.cssText = 'position:fixed;opacity:0';
      document.body.appendChild(ta);
      ta.select();
      try { document.execCommand('copy') } catch (e2) { /* give up quietly */ }
      ta.remove();
    }
    Kit.toast('已复制到剪贴板');
  }

  /* ── 结果 → 海报 ────────────────────────────────────────────────────────── */
  const P = { W: 1080, PAD: 84 };

  function tone(name) { return getComputedStyle(document.documentElement).getPropertyValue('--' + name).trim() }

  /* Canvas can't read a CSS variable and can't wait for a webfont on its own, so
     both are resolved here before a single pixel is drawn — otherwise the first
     poster of a session silently renders in Times. */
  async function poster(spec) {
    try { await document.fonts.ready } catch (e) { /* fonts API absent */ }
    const s = Object.assign({
      brand: '', title: '', subtitle: '', hero: null, rows: [], bars: [],
      note: '', footer: '', accent: tone('accent'),
      bg: tone('panel'), ink: tone('ink'), muted: tone('muted'),
      faint: tone('faint'), line: tone('line')
    }, spec);

    const sans = getComputedStyle(document.documentElement).getPropertyValue('--sans').trim()
      || 'system-ui, sans-serif';
    const mono = getComputedStyle(document.documentElement).getPropertyValue('--mono').trim()
      || 'ui-monospace, monospace';

    // Two passes: measure into a layout list, then paint. Height has to be known
    // before the canvas exists, and re-measuring during paint drifts.
    const ops = [];
    let y = P.PAD;
    const W = P.W, IN = W - P.PAD * 2;

    const m = document.createElement('canvas').getContext('2d');
    function wrap(text, font, maxW) {
      m.font = font;
      const out = [];
      let line = '';
      for (const ch of String(text)) {
        if (ch === '\n') { out.push(line); line = ''; continue }
        const t = line + ch;
        if (m.measureText(t).width > maxW && line) { out.push(line); line = ch }
        else line = t;
      }
      if (line || !out.length) out.push(line);
      return out;
    }

    if (s.brand) {
      ops.push({ t: 'brand', y, text: s.brand });
      y += 46;
    }
    if (s.title) {
      const font = `600 ${s.title.length > 16 ? 52 : 64}px ${sans}`;
      wrap(s.title, font, IN).forEach((l, i) => {
        ops.push({ t: 'title', y: y + i * (s.title.length > 16 ? 66 : 80), text: l, font });
      });
      y += wrap(s.title, font, IN).length * (s.title.length > 16 ? 66 : 80) + 10;
    }
    if (s.subtitle) {
      const font = `400 26px ${sans}`;
      wrap(s.subtitle, font, IN).forEach((l, i) => ops.push({ t: 'sub', y: y + i * 38, text: l, font }));
      y += wrap(s.subtitle, font, IN).length * 38 + 8;
    }
    if (s.hero) {
      y += 34;
      ops.push({ t: 'hero', y, hero: s.hero });
      y += 210;
    }
    if (s.bars.length) {
      y += 12;
      s.bars.forEach(b => { ops.push({ t: 'bar', y, bar: b }); y += 74 });
      y += 6;
    }
    if (s.rows.length) {
      y += 14;
      ops.push({ t: 'rule', y }); y += 26;
      s.rows.forEach(r => { ops.push({ t: 'row', y, row: r }); y += 58 });
      y += 4;
    }
    if (s.note) {
      y += 26;
      const font = `400 24px ${sans}`;
      wrap(s.note, font, IN).forEach((l, i) => ops.push({ t: 'note', y: y + i * 38, text: l, font }));
      y += wrap(s.note, font, IN).length * 38;
    }
    y += 40;
    ops.push({ t: 'rule', y }); y += 44;
    ops.push({ t: 'footer', y, text: s.footer || '' });
    y += 30 + P.PAD;

    const H = Math.max(1080, Math.round(y));
    const cv = document.createElement('canvas');
    cv.width = W; cv.height = H;
    const c = cv.getContext('2d');

    c.fillStyle = s.bg;
    c.fillRect(0, 0, W, H);

    // A faint deterministic grain keeps a flat export from looking like a
    // screenshot of a colour swatch. Seeded, so the same result exports twice
    // to the same file.
    const rnd = Kit.rng(0x5eed);
    c.save();
    c.globalAlpha = .028;
    c.fillStyle = s.ink;
    for (let i = 0; i < 2600; i++) c.fillRect(rnd() * W | 0, rnd() * H | 0, 1.6, 1.6);
    c.restore();

    // Accent spine down the left margin: one mark that makes the poster legible
    // as "this app's" at thumbnail size.
    c.fillStyle = s.accent;
    c.fillRect(0, 0, 10, H);

    c.textBaseline = 'alphabetic';
    for (const op of ops) {
      if (op.t === 'brand') {
        c.font = `500 22px ${mono}`;
        c.fillStyle = s.accent;
        c.letterSpacing = '2.6px';
        c.fillText(op.text.toUpperCase(), P.PAD, op.y + 20);
        c.letterSpacing = '0px';
      } else if (op.t === 'title') {
        c.font = op.font; c.fillStyle = s.ink;
        c.letterSpacing = '-1.4px';
        c.fillText(op.text, P.PAD, op.y + 56);
        c.letterSpacing = '0px';
      } else if (op.t === 'sub') {
        c.font = op.font; c.fillStyle = s.muted;
        c.fillText(op.text, P.PAD, op.y + 26);
      } else if (op.t === 'hero') {
        const h = op.hero;
        c.font = `600 168px ${mono}`;
        c.fillStyle = s.ink;
        c.letterSpacing = '-6px';
        c.fillText(h.value, P.PAD, op.y + 150);
        const vw = c.measureText(h.value).width;
        c.letterSpacing = '0px';
        if (h.unit) {
          c.font = `500 40px ${sans}`;
          c.fillStyle = s.accent;
          c.fillText(h.unit, P.PAD + vw + 18, op.y + 150);
        }
        if (h.caption) {
          c.font = `500 24px ${sans}`;
          c.fillStyle = s.faint;
          c.fillText(h.caption, P.PAD, op.y + 196);
        }
      } else if (op.t === 'bar') {
        const b = op.bar;
        c.font = `500 26px ${sans}`; c.fillStyle = s.ink;
        c.fillText(b.label, P.PAD, op.y + 26);
        c.font = `500 26px ${mono}`; c.fillStyle = s.muted;
        c.textAlign = 'right';
        c.fillText(b.text != null ? b.text : b.value, W - P.PAD, op.y + 26);
        c.textAlign = 'left';
        const bw = IN, bh = 12, by = op.y + 44;
        c.fillStyle = s.line;
        roundRect(c, P.PAD, by, bw, bh, 6); c.fill();
        c.fillStyle = b.color || s.accent;
        const frac = Math.max(0, Math.min(1, b.value / (b.max || 100)));
        if (frac > 0) { roundRect(c, P.PAD, by, Math.max(bh, bw * frac), bh, 6); c.fill() }
      } else if (op.t === 'row') {
        c.font = `400 27px ${sans}`; c.fillStyle = s.muted;
        c.fillText(op.row[0], P.PAD, op.y + 26);
        c.font = `500 29px ${mono}`; c.fillStyle = op.row[2] || s.ink;
        c.textAlign = 'right';
        c.fillText(op.row[1], W - P.PAD, op.y + 26);
        c.textAlign = 'left';
      } else if (op.t === 'rule') {
        c.fillStyle = s.line;
        c.fillRect(P.PAD, op.y, IN, 2);
      } else if (op.t === 'note') {
        c.font = op.font; c.fillStyle = s.muted;
        c.fillText(op.text, P.PAD, op.y + 26);
      } else if (op.t === 'footer') {
        c.font = `400 21px ${mono}`; c.fillStyle = s.faint;
        c.fillText(op.text, P.PAD, op.y);
      }
    }
    return cv;
  }

  function roundRect(c, x, y, w, h, r) {
    c.beginPath();
    if (c.roundRect) c.roundRect(x, y, w, h, r);
    else {
      c.moveTo(x + r, y); c.arcTo(x + w, y, x + w, y + h, r);
      c.arcTo(x + w, y + h, x, y + h, r); c.arcTo(x, y + h, x, y, r);
      c.arcTo(x, y, x + w, y, r); c.closePath();
    }
  }

  /* Shows the poster rather than downloading it straight off: on iOS a
     programmatic download of a blob does nothing visible, while a long-press on
     a visible <img> is the gesture people already know. */
  function showPoster(cv, { name = 'poster.png', tip = '长按图片保存 · 或点下载' } = {}) {
    const scrim = document.createElement('div');
    scrim.className = 'poster-scrim';
    const img = new Image();
    img.src = cv.toDataURL('image/png');
    img.alt = '结果海报';
    const acts = document.createElement('div');
    acts.className = 'acts';
    const dl = Object.assign(document.createElement('button'), { className: 'btn primary', textContent: '下载图片' });
    const close = Object.assign(document.createElement('button'), { className: 'btn', textContent: '关闭' });
    dl.onclick = () => {
      cv.toBlob(b => {
        const url = URL.createObjectURL(b);
        const a = Object.assign(document.createElement('a'), { href: url, download: name });
        document.body.appendChild(a); a.click(); a.remove();
        setTimeout(() => URL.revokeObjectURL(url), 1000);
      }, 'image/png');
    };
    const bye = () => { scrim.classList.remove('in'); setTimeout(() => scrim.remove(), 300) };
    close.onclick = bye;
    scrim.onclick = e => { if (e.target === scrim) bye() };
    acts.append(dl, close);
    const t = Object.assign(document.createElement('p'), { className: 'tip', textContent: tip });
    scrim.append(img, acts, t);
    document.body.appendChild(scrim);
    requestAnimationFrame(() => scrim.classList.add('in'));
    return scrim;
  }

  /* ── screens ───────────────────────────────────────────────────────────── */
  /* A stack, not a router: the flow here is linear (设置 → 进行 → 结果) with one
     back edge, and a URL-driven router would fight the share hash. */
  function screens(host) {
    if (typeof host === 'string') host = document.querySelector(host);
    const all = [...host.querySelectorAll('.screen')];
    const stack = [];
    all.forEach((s, i) => { s.hidden = i !== 0; if (i === 0) s.classList.add('in') });
    stack.push(all[0]?.dataset.screen);

    function el(id) { return all.find(s => s.dataset.screen === id) }

    function go(id, { replace = false, back = false } = {}) {
      const from = el(stack[stack.length - 1]);
      const to = el(id);
      if (!to || from === to) return;
      to.hidden = false;
      to.classList.remove('in');
      to.classList.add(back ? 'out-left' : 'out-right');
      to.scrollTop = 0;
      requestAnimationFrame(() => requestAnimationFrame(() => {
        to.classList.remove('out-left', 'out-right');
        to.classList.add('in');
        if (from) {
          from.classList.remove('in');
          from.classList.add(back ? 'out-right' : 'out-left');
          setTimeout(() => { if (!from.classList.contains('in')) from.hidden = true }, 400);
        }
      }));
      if (back) stack.pop();
      else if (replace) stack[stack.length - 1] = id;
      else stack.push(id);
      dispatchEvent(new CustomEvent('tool:screen', { detail: id }));
    }
    function back() { if (stack.length > 1) go(stack[stack.length - 2], { back: true }) }
    return { go, back, current: () => stack[stack.length - 1], depth: () => stack.length };
  }

  /* ── bottom sheet ──────────────────────────────────────────────────────── */
  function sheet({ title = '', body, actions = [] }) {
    const scrim = document.createElement('div');
    scrim.className = 'sheet-scrim';
    const el = document.createElement('div');
    el.className = 'sheet';
    el.innerHTML = '<div class="grip"></div>'
      + `<div class="sheet-head"><h3>${title}</h3><span class="spacer" style="flex:1"></span></div>`
      + '<div class="sheet-body"></div>';
    const bodyEl = el.querySelector('.sheet-body');
    if (typeof body === 'string') bodyEl.innerHTML = body;
    else if (body) bodyEl.appendChild(body);

    const close = () => {
      el.classList.remove('in'); scrim.classList.remove('in');
      setTimeout(() => { el.remove(); scrim.remove() }, 340);
    };
    const x = Object.assign(document.createElement('button'), { className: 'ib', innerHTML: ICON.x });
    x.onclick = close;
    el.querySelector('.sheet-head').appendChild(x);

    if (actions.length) {
      const bar = document.createElement('div');
      bar.className = 'bottombar';
      bar.style.borderTop = '1px solid var(--line)';
      actions.forEach(a => {
        const b = Object.assign(document.createElement('button'), {
          className: 'btn' + (a.primary ? ' primary' : ''), textContent: a.label
        });
        b.onclick = () => { if (a.onClick && a.onClick() === false) return; close() };
        bar.appendChild(b);
      });
      el.appendChild(bar);
    }
    scrim.onclick = close;
    document.body.append(scrim, el);
    requestAnimationFrame(() => { scrim.classList.add('in'); el.classList.add('in') });
    return { el, body: bodyEl, close };
  }

  /* ── sound ─────────────────────────────────────────────────────────────── */
  /* Synthesised rather than sampled: the few sounds the app needs are two
     oscillators, with no audio assets to ship. */
  const soundStore = store('tool-muted', false);
  let ctx = null, muted = soundStore.get();
  /* Audio is decoration; a machine without a working output device must lose the
     blips, not throw. resume() returns a promise that rejects on a kiosk, a VM
     or a headless browser — unhandled, that surfaces as a page error on a page
     whose only sin was ticking while a wheel spun. */
  function ac() {
    if (ctx === false) return false;
    if (!ctx) { try { ctx = new (self.AudioContext || self.webkitAudioContext)() } catch (e) { ctx = false } }
    if (ctx && ctx.state === 'suspended') { try { ctx.resume().catch(() => {}) } catch (e) { /* ignore */ } }
    return ctx;
  }
  function blip(freq, dur = .08, type = 'sine', gain = .12, slideTo = null) {
    if (muted) return;
    const a = ac(); if (!a) return;
    try { voice(a, freq, dur, type, gain, slideTo) } catch (e) { ctx = false }
  }
  function voice(a, freq, dur, type, gain, slideTo) {
    const o = a.createOscillator(), g = a.createGain();
    o.type = type;
    o.frequency.setValueAtTime(freq, a.currentTime);
    if (slideTo) o.frequency.exponentialRampToValueAtTime(slideTo, a.currentTime + dur);
    g.gain.setValueAtTime(0, a.currentTime);
    g.gain.linearRampToValueAtTime(gain, a.currentTime + .008);
    g.gain.exponentialRampToValueAtTime(.0001, a.currentTime + dur);
    o.connect(g).connect(a.destination);
    o.start(); o.stop(a.currentTime + dur + .02);
  }
  const sound = {
    tick() { blip(1180, .028, 'square', .05) },
    pop() { blip(520, .07, 'triangle', .11, 880) },
    ding() { blip(880, .16, 'sine', .13); setTimeout(() => blip(1320, .22, 'sine', .1), 90) },
    err() { blip(220, .16, 'sawtooth', .07, 130) },
    win() {
      [0, 110, 220, 400].forEach((d, i) => setTimeout(() => blip([523, 659, 784, 1047][i], .34, 'triangle', .12), d));
    },
    get muted() { return muted },
    set muted(v) { muted = !!v; soundStore.set(muted) },
    toggle() { sound.muted = !muted; return muted }
  };
  function haptic(ms = 12) { try { navigator.vibrate && navigator.vibrate(ms) } catch (e) { /* unsupported */ } }

  /* ── confetti ──────────────────────────────────────────────────────────── */
  function confetti({ n = 140, colors, origin = { x: .5, y: .38 }, spread = 1, duration = 3200 } = {}) {
    if (matchMedia('(prefers-reduced-motion: reduce)').matches) return;
    const cv = document.createElement('canvas');
    cv.style.cssText = 'position:fixed;inset:0;z-index:150;pointer-events:none';
    const dpr = Math.min(2, devicePixelRatio || 1);
    const w = cv.width = innerWidth * dpr, h = cv.height = innerHeight * dpr;
    cv.style.width = innerWidth + 'px'; cv.style.height = innerHeight + 'px';
    document.body.appendChild(cv);
    const c = cv.getContext('2d');
    const cols = colors || [tone('accent'), tone('pos'), tone('warn'), tone('ink')];
    const r = Kit.rng(7);
    const bits = Array.from({ length: n }, () => ({
      x: origin.x * w + (r() - .5) * 90 * dpr,
      y: origin.y * h,
      vx: (r() - .5) * 15 * spread * dpr,
      vy: (-9 - r() * 12) * spread * dpr,
      w: (5 + r() * 7) * dpr,
      h: (7 + r() * 11) * dpr,
      rot: r() * 6.28,
      vr: (r() - .5) * .34,
      col: cols[(r() * cols.length) | 0],
      life: 1
    }));
    const t0 = performance.now();
    (function frame(t) {
      const age = t - t0;
      c.clearRect(0, 0, w, h);
      bits.forEach(b => {
        b.vy += .42 * dpr;
        b.vx *= .992;
        b.x += b.vx; b.y += b.vy; b.rot += b.vr;
        b.life = Math.max(0, 1 - age / duration);
        c.save();
        c.translate(b.x, b.y);
        c.rotate(b.rot);
        c.globalAlpha = b.life;
        c.fillStyle = b.col;
        // Scaling height by cos gives the flutter of a real paper scrap without
        // a second axis of physics.
        c.fillRect(-b.w / 2, -b.h / 2, b.w, b.h * Math.abs(Math.cos(b.rot * 1.6)));
        c.restore();
      });
      if (age < duration) requestAnimationFrame(frame);
      else cv.remove();
    })(t0);
  }

  /* ── timing ────────────────────────────────────────────────────────────── */
  const mmss = ms => {
    const s = Math.max(0, Math.round(ms / 1000));
    const h = (s / 3600) | 0, m = ((s % 3600) / 60) | 0, sec = s % 60;
    const p = n => String(n).padStart(2, '0');
    return h ? `${h}:${p(m)}:${p(sec)}` : `${p(m)}:${p(sec)}`;
  };

  /* rAF rather than setInterval: a backgrounded tab throttles intervals to once
     a second and the clock silently drifts, which on a timed exam is a bug that
     only shows up in the score. Elapsed is always recomputed from Date.now(). */
  function timer(el, { mode = 'up', from = 0, onEnd, onTick } = {}) {
    if (typeof el === 'string') el = document.querySelector(el);
    let t0 = Date.now(), raf = 0, stopped = false, paused = 0, pausedAt = 0;
    function read() {
      const passed = Date.now() - t0 - paused;
      return mode === 'down' ? Math.max(0, from - passed) : passed;
    }
    function step() {
      const v = read();
      if (el) el.textContent = mmss(v);
      if (onTick) onTick(v);
      if (mode === 'down' && v <= 0) { stopped = true; if (onEnd) onEnd(); return }
      if (!stopped) raf = requestAnimationFrame(step);
    }
    step();
    return {
      read, get value() { return read() },
      pause() { if (!pausedAt) { pausedAt = Date.now(); stopped = true; cancelAnimationFrame(raf) } },
      resume() { if (pausedAt) { paused += Date.now() - pausedAt; pausedAt = 0; stopped = false; step() } },
      stop() { stopped = true; cancelAnimationFrame(raf); return read() },
      reset() { t0 = Date.now(); paused = 0; pausedAt = 0; stopped = false; cancelAnimationFrame(raf); step() }
    };
  }

  /* ── odds and ends ─────────────────────────────────────────────────────── */
  const ICON = {
    x: '<svg viewBox="0 0 24 24"><path d="M6 6l12 12M18 6L6 18"/></svg>',
    back: '<svg viewBox="0 0 24 24"><path d="M15 5l-7 7 7 7"/></svg>',
    share: '<svg viewBox="0 0 24 24"><path d="M12 15V3M8 7l4-4 4 4M4 14v5a2 2 0 002 2h12a2 2 0 002-2v-5"/></svg>',
    image: '<svg viewBox="0 0 24 24"><rect x="3" y="4" width="18" height="16" rx="2"/><circle cx="8.5" cy="9.5" r="1.6"/><path d="M21 16l-5-5-6 6-3-3-4 4"/></svg>',
    print: '<svg viewBox="0 0 24 24"><path d="M6 9V3h12v6M6 18H4v-6h16v6h-2M8 14h8v7H8z"/></svg>',
    plus: '<svg viewBox="0 0 24 24"><path d="M12 5v14M5 12h14"/></svg>',
    trash: '<svg viewBox="0 0 24 24"><path d="M4 7h16M9 7V4h6v3M6 7l1 13h10l1-13"/></svg>',
    check: '<svg viewBox="0 0 24 24"><path d="M4 12.5l5 5L20 6.5"/></svg>',
    sound: '<svg viewBox="0 0 24 24"><path d="M4 9v6h4l5 4V5L8 9H4zM17 9.5a4 4 0 010 5"/></svg>',
    mute: '<svg viewBox="0 0 24 24"><path d="M4 9v6h4l5 4V5L8 9H4zM17 10l4 4M21 10l-4 4"/></svg>',
    gear: '<svg viewBox="0 0 24 24"><circle cx="12" cy="12" r="3"/><path d="M12 2v3M12 19v3M2 12h3M19 12h3M4.9 4.9l2.2 2.2M16.9 16.9l2.2 2.2M19.1 4.9l-2.2 2.2M7.1 16.9l-2.2 2.2"/></svg>',
    dice: '<svg viewBox="0 0 24 24"><rect x="3" y="3" width="18" height="18" rx="3"/><circle cx="8.5" cy="8.5" r="1.3" fill="currentColor" stroke="none"/><circle cx="15.5" cy="15.5" r="1.3" fill="currentColor" stroke="none"/><circle cx="12" cy="12" r="1.3" fill="currentColor" stroke="none"/></svg>'
  };

  function el(tag, attrs = {}, ...kids) {
    const n = document.createElement(tag);
    for (const [k, v] of Object.entries(attrs)) {
      if (k === 'class') n.className = v;
      else if (k === 'html') n.innerHTML = v;
      else if (k === 'text') n.textContent = v;
      else if (k.startsWith('on')) n.addEventListener(k.slice(2).toLowerCase(), v);
      else if (v != null && v !== false) n.setAttribute(k, v === true ? '' : v);
    }
    kids.flat().forEach(k => k != null && n.append(k));
    return n;
  }

  /* Switches are buttons with aria-checked, so the state reads correctly to a
     screen reader and to any automation driving real clicks. */
  function switchRow(host, { label, desc, on = false, onChange }) {
    const sw = el('button', { class: 'sw' + (on ? ' on' : ''), type: 'button',
      role: 'switch', 'aria-checked': String(!!on) });
    const row = el('div', { class: 'swrow' },
      el('div', { class: 'txt' }, el('div', { class: 't', text: label }), desc ? el('div', { class: 'd', text: desc }) : null),
      sw);
    sw.onclick = () => {
      const next = sw.getAttribute('aria-checked') !== 'true';
      sw.setAttribute('aria-checked', String(next));
      // The class carries no styling the attribute doesn't, but an attribute-only
      // flip is easy to miss when the DOM is checked from the outside.
      sw.classList.toggle('on', next);
      haptic(8); sound.tick();
      if (onChange) onChange(next);
    };
    if (host) host.appendChild(row);
    return { row, sw, get value() { return sw.getAttribute('aria-checked') === 'true' } };
  }

  function printPDF() {
    Kit.toast('在打印对话框里选「存储为 PDF」');
    setTimeout(() => print(), 260);
  }

  /* Standard export trio, wired into a topbar in one call. */
  function exportBar(host, { poster: makePoster, link: makeLink, print: doPrint = true, name = 'result' }) {
    if (typeof host === 'string') host = document.querySelector(host);
    if (!host) return;
    if (makePoster) {
      const b = el('button', { class: 'ib', title: '导出图片', html: ICON.image });
      b.onclick = async () => { showPoster(await makePoster(), { name: name + '.png' }) };
      host.appendChild(b);
    }
    if (doPrint) {
      const b = el('button', { class: 'ib', title: '导出 PDF', html: ICON.print });
      b.onclick = printPDF;
      host.appendChild(b);
    }
    if (makeLink) {
      const b = el('button', { class: 'ib', title: '复制分享链接', html: ICON.share });
      b.onclick = async () => copy(await makeLink());
      host.appendChild(b);
    }
  }

  return {
    store, share, copy, poster, showPoster, screens, sheet, sound, haptic, confetti,
    timer, mmss, el, switchRow, printPDF, exportBar, ICON, tone, roundRect
  };
})();

export default Tool;
