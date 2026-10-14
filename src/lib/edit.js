/* 编辑面板走的是 Tool.sheet —— 它自己造 DOM、自己管进出场动画，是一个独立模态，
   留在 React 渲染树外面反而更稳。 */
import Kit from './kit.js';
import Tool from './tool.js';
import { midnight } from './todo.js';

const T = Tool, K = Kit;

export function editItem(i, { onSave, onDelete }){
  const b = T.el('div');
  const f1 = T.el('div', { class:'field' }, T.el('label', { text:'内容' }));
  const inp = T.el('input', { type:'text', value:i.t }); f1.appendChild(inp);
  const f2 = T.el('div', { class:'field' }, T.el('label', { text:'日期' }));
  const dt = T.el('input', { type:'date',
    value: i.due ? new Date(i.due - new Date().getTimezoneOffset()*6e4).toISOString().slice(0,10) : '' });
  f2.appendChild(dt);
  const f3 = T.el('div', { class:'field' }, T.el('label', { text:'分组' }));
  const gp = T.el('input', { type:'text', value:i.g }); f3.appendChild(gp);
  const f4 = T.el('div', { class:'field' }, T.el('label', { text:'优先级' }));
  const pr = T.el('div', { class:'prirow' });
  let pri = i.p;
  [[1,'高'],[2,'中'],[3,'低']].forEach(([v,l]) => {
    const c = T.el('button', { class:'chip' + (i.p===v?' on':''), type:'button', text:l });
    c.onclick = () => { pri = v; [...pr.children].forEach(x => x.classList.toggle('on', x===c)) };
    pr.appendChild(c);
  });
  f4.appendChild(pr);
  const del = T.el('button', { class:'btn', text:'删除这一条',
    style:'width:100%;color:var(--neg);border-color:var(--neg)' });
  del.onclick = () => { onDelete(i); sh.close(); K.toast('已删除') };
  b.append(f1, f2, f3, f4, del);
  const sh = T.sheet({ title:'编辑', body:b, actions:[
    { label:'取消' },
    { label:'保存', primary:true, onClick(){
        onSave({ ...i,
          t: inp.value.trim() || i.t,
          due: dt.value ? midnight(new Date(dt.value + 'T00:00:00')) : 0,
          g: gp.value.trim(),
          p: pri });
        K.toast('已保存');
      } }
  ]});
}
