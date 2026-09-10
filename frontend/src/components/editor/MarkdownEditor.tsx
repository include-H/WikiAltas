import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import {
  Avatar,
  Banner,
  Button,
  Input,
  Toast,
  Typography,
} from '@douyinfe/semi-ui'
import { IconEdit, IconSave } from '@douyinfe/semi-icons'
import { IconAIFilledLevel1 } from '@douyinfe/semi-icons'
import type { PutContentBody, PutContentResult } from '../../types'
import { parseOutline } from '../../lib/mdOutline'
import { ensureLeadingH1, stripLeadingH1 } from '../../lib/docMarkdown'
import { absoluteTime } from '../../lib/tree'
import MarkdownPreview from './MarkdownPreview'
import BlockMarkdownEditor from './BlockMarkdownEditor'

const { Text, Title } = Typography

export type EditorMode = 'edit' | 'read'

interface Props {
  title: string
  updatedAt?: string
  author?: string
  contentMd: string
  contentVer: number
  saving?: boolean
  aiWriting?: boolean
  /** 受控的编辑/阅读态（顶栏眼睛图标与世界共用） */
  mode?: EditorMode
  /** 信息栏右侧：AI 共建 + 更多菜单 */
  actions?: React.ReactNode
  /** 空文档时正文里的行内 AI 入口（飞书「AI 帮我写」的位置） */
  onAiWrite?: () => void
  onSave: (body: PutContentBody) => Promise<PutContentResult>
  onTitleChange?: (title: string) => void
  /** Debounced draft for live outline extraction in parent panes. */
  onDraftChange?: (markdown: string) => void
}

export default function MarkdownEditor({
  title,
  updatedAt,
  author = '我',
  contentMd,
  contentVer,
  saving,
  aiWriting,
  mode: modeProp,
  actions,
  onAiWrite,
  onSave,
  onTitleChange,
  onDraftChange,
}: Props) {
  // 默认编辑态 + 所见即所得（对齐 VISUAL_SPEC §1.4）
  const [modeState] = useState<EditorMode>('edit')
  const mode = modeProp ?? modeState
  const safeContent = contentMd ?? ''
  const [draft, setDraft] = useState(() => stripLeadingH1(safeContent))
  const [dirty, setDirty] = useState(false)
  const [localSaving, setLocalSaving] = useState(false)
  const [editTitle, setEditTitle] = useState(title)
  const lastExternal = useRef(stripLeadingH1(safeContent))

  // 外部内容变化（AI 提交 / 切换作品）时同步，正在编辑的内容不动
  useEffect(() => {
    const stripped = stripLeadingH1(safeContent)
    if (!dirty && lastExternal.current !== stripped) {
      setDraft(stripped)
      lastExternal.current = stripped
    }
    if (!dirty) setEditTitle(title)
  }, [safeContent, title, dirty])

  useEffect(() => {
    if (aiWriting) lastExternal.current = stripLeadingH1(safeContent)
  }, [aiWriting, safeContent])

  // 大纲跟随实时编辑
  useEffect(() => {
    if (!onDraftChange) return
    const t = window.setTimeout(() => onDraftChange(ensureLeadingH1(draft, editTitle)), 280)
    return () => window.clearTimeout(t)
  }, [draft, editTitle, onDraftChange])

  const onBodyChange = useCallback((v: string) => {
    setDraft(v)
    setDirty(true)
  }, [])

  const save = useCallback(async () => {
    setLocalSaving(true)
    try {
      const content = ensureLeadingH1(draft, editTitle)
      await onSave({
        contentMd: content,
        author: 'human',
        expectedVersion: contentVer,
        summary: dirty ? '手动编辑' : undefined,
      })
      lastExternal.current = draft
      setDirty(false)
      Toast.success('已保存')
      if (onTitleChange && editTitle !== title) onTitleChange(editTitle)
    } catch (e) {
      Toast.error(e instanceof Error ? e.message : '保存失败')
    } finally {
      setLocalSaving(false)
    }
  }, [draft, editTitle, contentVer, dirty, onSave, onTitleChange, title])

  // Ctrl/Cmd+S
  useEffect(() => {
    const h = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key === 's') {
        e.preventDefault()
        void save()
      }
    }
    window.addEventListener('keydown', h)
    return () => window.removeEventListener('keydown', h)
  }, [save])

  const headings = useMemo(() => parseOutline(draft), [draft])

  // 编辑态与修订态本质是同一个编辑器（差别在馆员的作业方式，不在界面）；
  // 只读态才切成渲染视图。产品里没有 Markdown 源码模式。
  const showBlock = mode === 'edit'
  const showRead = mode === 'read'

  return (
    <div className="md-editor">
      <div className="doc-header">
        {mode === 'edit' ? (
          <Input
            borderless
            className="doc-title-input"
            value={editTitle}
            inputStyle={{ fontSize: 30, fontWeight: 600, lineHeight: 1.3, padding: '2px 0' }}
            onChange={setEditTitle}
            placeholder="请输入标题"
            disabled={!onTitleChange}
          />
        ) : (
          <Title heading={2} className="doc-title-read">
            {editTitle || '无标题'}
          </Title>
        )}
        <div className="doc-meta">
          <Avatar size="extra-small" color="light-blue">
            {author.slice(0, 1)}
          </Avatar>
          <span className="doc-meta-author">{author}</span>
          {updatedAt && <span className="doc-meta-sep">|</span>}
          {updatedAt && <span className="doc-meta-time">{absoluteTime(updatedAt)} 修改</span>}
          <div className="doc-meta-spacer" />
          {/* 同步状态很轻：干净时只有一行小字，有改动才出现保存按钮 */}
          <Text type="tertiary" size="small" className="editor-sync-hint">
            v{contentVer}
            {dirty ? ' · 未保存' : saving || localSaving ? ' · 保存中…' : ' · 已同步'}
          </Text>
          {(dirty || localSaving || saving) && (
            <Button
              size="small"
              theme="solid"
              type="primary"
              icon={<IconSave />}
              loading={localSaving || saving}
              onClick={() => void save()}
            >
              保存
            </Button>
          )}
          {actions}
        </div>
      </div>

      {aiWriting && (
        <Banner
          type="warning"
          icon={<IconEdit />}
          description="馆员正在写入…完成后正文会自动刷新。"
          closeIcon={null}
          className="doc-writing-banner"
        />
      )}

      <div className={`editor-body mode-${mode}`}>
        {showBlock && (
          <BlockMarkdownEditor
            value={draft}
            onChange={onBodyChange}
            readOnly={false}
            emptyHint={
              draft.trim() === '' && onAiWrite ? (
                <>
                  <span>按 “/” 插入内容，或让</span>
                  <button type="button" className="ai-inline-pill" onClick={onAiWrite}>
                    <IconAIFilledLevel1 size="small" />
                    AI 帮我写
                  </button>
                </>
              ) : null
            }
          />
        )}
        {showRead && (
          <div className="editor-pane preview-pane" id="content-scroll">
            {draft.trim() ? (
              <MarkdownPreview markdown={draft} />
            ) : (
              <div className="preview-empty">
                正文为空。可在编辑态编写，或唤起右下角馆员建档。
                <div style={{ marginTop: 8, opacity: 0.6, fontSize: 12 }}>
                  {headings.length > 0 ? `${headings.length} 个标题` : ''}
                </div>
              </div>
            )}
          </div>
        )}
      </div>
    </div>
  )
}
