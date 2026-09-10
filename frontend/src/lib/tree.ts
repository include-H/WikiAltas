import type { WorkKind, WorkStatus, WorkSummary } from '../types'

export interface TreeNodeData {
  key: string
  label: string
  value: string
  children?: TreeNodeData[]
}

// 「系列 = 资料夹」：系列节点在建出来的时候就带着自己的资料夹，
// 树里用一行合成节点呈现（不是 works 表里的真实节点）。
export const FOLDER_KEY_PREFIX = 'folder:'

export function folderKey(workId: string): string {
  return `${FOLDER_KEY_PREFIX}${workId}`
}

export function isFolderKey(key: string): boolean {
  return key.startsWith(FOLDER_KEY_PREFIX)
}

export function folderKeyWorkId(key: string): string {
  return key.slice(FOLDER_KEY_PREFIX.length)
}

/** 给每个系列节点挂一行「资料夹」子项。 */
export function attachFolderRows(nodes: WorkSummary[]): TreeNodeData[] {
  const tree = buildTreeData(nodes)
  const kindById = new Map(nodes.map((n) => [n.id, n.kind]))
  const walk = (rows: TreeNodeData[]) => {
    for (const row of rows) {
      const realChildren = (row.children ?? []).filter((c) => !isFolderKey(c.key))
      if (kindById.get(row.key) === 'series') {
        row.children = [
          ...realChildren,
          { key: folderKey(row.key), value: folderKey(row.key), label: '资料夹' },
        ]
      } else {
        row.children = realChildren.length ? realChildren : undefined
      }
      if (row.children) walk(realChildren)
    }
  }
  walk(tree)
  return tree
}

export const STATUS_META: Record<
  WorkStatus,
  { color: 'grey' | 'orange' | 'green'; text: string; label: string }
> = {
  stub: { color: 'grey', text: 'stub', label: '待建档' },
  draft: { color: 'orange', text: 'draft', label: '草稿' },
  ready: { color: 'green', text: 'ready', label: '就绪' },
}

export const KIND_META: Record<WorkKind, { text: string; label: string }> = {
  universe: { text: 'universe', label: '宇宙' },
  series: { text: 'series', label: '系列' },
  work: { text: 'work', label: '单作' },
}

/** Flat node list → nested tree data, ordered by sortOrder then title. */
export function buildTreeData(nodes: WorkSummary[]): TreeNodeData[] {
  const ordered = [...nodes].sort(
    (a, b) => a.sortOrder - b.sortOrder || a.title.localeCompare(b.title),
  )
  const byId = new Map<string, TreeNodeData>()
  for (const n of ordered) {
    byId.set(n.id, { key: n.id, value: n.id, label: n.title, children: [] })
  }
  const roots: TreeNodeData[] = []
  for (const n of ordered) {
    const node = byId.get(n.id)
    if (!node) continue
    const parent = n.parentId ? byId.get(n.parentId) : undefined
    if (parent?.children) parent.children.push(node)
    else roots.push(node)
  }
  const prune = (arr: TreeNodeData[]) => {
    for (const n of arr) {
      if (!n.children?.length) delete n.children
      else prune(n.children)
    }
  }
  prune(roots)
  return roots
}

/** Ids of the node itself plus every descendant — used to block illegal moves. */
export function subTreeIds(nodes: WorkSummary[], rootId: string): Set<string> {
  const childrenOf = new Map<string, string[]>()
  for (const n of nodes) {
    if (!n.parentId) continue
    const list = childrenOf.get(n.parentId) ?? []
    list.push(n.id)
    childrenOf.set(n.parentId, list)
  }
  const out = new Set<string>([rootId])
  const queue = [rootId]
  while (queue.length) {
    const cur = queue.shift() as string
    for (const child of childrenOf.get(cur) ?? []) {
      if (out.has(child)) continue
      out.add(child)
      queue.push(child)
    }
  }
  return out
}

/** Breadcrumb path from root to the node (inclusive). */
export function ancestorPath(nodes: WorkSummary[], id: string): WorkSummary[] {
  const byId = new Map(nodes.map((n) => [n.id, n]))
  const out: WorkSummary[] = []
  let cur = byId.get(id)
  let guard = 0
  while (cur && guard < 32) {
    out.unshift(cur)
    cur = cur.parentId ? byId.get(cur.parentId) : undefined
    guard += 1
  }
  return out
}

/** Feishu-style relative time: 刚刚 / x 分钟前 / x 小时前 / x 天前 / M月D日. */
export function relativeTime(iso?: string | null): string {
  if (!iso) return ''
  const t = new Date(iso).getTime()
  if (Number.isNaN(t)) return ''
  const diff = Date.now() - t
  if (diff < 60_000) return '刚刚'
  if (diff < 3_600_000) return `${Math.floor(diff / 60_000)} 分钟前`
  if (diff < 86_400_000) return `${Math.floor(diff / 3_600_000)} 小时前`
  if (diff < 7 * 86_400_000) return `${Math.floor(diff / 86_400_000)} 天前`
  const d = new Date(t)
  return `${d.getMonth() + 1}月${d.getDate()}日`
}

/** "9月1日 10:19" — absolute timestamp used in the document header. */
export function absoluteTime(iso?: string | null): string {
  if (!iso) return ''
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return ''
  const hh = String(d.getHours()).padStart(2, '0')
  const mm = String(d.getMinutes()).padStart(2, '0')
  return `${d.getMonth() + 1}月${d.getDate()}日 ${hh}:${mm}`
}
