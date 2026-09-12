import { useCallback, useEffect, useRef, useState } from 'react'
import { BlockNoteSchema, defaultBlockSpecs } from '@blocknote/core'
import { zh } from '@blocknote/core/locales'
import {
  FormattingToolbarController,
  SideMenuController,
  useCreateBlockNote,
  useEditorChange,
  useEditorSelectionChange,
} from '@blocknote/react'
import { BlockNoteView } from '@blocknote/mantine'
import '@blocknote/core/fonts/inter.css'
import '@blocknote/mantine/style.css'
import {
  EPIGRAPH_TYPE,
  blocksToStoredMd,
  mdToBlockNoteMd,
  placeholderToEpigraphBlocks,
} from '../../lib/blockMd'
import { slugifyHeading, uniqueSlug } from '../../lib/mdOutline'
import FeishuToolbar from './FeishuToolbar'
import { epigraphBlockSpec } from './EpigraphBlock'

interface Props {
  /** Stored markdown (content_md contract). */
  value: string
  /** Fires when the user edits; receives serialized markdown. */
  onChange?: (markdown: string) => void
  readOnly?: boolean
  /** Scroll container id for OutlinePane (default content-scroll). */
  scrollId?: string
  /** 空文档时贴着光标显示的提示（飞书「按 / 插入内容，或让 AI 帮我写」） */
  emptyHint?: React.ReactNode
}

function colorSchemeOf(): 'light' | 'dark' {
  return document.body.getAttribute('theme-mode') === 'dark' ? 'dark' : 'light'
}

/** 默认块 + 题记块（BlockNote 不认识 :::epigraph，所以自定义块）。 */
const schema = BlockNoteSchema.create({
  blockSpecs: { ...defaultBlockSpecs, epigraph: epigraphBlockSpec },
})

// 空文档占位交给 MarkdownEditor 的「按 / 插入内容，或让 AI 帮我写」那一行，
// 这里把 BlockNote 自带的占位符清掉，避免两套提示打架。
const dictionary = {
  ...zh,
  placeholders: { ...zh.placeholders, default: '' },
}

/** vscode-editor-data 是 VS Code 系编辑器复制时写的 JSON，带被复制文件的语言。 */
function copiedFromMarkdownEditor(data: DataTransfer): boolean {
  const raw = data.getData('vscode-editor-data')
  if (!raw) return false
  try {
    const mode = (JSON.parse(raw) as { mode?: unknown }).mode
    return typeof mode === 'string' && /^(markdown|md)$/i.test(mode)
  } catch {
    return false
  }
}

const EPIGRAPH_FENCE = ':::epigraph'
const FENCE_CLOSE = ':::'

interface FenceBlock {
  id: string
  type: string
  content?: unknown
}

/**
 * 段落块的文本按行拆开。BlockNote 把硬换行存成文本里的 "\n"（nodeToBlock.ts），
 * 所以粘贴一整段围栏时，四行会落在同一个段落里，这里按行还原。
 * 非段落返回 null（正文里混进列表/代码块时据此放弃，不硬折）。
 */
function paragraphLines(block: FenceBlock): string[] | null {
  if (block.type !== 'paragraph') return null
  const content = block.content
  if (typeof content === 'string') return content.split('\n')
  if (!Array.isArray(content)) return []
  return content
    .map((item) => {
      const t = (item as { text?: unknown })?.text
      return typeof t === 'string' ? t : ''
    })
    .join('')
    .split('\n')
}

interface FenceEditor {
  document: FenceBlock[]
  // 用方法简写：这几处只需要结构对得上，参数用宽类型即可（属性写法会按逆变报错）
  replaceBlocks(ids: string[], blocks: unknown): void
  setTextCursorPosition(id: string, placement?: 'start' | 'end'): void
  insertBlocks(
    blocks: unknown[],
    referenceBlock: string,
    placement?: 'before' | 'after',
  ): { id: string }[]
}

/** 有没有以 `:::epigraph` 起头的段落（每次输入都判一次，尽量便宜）。 */
function hasEpigraphFence(editor: FenceEditor): boolean {
  return editor.document.some((b) => paragraphLines(b)?.[0]?.trim() === EPIGRAPH_FENCE)
}

/**
 * 把编辑器里手打/粘进来的 `:::epigraph … :::` 折成题记块。
 *
 * 载入路径（mdToBlockNoteMd + placeholderToEpigraphBlocks）只在打开文档时跑一次，
 * 打字走的是反向序列化，到不了那里——所以在编辑器里补这一遍扫描。
 * 闭合行还没写出来就不动（等下次 change）；中途遇到非段落块就放弃，不硬折。
 */
