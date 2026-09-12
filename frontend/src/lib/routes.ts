/**
 * UUID-first path builders.
 *
 * 身份永远是 UUID（`:id` / `:docId`）。**路径里不写 slug**——UUID 本身就是那篇文章：
 * 再挂一段标题，同一条内容就有了多个 URL，还得处理转义、保留字（`folder`）、
 * 改名后的陈旧链接、以及"该跳回规范链接"的重定向。这些成本全是 slug 带来的，
 * 而它一点信息量都不增加。
 *
 * Path table:
 *   /w/:id                      work
 *   /w/:id/folder               docs folder
 *   /w/:id/folder/:docId        doc by UUID
 *
 * （App 里仍保留一条 `/w/:id/:slug` 的容忍路由：不生成、但旧链接点进来照样能开。）
 */

/** `/w/:id` */
export function workPath(id: string): string {
  return `/w/${id}`
}

/** `/w/:id/folder` — identity is the work UUID. */
export function folderPath(workId: string): string {
  return `/w/${workId}/folder`
}

/** `/w/:id/folder/:docId` — both segments are UUIDs. */
export function docPath(workId: string, docId: string): string {
  return `/w/${workId}/folder/${docId}`
}

/** Sentinel work-id used when only a doc UUID is known (e.g. search hits). */
export const UNKNOWN_WORK_ID = '-'
