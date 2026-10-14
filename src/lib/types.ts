export interface Item {
  id: number
  t: string
  p: number
  due: number
  g: string
  done: boolean
  at?: number
}

export interface State {
  sampleVersion?: number
  items: Item[]
  sort: 'manual' | 'due' | 'pri' | 'group'
  nid: number
}
