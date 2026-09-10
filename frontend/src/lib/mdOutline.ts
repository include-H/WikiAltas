import type { OutlineHeading } from '../types'
import { parseEpigraph } from './epigraph'

/** Parse ## and ### headings from markdown source. */
export function parseOutline(markdown: string): OutlineHeading[] {
  if (!markdown) return []
  const lines = markdown.split(/\r?\n/)
  const headings: OutlineHeading[] = []
  let inFence = false
  let fenceMarker = ''
  let chapter = 0
  let section = 0

  for (const raw of lines) {
    const line = raw.trimEnd()
    if (line.startsWith('```') || line.startsWith('~~~')) {
      if (!inFence) {
        inFence = true
        fenceMarker = line.slice(0, 3)
      } else if (line.startsWith(fenceMarker)) {
        inFence = false
        fenceMarker = ''
      }
      continue
    }
    if (inFence) continue
    if (line.startsWith(':::')) continue

    const m = /^(#{2,3})\s+(.+)$/.exec(line)
    if (!m) continue
    const level = m[1].length as 2 | 3
    const rawTitle = m[2].replace(/[#*`_]+/g, '').trim()
    if (!rawTitle) continue

    // 正文里已经写了编号（"1. 作品概览" / "2.1 基本资料"）就沿用，
    // 否则按层级自动编号，保证大纲永远有 1. / 2.1 这样的前缀。
    const numbered = /^(\d+(?:\.\d+)*)[.、]?\s+(.*)$/.exec(rawTitle)
    let number: string
    let text: string
    if (numbered) {
      number = numbered[1].includes('.') ? numbered[1] : `${numbered[1]}.`
      text = numbered[2].trim()
      const parts = numbered[1].split('.')
      if (parts.length > 1) {
        chapter = Number(parts[0]) || chapter
        section = Number(parts[1]) || section
      } else {
        chapter = Number(parts[0]) || chapter + 1
        section = 0
      }
    } else if (level === 2) {
      chapter += 1
      section = 0
      number = `${chapter}.`
      text = rawTitle
    } else {
      section += 1
      number = chapter > 0 ? `${chapter}.${section}` : `${section}`
      text = rawTitle
    }

    headings.push({
      // id 必须与正文渲染时注入的锚点一致，所以用带编号的原始标题
      id: slugifyHeading(rawTitle, headings.length),
      level,
      number,
      text,
    })
  }
  return headings
}

export function slugifyHeading(text: string, index: number): string {
  const base = text
    .toLowerCase()
    .replace(/[^\p{L}\p{N}\s-]/gu, '')
    .trim()
    .replace(/\s+/g, '-')
  return base || `h-${index}`
}

/** Rewrite markdown so ##/### get id attributes for anchor scroll. */
export function injectHeadingIds(markdown: string): string {
  const lines = markdown.split(/\r?\n/)
  const seen = new Set<string>()
  let inFence = false
  let fenceMarker = ''
  let counter = 0

  const out = lines.map((raw) => {
    const line = raw.trimEnd()
    if (line.startsWith('```') || line.startsWith('~~~')) {
      if (!inFence) {
        inFence = true
        fenceMarker = line.slice(0, 3)
      } else if (line.startsWith(fenceMarker)) {
        inFence = false
        fenceMarker = ''
      }
      return raw
    }
    if (inFence) return raw

    const m = /^(#{2,3})\s+(.+)$/.exec(line)
    if (!m) return raw
    const text = m[2].replace(/[#*`_]+/g, '').trim()
    let id = slugifyHeading(text, counter)
    counter += 1
    if (seen.has(id)) {
      id = `${id}-${counter}`
    }
    seen.add(id)
    return `${m[1]} ${m[2]} {#${id}}`
  })
  return out.join('\n')
}

/**
 * 把 :::epigraph … ::: 围栏转成「书页留白」题记 HTML。
 *
 * 渲染形态对齐 /root/Wiki/src/styles.css 的 .doc-epigraph：
 * 居中、衬线、上下细分隔线、左上角引号、右下署名。
 * 规则：围栏内最后一个非空行以 —— / — 开头时视为署名（cite）。
 */
export function transformEpigraph(markdown: string): string {
  const lines = markdown.split(/\r?\n/)
  const out: string[] = []
  let collecting = false
  let buffer: string[] = []
  let inFence = false
  let fenceMarker = ''

  const flush = () => {
    const { lines, author } = parseEpigraph(buffer.join('\n'))
    if (lines.length === 0) {
      buffer = []
      return
    }
    const bodyHtml = lines
      .map((line) => {
        if (line.type === 'latin') {
          return `<p class="doc-epigraph-line doc-epigraph-line--latin">${escapeHtml(line.text)}</p>`
        }
        const segments = line.segments
          .map(
            (seg) =>
              `<span class="doc-epigraph-segment">` +
              `<span class="doc-epigraph-segment-text">${escapeHtml(seg.text)}</span>` +
              (seg.punctuation
                ? `<span class="doc-epigraph-punctuation">${escapeHtml(seg.punctuation)}</span>`
                : '') +
              `</span>`,
          )
          .join('')
        return (
          `<p class="doc-epigraph-line doc-epigraph-line--display" style="font-size:${line.style.fontSize};letter-spacing:${line.style.letterSpacing}">` +
          `${segments}</p>`
        )
      })
      .join('')
    const authorHtml = author
      ? `<figcaption class="doc-epigraph-author">—— ${escapeHtml(author)}</figcaption>`
      : ''
    out.push(
      `<figure class="doc-epigraph"><div class="doc-epigraph-content">` +
        `<blockquote class="doc-epigraph-body">${bodyHtml}</blockquote>${authorHtml}` +
        `</div></figure>`,
    )
    buffer = []
  }

  for (const line of lines) {
    if (line.startsWith('```') || line.startsWith('~~~')) {
      if (!inFence) {
        inFence = true
        fenceMarker = line.slice(0, 3)
      } else if (line.startsWith(fenceMarker)) {
        inFence = false
        fenceMarker = ''
      }
      out.push(line)
      continue
    }
    if (inFence) {
      out.push(line)
      continue
    }
    const t = line.trim()
    if (t === ':::epigraph') {
      collecting = true
      buffer = []
      continue
    }
    if (collecting && (t === ':::' || t === ':::epigraph')) {
      flush()
      collecting = false
      continue
    }
    if (collecting) {
      buffer.push(line)
      continue
    }
    out.push(line)
  }
  if (collecting) flush()
  return out.join('\n')
}

function escapeHtml(s: string): string {
  return s
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
}
