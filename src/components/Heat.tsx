/* 完成热力格：列是周，行是周一到周日，和上面那本月历同一个起点。
   一年太长，手账只回看 18 周 —— 340px 的边栏里，再多一列格子就该眯眼看了。 */
import { useState } from 'react'
import { TODAY } from '../lib/todo.js'
import { DAYS, WEEKS, counts, level, streak } from '../lib/heat.js'
import type { Item } from '../lib/types'

const WDL = ['一', '', '三', '', '五', '', '']
const fmt = (ts: number) =>
  new Date(ts).toLocaleDateString('zh-CN', { month: 'long', day: 'numeric' })

export default function Heat({ items }: { items: Item[] }) {
  /* 悬停哪一格，底下那行就报哪一天。系统 tooltip 要停留一秒才出来，等于没人
     知道这些格子是可以问的。 */
  const [hov, setHov] = useState(0)
  const c = counts(items) as Record<number, number>
  const total = DAYS.reduce((s: number, ts: number) => s + (ts <= TODAY ? (c[ts] || 0) : 0), 0)
  const run = streak(c)

  return (
    <>
      <div className="grouphead"><span>完成热力</span><b>{`近 ${WEEKS} 周 ${total} 件`}</b></div>

      <div className="heat">
        <div className="heathint">一格一天，颜色越深，那天勾掉的事越多。</div>
        <div className="heatgrid" onMouseLeave={() => setHov(0)}>
          {WDL.map((w, d) => w ? (
            <span className="hwd" key={d} style={{ gridColumn: 1, gridRow: d + 2 }}>{w}</span>
          ) : null)}

          {Array.from({ length: WEEKS }, (_, w) => {
            /* 月份只在换月的那一列标一次。最左那列不标：它多半是半截月份，
               标出来只会和第二列的标签挤在一起。 */
            const m = new Date(DAYS[w * 7]).getMonth()
            if (!w || m === new Date(DAYS[w * 7 - 7]).getMonth()) return null
            return <span className="hmon" key={'m' + w}
                         style={{ gridColumn: w + 2, gridRow: 1 }}>{m + 1 + ' 月'}</span>
          })}

          {DAYS.map((ts: number, k: number) => {
            const fut = ts > TODAY
            const n = c[ts] || 0
            return (
              <button type="button" key={ts} disabled={fut} aria-label={fmt(ts) + ' · ' + n} className={'hc l' + (fut ? 'x' : level(n)) + (ts === hov ? ' on' : '')}
                 style={{ gridColumn: Math.floor(k / 7) + 2, gridRow: k % 7 + 2 }}
                 onFocus={() => setHov(ts)} onClick={() => setHov(ts)} onMouseEnter={() => !fut && setHov(ts)} />
            )
          })}
        </div>

        <div className="heatfoot">
          <span>{hov ? `${fmt(hov)} · 完成 ${c[hov] || 0} 件`
            : run ? `连续 ${run} 天有进展` : '今天还没勾掉任何一件'}</span>
          <span className="hleg">少{[0, 1, 2, 3, 4].map(l =>
            <i className={'hc l' + l} key={l} />)}多</span>
        </div>
      </div>
    </>
  )
}
