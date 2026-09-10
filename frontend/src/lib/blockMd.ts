// 存储用 Markdown ⇄ BlockNote 文档的转换。
//
// BlockNote 的 markdown 解析器不认识 :::epigraph 这种自定义围栏，所以：
// · 载入：把题记围栏抠出来，换成一行占位段落，解析完再把占位段落
//   替换成自定义 epigraph 块（见 components/editor/EpigraphBlock.tsx）
// · 保存：按块序列化，遇到 epigraph 块写回 :::epigraph … :::

export const EPIGRAPH_TYPE = 'epigraph'
const PLACEHOLDER_PREFIX = '@@WIKIATLAS_EPIGRAPH_'

export interface ParsedMarkdown {
  /** 交给 BlockNote 解析的 markdown（题记被替换成占位段落） */
  markdown: string
  /** 按出现顺序保存的题记原文 */
  epigraphs: string[]
}

function isFence(line: string): boolean {
  return line.startsWith('```') || line.startsWith('~~~')
}

/** 抠出题记围栏，返回可被 BlockNote 解析的 markdown + 题记数组。 */
export function mdToBlockNoteMd(markdown: string): ParsedMarkdown {
  if (!markdown) return { markdown: '', epigraphs: [] }
  const lines = markdown.split(/\r?\n/)
  const out: string[] = []
  const epigraphs: string[] = []
  let collecting = false
  let buffer: string[] = []
  let inFence = false
  let fenceMarker = ''

  const pushEpigraph = () => {
    epigraphs.push(buffer.join('\n').trim())
    out.push(`${PLACEHOLDER_PREFIX}${epigraphs.length - 1}@@`)
    buffer = []
  }

  for (const line of lines) {
    const t = line.trim()
    if (isFence(t)) {
      if (!inFence) {
        inFence = true
        fenceMarker = t.slice(0, 3)
      } else if (t.startsWith(fenceMarker)) {
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
    if (t === ':::epigraph') {
      collecting = true
      buffer = []
      continue
    }
    if (collecting && (t === ':::' || t === ':::epigraph')) {
      pushEpigraph()
      collecting = false
      continue
    }
    if (collecting) {
      buffer.push(line)
      continue
    }
    out.push(line)
  }
  if (collecting) pushEpigraph()
  return { markdown: out.join('\n'), epigraphs }
}

/** 占位段落 → 自定义 epigraph 块；返回可直接 replaceBlocks 的块数组。 */
export function placeholderToEpigraphBlocks<T extends { type?: string; content?: unknown }>(
  blocks: T[],
  epigraphs: string[],
): (T | { type: string; props: { text: string } })[] {
  const re = new RegExp(`^${PLACEHOLDER_PREFIX}(\\d+)@@$`)
  return blocks.map((b) => {
    const text = inlinePlainText(b)
    const m = re.exec(text)
    if (!m) return b
    const value = epigraphs[Number(m[1])] ?? ''
    return { type: EPIGRAPH_TYPE, props: { text: value } }
  })
}

/** 取块里的纯文本（占位段落用）。 */
function inlinePlainText(block: { content?: unknown }): string {
  const content = block.content
  if (typeof content === 'string') return content.trim()
  if (!Array.isArray(content)) return ''
  return content
    .map((item) => {
      const o = item as { text?: unknown }
      return typeof o?.text === 'string' ? o.text : ''
    })
    .join('')
    .trim()
}

interface BlockLike {
  type: string
  props?: Record<string, unknown>
}

/**
 * 按块序列化回存储用 markdown：epigraph 块写回围栏，其余块成组交给
 * BlockNote 自带的 markdown 导出器。
 */
export function blocksToStoredMd(
  blocks: BlockLike[],
  exportRun: (run: BlockLike[]) => string,
): string {
  const chunks: string[] = []
  let run: BlockLike[] = []
  const flush = () => {
    if (!run.length) return
    const md = exportRun(run).trim()
    if (md) chunks.push(md)
    run = []
  }
  for (const b of blocks) {
    if (b.type === EPIGRAPH_TYPE) {
      flush()
      const text = String(b.props?.text ?? '').trim()
      if (text) chunks.push(`:::epigraph\n${text}\n:::`)
      continue
    }
    run.push(b)
  }
  flush()
  return chunks.length ? `${chunks.join('\n\n')}\n` : ''
}
