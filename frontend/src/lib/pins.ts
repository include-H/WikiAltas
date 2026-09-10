// 置顶（首期实现）
//
// 冻结的 works 表没有 pinned 字段，DESIGN_V2 §13.6 要求加字段必须先改冻结文档，
// 因此首期把置顶保存在本地（单用户产品足够），后端字段就绪后可无损迁移。
const KEY = 'wikiatlas.pinned.v1'

export function readPins(): string[] {
  try {
    const raw = localStorage.getItem(KEY)
    if (!raw) return []
    const parsed: unknown = JSON.parse(raw)
    if (!Array.isArray(parsed)) return []
    return parsed.filter((v): v is string => typeof v === 'string')
  } catch {
    return []
  }
}

export function writePins(ids: string[]): void {
  try {
    localStorage.setItem(KEY, JSON.stringify(ids))
  } catch {
    // storage disabled — 置顶降级为会话内状态
  }
}
