import { DAY, TODAY, midnight } from '../lib/todo.js'
import type { Item } from '../lib/types'

export default function Stats({ items }: { items: Item[] }) {
  const open = items.filter(i => !i.done)
  const done = items.filter(i => i.done)
  const late = open.filter(i => i.due && midnight(i.due) < TODAY)

  const week: { d: number; n: number }[] = []
  for (let k = 6; k >= 0; k--) {
    const d = TODAY - k * DAY
    week.push({ d, n: items.filter(i => i.done && i.at && midnight(i.at) === d).length })
  }
  const max = Math.max(1, ...week.map(w => w.n))

  const byG: Record<string, { n: number; d: number }> = {}
  items.forEach(i => { byG[i.g] = byG[i.g] || { n: 0, d: 0 }; byG[i.g].n++; if (i.done) byG[i.g].d++ })

  return (
    <>
      <div className="stats">
        <div><div className="n">{items.length}</div><div className="l">全部</div></div>
        <div><div className="n">{open.length}</div><div className="l">未完成</div></div>
        <div><div className="n">{done.length}</div><div className="l">已完成</div></div>
        <div>
          <div className="n" style={late.length ? { color: 'var(--neg)' } : undefined}>{late.length}</div>
          <div className="l">逾期</div>
        </div>
      </div>

      <div className="grouphead"><span>近 7 天完成</span></div>
      <div className="weekbar">
        {week.map(w => (
          <div className="d" key={w.d}>
            <div className="b" style={{ height: (w.n / max * 58 + 2) + 'px' }} />
            <div className="l">{'日一二三四五六'[new Date(w.d).getDay()]}</div>
          </div>
        ))}
      </div>

      <div className="grouphead"><span>按分组</span></div>
      {Object.entries(byG).sort((a, b) => b[1].n - a[1].n).map(([g, v]) => (
        <div className="item" key={g} style={{ '--pri': 'transparent' } as React.CSSProperties}>
          <div className="body">
            <div className="txt">{g}</div>
            <div className="meta"><span>{v.d + ' / ' + v.n + ' 已完成'}</span></div>
          </div>
          <div className="bar" style={{ width: '80px', alignSelf: 'center' }}>
            <i style={{ width: (v.d / v.n * 100) + '%' }} />
          </div>
        </div>
      ))}
      <div style={{ height: '16px' }} />
    </>
  )
}
