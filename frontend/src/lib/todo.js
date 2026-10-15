/* 纯逻辑：日期换算、输入解析、分桶。全栈版本不再注入样本数据：
   新账号的任务列表为空，生产环境没有合成历史（见 FULLSTACK.md）。 */

export const DAY = 864e5;
export const midnight = d => { const x = new Date(d); x.setHours(0,0,0,0); return +x };
export const TODAY = midnight(Date.now());


/* ── 输入解析 ──────────────────────────────────────────────────────────────
   一行字里就能带出日期和优先级。用户在手机上不会去点三个下拉菜单，
   打字是唯一真的会被用的录入方式。 */
export function parseInput(raw){
  let t = raw.trim();
  let p = 3, due = 0, g = '';
  const bang = t.match(/(^|\s)(!{1,3})(\s|$)/);
  if (bang){ p = 4 - bang[2].length; t = t.replace(bang[0], ' ') }
  const rules = [
    [/后天|day after tomorrow/i, () => TODAY + DAY*2],
    [/今天|today/i, () => TODAY],
    [/明天|tomorrow/i, () => TODAY + DAY],
    [/下周/, () => TODAY + DAY*7],
    [/(\d{1,2})[\/月-](\d{1,2})[日号]?/, m => {
      const y = new Date().getFullYear();
      const d = midnight(new Date(y, +m[1]-1, +m[2]));
      return d < TODAY ? midnight(new Date(y+1, +m[1]-1, +m[2])) : d;
    }],
  ];
  for (const [re, fn] of rules){
    const m = t.match(re);
    if (m){ due = fn(m); t = t.replace(m[0], ' '); break }
  }
  const tag = t.match(/#(\S+)/);
  if (tag){ g = tag[1]; t = t.replace(tag[0], ' ') }
  t = t.replace(/\s+/g,' ').trim();
  return { t, p, due, g: g || (p === 1 ? '工作' : '生活') };
}


export function bucketOf(i){
  if (i.done) return '已完成';
  if (!i.due) return '没排期';
  const d = midnight(i.due);
  if (d < TODAY) return '逾期';
  if (d === TODAY) return '今天';
  if (d === TODAY + DAY) return '明天';
  return '以后';
}
export const ORDER = ['逾期','今天','明天','以后','没排期','已完成'];

export function dueText(i){
  if (!i.due) return '';
  const d = midnight(i.due), diff = Math.round((d - TODAY)/DAY);
  if (diff === 0) return '今天';
  if (diff === 1) return '明天';
  if (diff === -1) return '昨天';
  if (diff < 0) return `逾期 ${-diff} 天`;
  return new Date(d).toLocaleDateString('zh-CN', { month:'numeric', day:'numeric' });
}

export const SORTS = { manual:'手动', due:'按日期', pri:'按优先级', group:'按分组' };
export const WD = ['一','二','三','四','五','六','日'];

export function visible(items, filter, sort){
  let v = items.slice();
  if (filter === 'today') v = v.filter(i => i.due && midnight(i.due) === TODAY);
  if (filter === 'open') v = v.filter(i => !i.done);
  if (filter === 'done') v = v.filter(i => i.done);
  if (sort === 'due') v.sort((a,b) => (a.due||9e15) - (b.due||9e15));
  if (sort === 'pri') v.sort((a,b) => a.p - b.p);
  if (sort === 'group') v.sort((a,b) => a.g.localeCompare(b.g,'zh'));
  return v;
}

export function loadOn(items, ts){
  const on = items.filter(i => i.due && midnight(i.due) === ts);
  return {
    late: on.filter(i => !i.done && ts < TODAY).length,
    open: on.filter(i => !i.done && ts >= TODAY).length,
    did:  on.filter(i => i.done).length
  };
}
