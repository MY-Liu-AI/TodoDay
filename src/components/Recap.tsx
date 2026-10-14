/* 热力格底下的两块小结：一行本周读数，一组分组进度。
   这两块都从真实清单来，回答的是清单本身答不上来的两个问题：
   这周做了多少、哪一类欠得最多。 */
import { DAY, TODAY, midnight } from '../lib/todo.js'
import { best, counts, recent } from '../lib/heat.js'
import type { Item } from '../lib/types'

export default function Recap({ items }: { items: Item[] }) {
  const c = counts(items)
  const wk = recent(c, 7)
  const last = recent(c, 28)
  const groups = Object.entries(
    items.reduce((m: Record<string, { n: number; d: number }>, i) => {
      const g = m[i.g] || (m[i.g] = { n: 0, d: 0 })
      g.n++; if (i.done) g.d++
      return m
    }, {})
  ).sort((a, b) => b[1].n - a[1].n)

  const open = items.filter(i => !i.done)
  const soon = open.filter(i => i.due && midnight(i.due) <= TODAY + DAY * 7).length

  return (
    <>
      <div className="grouphead"><span>这一周</span></div>
      <div className="wkstat">
        <div><b>{wk}</b><i>本周完成</i></div>
        <div><b>{(last / 28).toFixed(1)}</b><i>日均</i></div>
        <div><b>{best(c)}</b><i>最长连续</i></div>
      </div>

      <div className="grouphead"><span>按分组</span><b>{soon + ' 件待办在一周内'}</b></div>
      <div className="glist">
        {groups.map(([g, v]) => (
          <div className="grow" key={g}>
            <span className="nm">{g}</span>
            <span className="bar"><i style={{ width: (v.d / v.n * 100) + '%' }} /></span>
            <span className="v">{v.d + '/' + v.n}</span>
          </div>
        ))}
      </div>
    </>
  )
}
