/**
 * UUID-first path builders.
 *
 * Identity is always the UUID (`:id` / `:docId`). The slug segment is
 * cosmetic only (GitHub/Notion style) and must never be used for lookup.
 *
 * Path table:
 *   /w/:id                      work
 *   /w/:id/:slug                work (cosmetic slug; ignored on resolve)
 *   /w/:id/folder               docs folder
 *   /w/:id/folder/:docId        doc by UUID
 */

/** Path tokens that must never be treated as a cosmetic slug. */
const RESERVED = new Set(['folder'])

/**
 * Encode a display slug for use as a path segment.
 * Returns null when the slug is empty or reserved — callers then omit it.
 */
export function encodeSlugPath(slug?: string | null): string | null {
  if (!slug) return null
  if (RESERVED.has(slug)) return null
  // Reject values that would break path structure even after encode
  if (slug === '.' || slug === '..') return null
  return encodeURIComponent(slug)
}

/** `/w/:id` or `/w/:id/:slug` when a safe cosmetic slug is available. */
export function workPath(id: string, slug?: string | null): string {
  const s = encodeSlugPath(slug)
  return s ? `/w/${id}/${s}` : `/w/${id}`
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
