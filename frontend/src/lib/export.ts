/* 客户端导出：CSV 与 Canvas 海报（移植自原 kit.js / tool.js）。
   图片/PDF 由用户触发并在本机生成，不自动外发、不伪造“已存云端”。 */
export function download(name: string, text: string, mime = "text/csv;charset=utf-8") {
  const url = URL.createObjectURL(new Blob(["﻿" + text], { type: mime }));
  const a = Object.assign(document.createElement("a"), { href: url, download: name });
  document.body.appendChild(a);
  a.click();
  a.remove();
  setTimeout(() => URL.revokeObjectURL(url), 1000);
}

export function toCSV(rows: (string | number)[][]) {
  return rows
    .map((r) =>
      r
        .map((c) => {
          const s = String(c ?? "");
          return /[",\n]/.test(s) ? '"' + s.replace(/"/g, '""') + '"' : s;
        })
        .join(","),
    )
    .join("\n");
}

function tone(name: string) {
  return getComputedStyle(document.documentElement).getPropertyValue("--" + name).trim();
}

/* Canvas can't read a CSS variable and can't wait for a webfont on its own, so
   both are resolved here before a single pixel is drawn — otherwise the first
   poster of a session silently renders in Times. */
export async function poster(spec: PosterSpec): Promise<HTMLCanvasElement> {
  try {
    await document.fonts.ready;
  } catch {
    /* fonts API absent */
  }
  const s: Required<PosterSpec> = Object.assign(
    {
      brand: "",
      title: "",
      subtitle: "",
      hero: null,
      rows: [],
      bars: [],
      note: "",
      footer: "",
      accent: tone("accent"),
      bg: tone("panel"),
      ink: tone("ink"),
      muted: tone("muted"),
      faint: tone("faint"),
      line: tone("line"),
    },
    spec,
  );

  const sans =
    getComputedStyle(document.documentElement).getPropertyValue("--sans").trim() ||
    "system-ui, sans-serif";
  const mono =
    getComputedStyle(document.documentElement).getPropertyValue("--mono").trim() ||
    "ui-monospace, monospace";

  const P = { W: 1080, PAD: 84 };
  const ops: { t: string; y: number; text?: string; font?: string; hero?: any; bar?: any; row?: any }[] = [];
  let y = P.PAD;
  const W = P.W,
    IN = W - P.PAD * 2;

  const m = document.createElement("canvas").getContext("2d")!;
  function wrap(text: string, font: string, maxW: number) {
    m.font = font;
    const out: string[] = [];
    let line = "";
    for (const ch of String(text)) {
      if (ch === "\n") {
        out.push(line);
        line = "";
        continue;
      }
      const t = line + ch;
      if (m.measureText(t).width > maxW && line) {
        out.push(line);
        line = ch;
      } else line = t;
    }
    if (line || !out.length) out.push(line);
    return out;
  }

  if (s.brand) {
    ops.push({ t: "brand", y, text: s.brand });
    y += 46;
  }
  if (s.title) {
    const font = `600 ${s.title.length > 16 ? 52 : 64}px ${sans}`;
    wrap(s.title, font, IN).forEach((l, i) => {
      ops.push({ t: "title", y: y + i * (s.title.length > 16 ? 66 : 80), text: l, font });
    });
    y += wrap(s.title, font, IN).length * (s.title.length > 16 ? 66 : 80) + 10;
  }
  if (s.subtitle) {
    const font = `400 26px ${sans}`;
    wrap(s.subtitle, font, IN).forEach((l, i) => ops.push({ t: "sub", y: y + i * 38, text: l, font }));
    y += wrap(s.subtitle, font, IN).length * 38 + 8;
  }
  if (s.hero) {
    y += 34;
    ops.push({ t: "hero", y, hero: s.hero });
    y += 210;
  }
  if (s.bars.length) {
    y += 12;
    s.bars.forEach((b) => {
      ops.push({ t: "bar", y, bar: b });
      y += 74;
    });
    y += 6;
  }
  if (s.rows.length) {
    y += 14;
    ops.push({ t: "rule", y });
    y += 26;
    s.rows.forEach((r) => {
      ops.push({ t: "row", y, row: r });
      y += 58;
    });
    y += 4;
  }
  if (s.note) {
    y += 26;
    const font = `400 24px ${sans}`;
    wrap(s.note, font, IN).forEach((l, i) => ops.push({ t: "note", y: y + i * 38, text: l, font }));
    y += wrap(s.note, font, IN).length * 38;
  }
  y += 40;
  ops.push({ t: "rule", y });
  y += 44;
  ops.push({ t: "footer", y, text: s.footer || "" });
  y += 30 + P.PAD;

  const H = Math.max(1080, Math.round(y));
  const cv = document.createElement("canvas");
  cv.width = W;
  cv.height = H;
  const c = cv.getContext("2d")!;

  c.fillStyle = s.bg;
  c.fillRect(0, 0, W, H);

  // A faint deterministic grain keeps a flat export from looking like a
  // screenshot of a colour swatch. Seeded, so the same result exports twice
  // to the same file.
  let seed = 0x5eed;
  const rnd = () => ((seed = (seed * 1103515245 + 12345) & 0x7fffffff) / 0x7fffffff);
  c.save();
  c.globalAlpha = 0.028;
  c.fillStyle = s.ink;
  for (let i = 0; i < 2600; i++) c.fillRect((rnd() * W) | 0, (rnd() * H) | 0, 1.6, 1.6);
  c.restore();

  // Accent spine down the left margin: one mark that makes the poster legible
  // as "this app's" at thumbnail size.
  c.fillStyle = s.accent;
  c.fillRect(0, 0, 10, H);

  c.textBaseline = "alphabetic";
  for (const op of ops) {
    if (op.t === "brand") {
      c.font = `500 22px ${mono}`;
      c.fillStyle = s.accent;
      c.fillText(op.text!.toUpperCase(), P.PAD, op.y + 20);
    } else if (op.t === "title") {
      c.font = op.font!;
      c.fillStyle = s.ink;
      c.fillText(op.text!, P.PAD, op.y + 56);
    } else if (op.t === "sub") {
      c.font = op.font!;
      c.fillStyle = s.muted;
      c.fillText(op.text!, P.PAD, op.y + 26);
    } else if (op.t === "hero") {
      const h = op.hero;
      c.font = `600 168px ${mono}`;
      c.fillStyle = s.ink;
      c.fillText(h.value, P.PAD, op.y + 150);
      const vw = c.measureText(h.value).width;
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
    } else if (op.t === "bar") {
      const b = op.bar;
      c.font = `500 26px ${sans}`;
      c.fillStyle = s.ink;
      c.fillText(b.label, P.PAD, op.y + 26);
      c.font = `500 26px ${mono}`;
      c.fillStyle = s.muted;
      c.textAlign = "right";
      c.fillText(b.text != null ? b.text : b.value, W - P.PAD, op.y + 26);
      c.textAlign = "left";
      const bw = IN,
        bh = 12,
        by = op.y + 44;
      c.fillStyle = s.line;
      roundRect(c, P.PAD, by, bw, bh, 6);
      c.fill();
      c.fillStyle = b.color || s.accent;
      const frac = Math.max(0, Math.min(1, b.value / (b.max || 100)));
      if (frac > 0) {
        roundRect(c, P.PAD, by, Math.max(bh, bw * frac), bh, 6);
        c.fill();
      }
    } else if (op.t === "row") {
      c.font = `400 27px ${sans}`;
      c.fillStyle = s.muted;
      c.fillText(op.row[0], P.PAD, op.y + 26);
      c.font = `500 29px ${mono}`;
      c.fillStyle = op.row[2] || s.ink;
      c.textAlign = "right";
      c.fillText(op.row[1], W - P.PAD, op.y + 26);
      c.textAlign = "left";
    } else if (op.t === "rule") {
      c.fillStyle = s.line;
      c.fillRect(P.PAD, op.y, IN, 2);
    } else if (op.t === "note") {
      c.font = op.font!;
      c.fillStyle = s.muted;
      c.fillText(op.text!, P.PAD, op.y + 26);
    } else if (op.t === "footer") {
      c.font = `400 21px ${mono}`;
      c.fillStyle = s.faint;
      c.fillText(op.text!, P.PAD, op.y);
    }
  }
  return cv;
}

export interface PosterSpec {
  brand?: string;
  title?: string;
  subtitle?: string;
  hero?: { value: string; unit?: string; caption?: string } | null;
  rows?: string[][];
  bars?: { label: string; value: number; max?: number; text?: string; color?: string }[];
  note?: string;
  footer?: string;
  accent?: string;
  bg?: string;
  ink?: string;
  muted?: string;
  faint?: string;
  line?: string;
}

function roundRect(c: CanvasRenderingContext2D, x: number, y: number, w: number, h: number, r: number) {
  c.beginPath();
  if (c.roundRect) c.roundRect(x, y, w, h, r);
  else {
    c.moveTo(x + r, y);
    c.arcTo(x + w, y, x + w, y + h, r);
    c.arcTo(x + w, y + h, x, y + h, r);
    c.arcTo(x, y + h, x, y, r);
    c.arcTo(x, y, x + w, y, r);
    c.closePath();
  }
}

/* Shows the poster rather than downloading it straight off: on iOS a
   programmatic download of a blob does nothing visible, while a long-press on
   a visible <img> is the gesture people already know. */
export function showPoster(cv: HTMLCanvasElement, { name = "poster.png", tip = "长按图片保存 · 或点下载" } = {}) {
  const scrim = document.createElement("div");
  scrim.className = "poster-scrim";
  const img = new Image();
  img.src = cv.toDataURL("image/png");
  img.alt = "结果海报";
  const acts = document.createElement("div");
  acts.className = "acts";
  const dl = Object.assign(document.createElement("button"), { className: "btn primary", textContent: "下载图片" });
  const close = Object.assign(document.createElement("button"), { className: "btn", textContent: "关闭" });
  dl.onclick = () => {
    cv.toBlob((b) => {
      if (!b) return;
      const url = URL.createObjectURL(b);
      const a = Object.assign(document.createElement("a"), { href: url, download: name });
      document.body.appendChild(a);
      a.click();
      a.remove();
      setTimeout(() => URL.revokeObjectURL(url), 1000);
    }, "image/png");
  };
  const bye = () => {
    scrim.classList.remove("in");
    setTimeout(() => scrim.remove(), 300);
  };
  close.onclick = bye;
  scrim.onclick = (e) => {
    if (e.target === scrim) bye();
  };
  acts.append(dl, close);
  const t = Object.assign(document.createElement("p"), { className: "tip", textContent: tip });
  scrim.append(img, acts, t);
  document.body.appendChild(scrim);
  requestAnimationFrame(() => scrim.classList.add("in"));
  return scrim;
}
