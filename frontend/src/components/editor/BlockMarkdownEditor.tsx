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
import { blocksToStoredMd, mdToBlockNoteMd, placeholderToEpigraphBlocks } from '../../lib/blockMd'
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

  useEditorChange((e) => {
    if (suppressChange.current) return
    const md = blocksToStoredMd(e.document, (run) =>
      e.blocksToMarkdownLossy(run as never),
    )
    if (md === lastLoaded.current) return
    lastLoaded.current = md
    syncHeadingIds(wrapRef.current)
    onChange?.(md)
  }, editor)

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
          <FormattingToolbarController formattingToolbar={FeishuToolbar} />
          <SideMenuController />
        </BlockNoteView>
      </div>
    </div>
  )
}
