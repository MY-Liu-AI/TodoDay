/* 一天一格，格子底下是那天的负载。点一格就把列表收到那一天，再点一次放开 ——
   日历在这里是筛选器，不是另一个视图，所以它和下面那张列表永远是同一份数据。 */
import Tool from '../lib/tool.js'
import { DAY, TODAY, WD, loadOn, midnight } from '../lib/todo.js'
import type { Item } from '../lib/types'

const PREV = <svg viewBox="0 0 24 24"><path d="M15 5l-7 7 7 7" /></svg>
const NEXT = <svg viewBox="0 0 24 24"><path d="M9 5l7 7-7 7" /></svg>

export default function Calendar({ items, month, daySel, onMonth, onPick }: {
  items: Item[]
  month: Date
  daySel: number
  onMonth: (d: Date) => void
  onPick: (ts: number) => void
}) {
  /* 周一开头。getDay() 里周日是 0，直接拿来当偏移会把整个月错开一格。 */
  const first = new Date(month)
  const lead = (first.getDay() + 6) % 7
  const start = midnight(+first - lead * DAY)

  return (
    <div className="cal">
      <div className="calhead">
        <div className="m" id="calm">{month.getFullYear() + ' 年 ' + (month.getMonth() + 1) + ' 月'}</div>
        <button className="miniib" id="calprev" title="上个月" onClick={() => {
          const d = new Date(month); d.setMonth(d.getMonth() - 1); onMonth(d)
        }}>{PREV}</button>
        <button className="miniib" id="calnext" title="下个月" onClick={() => {
          const d = new Date(month); d.setMonth(d.getMonth() + 1); onMonth(d)
        }}>{NEXT}</button>
        <button className="chip" id="caltoday" onClick={() => {
          const d = new Date(TODAY); d.setDate(1); onMonth(d); onPick(TODAY)
        }}>今天</button>
      </div>

      <div className="calgrid" id="calwd">
        {WD.map((w: string, n: number) => <div className="wd" key={n}>{w}</div>)}
      </div>

      <div className="calgrid" id="calgrid">
        {Array.from({ length: 42 }, (_, k) => {
          const ts = midnight(start + k * DAY)
          const d = new Date(ts)
          const load = loadOn(items, ts)
          const dots: string[] = []
          const add = (cls: string, n: number) => {
            for (let i = 0; i < Math.min(n, 3); i++) dots.push(cls)
          }
          add('late', load.late); add('', load.open); add('did', load.did)
          return (
            <button key={k} type="button"
              className={'cell'
                + (d.getMonth() !== month.getMonth() ? ' out' : '')
                + (ts === TODAY ? ' today' : '')
                + (ts === daySel ? ' on' : '')}
              aria-current={ts === daySel ? 'true' : undefined}
              aria-label={d.toLocaleDateString('zh-CN', { month: 'long', day: 'numeric' })}
              onClick={() => {
                onPick(daySel === ts ? 0 : ts)
                if (daySel !== ts) { const m = new Date(ts); m.setDate(1); onMonth(m) }
                Tool.sound.tick()
              }}>
              <span>{d.getDate()}</span>
              <span className="dots">{dots.map((c, i) => <i key={i} className={c || undefined} />)}</span>
            </button>
          )
        })}
      </div>
    </div>
  )
}
