/* 交互音效与触感反馈（移植自原 tool.js）。
   合成音而不是采样：应用需要的几声提示只是两个振荡器，没有音频资源要打包。 */
let ctx: AudioContext | false | null = null;

function ac(): AudioContext | false {
  if (ctx === false) return false;
  if (!ctx) {
    try {
      ctx = new (window.AudioContext ||
        (window as any).webkitAudioContext)();
    } catch {
      ctx = false;
    }
  }
  if (ctx && ctx.state === "suspended") {
    try {
      ctx.resume().catch(() => {});
    } catch {
      /* ignore */
    }
  }
  return ctx;
}

function voice(a: AudioContext, freq: number, dur: number, type: OscillatorType, gain: number, slideTo: number | null) {
  const o = a.createOscillator(),
    g = a.createGain();
  o.type = type;
  o.frequency.setValueAtTime(freq, a.currentTime);
  if (slideTo) o.frequency.exponentialRampToValueAtTime(slideTo, a.currentTime + dur);
  g.gain.setValueAtTime(0, a.currentTime);
  g.gain.linearRampToValueAtTime(gain, a.currentTime + 0.008);
  g.gain.exponentialRampToValueAtTime(0.0001, a.currentTime + dur);
  o.connect(g).connect(a.destination);
  o.start();
  o.stop(a.currentTime + dur + 0.02);
}

function blip(freq: number, dur = 0.08, type: OscillatorType = "sine", gain = 0.12, slideTo: number | null = null) {
  const a = ac();
  if (!a) return;
  try {
    voice(a, freq, dur, type, gain, slideTo);
  } catch {
    ctx = false;
  }
}

export const sound = {
  tick() {
    blip(1180, 0.028, "square", 0.05);
  },
  pop() {
    blip(520, 0.07, "triangle", 0.11, 880);
  },
  ding() {
    blip(880, 0.16, "sine", 0.13);
    setTimeout(() => blip(1320, 0.22, "sine", 0.1), 90);
  },
};

export function haptic(ms = 12) {
  try {
    navigator.vibrate && navigator.vibrate(ms);
  } catch {
    /* unsupported */
  }
}
