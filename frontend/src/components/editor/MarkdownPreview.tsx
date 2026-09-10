import DOMPurify from 'dompurify'
import { marked } from 'marked'
import { useMemo } from 'react'
import { transformEpigraph } from '../../lib/mdOutline'

marked.setOptions({ gfm: true, breaks: true })

interface Props {
  markdown: string
  className?: string
}

export default function MarkdownPreview({ markdown, className }: Props) {
  const html = useMemo(() => {
    if (!markdown?.trim()) return ''
    // 注意：不要用 injectHeadingIds 往标题里塞 `{#id}` —— marked 不认这个语法，
    // 会把标记当正文渲染出来（访客/阅读态会看到 "1. 章节 {#1-章节1}"）。
    // 这里统一在渲染后按顺序补 id，与 parseOutline 的 slug 规则保持一致。
    const src = transformEpigraph(markdown)
    let rendered = marked.parse(src, { async: false }) as string
    rendered = ensureHeadingIds(rendered, markdown)
    return DOMPurify.sanitize(rendered, {
      ADD_ATTR: ['target', 'id', 'class'],
    })
  }, [markdown])

  return (
    <div
      className={`md-preview ${className ?? ''}`}
      // eslint-disable-next-line react/no-danger
      dangerouslySetInnerHTML={{ __html: html }}
    />
  )
}

/** Ensure every h2/h3 has an id matching outline slug order. */
function ensureHeadingIds(html: string, originalMd: string): string {
  // Collect heading texts from md in order
  const texts: string[] = []
  let inFence = false
  for (const line of originalMd.split(/\r?\n/)) {
    if (line.startsWith('```') || line.startsWith('~~~')) {
      inFence = !inFence
      continue
    }
    if (inFence) continue
    const m = /^#{2,3}\s+(.+)$/.exec(line.trim())
    if (m) texts.push(m[1].replace(/[#*`_]+/g, '').trim())
  }
  if (texts.length === 0) return html

  let i = 0
  return html.replace(/<h([23])([^>]*)>/gi, (match, level, attrs) => {
    const text = texts[i] ?? `h-${i}`
    i += 1
    if (/\sid="/i.test(attrs)) return match
    const id = slug(text, i)
    return `<h${level}${attrs} id="${id}">`
  })
}

function slug(text: string, index: number): string {
  const base = text
    .toLowerCase()
    .replace(/[^\p{L}\p{N}\s-]/gu, '')
    .trim()
    .replace(/\s+/g, '-')
  return base || `h-${index}`
}