function foldEpigraphFences(editor: FenceEditor): boolean {
  const blocks = editor.document
  for (let i = 0; i < blocks.length; i += 1) {
    const head = paragraphLines(blocks[i])
    if (!head || head[0]?.trim() !== EPIGRAPH_FENCE) continue

    const body: string[] = []
    let tail: string[] = []
    let endBlock = -1
    let pending = head.slice(1)
    for (let j = i; j < blocks.length; j += 1) {
      if (j > i) {
        const next = paragraphLines(blocks[j])
        // 又开了一个围栏，或混进别的块：交给用户自己收拾
        if (next === null || next[0]?.trim() === EPIGRAPH_FENCE) break
        pending = next
      }
      const close = pending.findIndex((l) => l.trim() === FENCE_CLOSE)
      if (close === -1) {
        body.push(...pending)
        continue
      }
      body.push(...pending.slice(0, close))
      tail = pending.slice(close + 1)
      endBlock = j
      break
    }
    if (endBlock === -1) continue

    const replacement: unknown[] = [
      { type: EPIGRAPH_TYPE, props: { text: body.join('\n').trim() } },
    ]
    // 闭合行后面还留着字：留在题记下面当新段落，别吞掉
    if (tail.join('').trim() !== '') {
      replacement.push({ type: 'paragraph', content: tail.join('\n') })
    }
    editor.replaceBlocks(
      blocks.slice(i, endBlock + 1).map((b) => b.id),
      replacement,
    )
    // 折完整块会停在「节点选中」态，BlockNote 那时会吞掉可打印键（打字没反应）。
    // 把光标交给后面那块；后面没有就补一个空段落让用户接着写。
    try {
      const next = editor.document[i + 1]
      if (next) {
        editor.setTextCursorPosition(next.id, 'start')
      } else {
        const [created] = editor.insertBlocks(
          [{ type: 'paragraph' }],
          editor.document[i].id,
          'after',
        )
        if (created) editor.setTextCursorPosition(created.id, 'start')
      }
    } catch {
      // 光标停哪儿不影响内容，放不下就算了
    }
    return true
  }
  return false
}

/**
 * Inject slug ids into BlockNote headings so OutlinePane can scroll.
 *
 * BlockNote 把 `data-content-type` 挂在 `.bn-block-content` 上（`.bn-block` 只有
 * `data-node-type`），标题元素本身就是 `.bn-inline-content`（h2/h3）。
 * 只处理 h2/h3：与 parseOutline 的层级一致，否则正文里混进的 h1/h4+
 * 会让同名标题的去重序号错位，点大纲滚到错地方。
 */
function syncHeadingIds(root: HTMLElement | null): void {
  if (!root) return
  const heads = root.querySelectorAll<HTMLElement>(
    '.bn-block-content[data-content-type="heading"] .bn-inline-content',
  )
  let i = 0
  const used = new Map<string, number>()
  for (const el of heads) {
    const tag = el.tagName
    if (tag !== 'H2' && tag !== 'H3') continue
    const text = (el.textContent ?? '').trim()
    if (!text) continue
    el.id = uniqueSlug(slugifyHeading(text.replace(/[#*`_]+/g, '').trim(), i), used)
    i += 1
  }
}

export default function BlockMarkdownEditor({
  value,
  onChange,
  readOnly,
  scrollId = 'content-scroll',
  emptyHint,
}: Props) {
  const [colorScheme, setColorScheme] = useState<'light' | 'dark'>(colorSchemeOf)
  const wrapRef = useRef<HTMLDivElement | null>(null)
  const innerRef = useRef<HTMLDivElement | null>(null)
  const [hintPos, setHintPos] = useState<{ top: number; left: number } | null>(null)
  /** Last markdown prop we applied into the editor (null = never loaded). */
  const lastLoaded = useRef<string | null>(null)
  /** True while applying an external document replace (init / AI / source). */
  const suppressChange = useRef(false)

  const editor = useCreateBlockNote({
    dictionary,
    schema,
    // BlockNote 收到 vscode-editor-data 就直奔 handleVSCodePaste，把整段原样包成
    // 一个代码块（跳过它自己的 markdown 判断）。从编辑器里复制 markdown 源码
    // 落进 Wiki 时，那一大坨灰容器就是这么来的——这里按 markdown 解析回块。
    // 光标本来就在代码块里时不拦：那时用户要的就是代码本身。
    pasteHandler: ({ event, editor: ed, defaultPasteHandler }) => {
      const data = event.clipboardData
      const text = data?.getData('text/plain') ?? ''
      const inCodeBlock = ed.prosemirrorState.selection.$from.parent.type.spec.code
      if (!inCodeBlock && text.trim() && data && copiedFromMarkdownEditor(data)) {
        ed.pasteMarkdown(text)
        return true
      }
      return defaultPasteHandler()
    },
  })

  // Sync with app theme (Semi body[theme-mode])
  useEffect(() => {
    const obs = new MutationObserver(() => setColorScheme(colorSchemeOf()))
    obs.observe(document.body, { attributes: true, attributeFilter: ['theme-mode'] })
    return () => obs.disconnect()
  }, [])

  // Load initial content + external updates (AI commit / source-mode edits / remount)
  useEffect(() => {
    if (value === lastLoaded.current) return
    suppressChange.current = true
    lastLoaded.current = value
    const parsed = mdToBlockNoteMd(value)
    const blocks = editor.tryParseMarkdownToBlocks(parsed.markdown)
    // 占位段落 → 自定义题记块
    const withEpigraphs = placeholderToEpigraphBlocks(blocks, parsed.epigraphs)
    editor.replaceBlocks(editor.document, withEpigraphs as never)
    // replaceBlocks 会在同一 tick 触发一次 change 回声，这里放行后立刻恢复。
    // 注意：故意不在 cleanup 里 clearTimeout —— StrictMode 会 effect→cleanup→effect，
    // 第二次进入会因为 value === lastLoaded 提前 return，若第一次的定时器被清掉，
    // suppressChange 会永远为 true，表现为"打字被吞、内容不落库"。
    window.setTimeout(() => {
      suppressChange.current = false
      syncHeadingIds(wrapRef.current)
    }, 0)
  }, [editor, value])

  // Keep heading anchors in sync for the outline pane
  useEffect(() => {
    const root = wrapRef.current
    syncHeadingIds(root)
    const t = window.setTimeout(() => syncHeadingIds(root), 50)
    return () => window.clearTimeout(t)
  }, [editor, value, readOnly])

  /** 待执行的题记围栏折叠（延后到本次 change 落定，避免在 dispatch 里再 dispatch）。 */
  const fenceSweep = useRef<number | null>(null)

  useEditorChange((e) => {
    if (suppressChange.current) return
    const md = blocksToStoredMd(e.document, (run) =>
      e.blocksToMarkdownLossy(run as never),
    )
    if (md === lastLoaded.current) return
    lastLoaded.current = md
    syncHeadingIds(wrapRef.current)
    onChange?.(md)
    // 手打/粘进来的 `:::epigraph … :::` 折成题记块；折叠本身会再触发一次 change，
    // 那一次没有围栏可折，所以不会来回打架。
    if (fenceSweep.current === null && hasEpigraphFence(e)) {
      fenceSweep.current = window.setTimeout(() => {
        fenceSweep.current = null
        if (!suppressChange.current) foldEpigraphFences(e)
      }, 0)
    }
  }, editor)

  useEffect(
    () => () => {
      if (fenceSweep.current !== null) window.clearTimeout(fenceSweep.current)
    },
    [],
  )

  useEffect(() => {
    editor.isEditable = !readOnly
  }, [editor, readOnly])

  /**
   * 空文档提示跟随光标：把提示定位到当前光标所在块的左上角。
   * 坐标是相对 .block-editor-inner 的（提示与块一起随滚动移动，所以无需补偿 scrollTop）。
   */
  const placeHint = useCallback(() => {
    const inner = innerRef.current
    const view = editor._tiptapEditor?.view
    if (!inner || !view) return
    try {
      const head = view.state.selection.head
      const coords = view.coordsAtPos(head)
      const rect = inner.getBoundingClientRect()
      setHintPos({
        top: coords.top - rect.top,
        left: coords.left - rect.left,
      })
    } catch {
      setHintPos(null)
    }
  }, [editor])

  useEditorSelectionChange(() => {
    if (editor.isEditable) placeHint()
  }, editor)

  useEffect(() => {
    if (!emptyHint) return
    const raf = window.requestAnimationFrame(placeHint)
    const onResize = () => placeHint()
    window.addEventListener('resize', onResize)
    return () => {
      window.cancelAnimationFrame(raf)
      window.removeEventListener('resize', onResize)
    }
  }, [emptyHint, placeHint, value])

  const setWrap = useCallback((el: HTMLDivElement | null) => {
    wrapRef.current = el
    if (el) syncHeadingIds(el)
  }, [])

  // 题记是自定义块，点它进编辑态时 ProseMirror 的选区还停在上一个块上，
  // 结果格式化工具条会飘在上一段头顶。编辑题记期间把工具条收起来。
  const [epigraphEditing, setEpigraphEditing] = useState(false)
  useEffect(() => {
    const sync = () =>
      setEpigraphEditing(!!document.activeElement?.closest?.('.doc-epigraph-input'))
    const onFocusOut = () => window.setTimeout(sync, 0)
    document.addEventListener('focusin', sync)
    document.addEventListener('focusout', onFocusOut)
    return () => {
      document.removeEventListener('focusin', sync)
      document.removeEventListener('focusout', onFocusOut)
    }
  }, [])

  return (
    <div className="block-editor-scroll" id={scrollId} ref={setWrap}>
      <div className="block-editor-inner" ref={innerRef}>
        {emptyHint && hintPos && (
          <div
            className="doc-empty-hint"
            style={{ top: hintPos.top, left: hintPos.left }}
          >
            {emptyHint}
          </div>
        )}
        <BlockNoteView
          editor={editor}
          editable={!readOnly}
          theme={colorScheme}
          formattingToolbar={false}
        >
          {!epigraphEditing && (
            <FormattingToolbarController formattingToolbar={FeishuToolbar} />
          )}
          <SideMenuController />
        </BlockNoteView>
      </div>
    </div>
  )
}
