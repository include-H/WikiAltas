import { useMemo, useState } from 'react'
import { AIChatDialogue, Typography } from '@douyinfe/semi-ui'
import {
  IconBriefStroked,
  IconChevronDown,
  IconChevronRight,
  IconCodeStroked,
  IconLoading,
  IconSearchStroked,
} from '@douyinfe/semi-icons'

// 工具卡：`function_call` 输出项的自定义渲染。
//
// Semi 原生的 ToolCallWidget 只有 `<IconWrench/> 名字 原始参数`（它是个空壳），
// 而 Responses 的自定义渲染**原生支持**按内容类型（这里就是 function_call）
// 接管，所以这不是"绕开组件"，是它给的扩展点。
//
// 卡片上的信息全部来自**项本身**：name / arguments 是规范的字段，结果摘要、
// 耗时、diff、产物在 `item.wikiatlas` 下（见后端 responses.go 的 itemExtraKey）。
// 也就是说回放一张卡片不需要去别的事件里检索——项是自足的。

export interface ToolArtifact {
  kind?: string
  lines?: string[]
  truncated?: boolean
  totalLines?: number
}

/** 挂在 function_call 项上的宿主附加信息（规范里没有，所以收在 wikiatlas 键下）。 */
export interface ToolExtra {
  outputSummary?: string
  ok?: boolean
  durationMs?: number
  additions?: number
  deletions?: number
  artifact?: ToolArtifact
  cached?: boolean
}

export interface FunctionCallItem {
  type: 'function_call'
  id?: string
  call_id?: string
  name?: string
  arguments?: string
  status?: string
  wikiatlas?: ToolExtra
}

/** 折叠行摘要的字数上限。见 detail 处的注释：这不是排版偏好，是防撑宽。 */
const ROW_DETAIL_MAX = 42

/** 工具名 → 人话短语（飞书芯片语气）。 */
const TOOL_LABELS: Record<string, string> = {
  read_skill: '已读取 wiki-writing skill',
  search_works: '已检索站内条目',
  read_work: '已读取作品正文',
  read_doc: '已读取资料',
  get_tree: '已读取作品树',
  search_sessions: '已检索跨会话记录',
  read_session: '已读取早先会话',
  search_web: '已联网检索',
  fetch_url: '已抓取网页',
  write_content: '已写入正文',
  patch_section: '已改写章节',
  edit: '已订正文字',
  upsert_work: '已更新作品节点',
  upsert_relation: '已建立作品关系',
  create_doc: '已创建资料',
  attach_library_link: '已挂接媒体库链接',
  sync_library: '已同步媒体库',
  answer: '已作答',
}

/** 图标类别：检索 / 抓取 / 读取 / 写入。 */
function iconOf(name: string) {
  if (name === 'search_web' || name === 'search_works' || name === 'search_sessions') return <IconSearchStroked />
  if (name === 'write_content' || name === 'patch_section' || name === 'edit' || name === 'create_doc') {
    return <IconCodeStroked />
  }
  return <IconBriefStroked />
}

/** 参数 → 一行摘要。参数是模型写的 JSON，可能半截、可能很长，都要能读。 */
function argsSummary(name: string, raw?: string): string {
  const flat = (raw ?? '')
    .replace(/\s+/g, ' ')
    .replace(/"(contentMd|newMarkdown|previewMd|content)"\s*:\s*"(?:[^"\\]|\\.)*"\s*,?/g, '')
    .replace(/[{}]/g, '')
    .replace(/"([^"]*)"\s*:\s*/g, '$1: ')
    .replace(/"([^"]*)"/g, '$1')
    .replace(/\b[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\b/gi, '…')
    .trim()
  if (!flat) return ''
  const cut = (s: string, n: number) => (s.length > n ? `${s.slice(0, n)}…` : s)
  if (name === 'search_web' || name === 'search_works') {
    const q = /q:\s*(.+?)(?:·|参考|count=|$)/.exec(flat)?.[1]?.trim()
    if (q) return `「${cut(q, 20)}」`
  }
  if (name === 'read_work' || name === 'read_doc') {
    const id = /(?:workId|docId|id):\s*(\S+)/.exec(flat)?.[1]
    if (id && id !== '…') return id
  }
  return cut(flat, 40)
}

/** Semi 官方代码卡片（带语言标签 + 复制键）——读取类产物的展示容器。 */
const CodeCard = AIChatDialogue.defaultComponents.code as unknown as React.ComponentType<{
  className?: string
  children?: React.ReactNode
}>

/** 检索类产物：编号来源列表（可点标题）。 */
function SourcesList({ lines }: { lines: string[] }) {
  const items = lines
    .map((line) => {
      const idx = line.lastIndexOf(' — ')
      if (idx > 0) return { title: line.slice(0, idx).trim(), tail: line.slice(idx + 3).trim() }
      return { title: line.trim(), tail: '' }
    })
    .filter((it) => it.title)
    .slice(0, 10)
  if (!items.length) return null
  return (
    <ol className="ai-sources">
      {items.map((it, i) => (
        <li key={i}>
          {it.tail.startsWith('http') ? (
            <a href={it.tail} target="_blank" rel="noreferrer">
              {it.title}
            </a>
          ) : (
            it.title
          )}
        </li>
      ))}
    </ol>
  )
}

export default function ToolCard({ item }: { item: FunctionCallItem }) {
  const name = item.name || 'tool'
  const extra = item.wikiatlas ?? {}
  const running = item.status === 'in_progress'
  const failed = item.status === 'failed' || extra.ok === false
  const [open, setOpen] = useState(false)

  const art = extra.artifact
  const hasDiff = typeof extra.additions === 'number' || typeof extra.deletions === 'number'
  // 展开时给全文：折起那行被截了，别让信息只能靠悬停才看得到
  const fullText = (extra.outputSummary ?? '').replace(/\s+/g, ' ').trim()
  const expandable = !!art?.lines?.length || fullText.length > ROW_DETAIL_MAX

  const detail = useMemo(() => {
    // 结果摘要优先：它说的是"做成了什么"，参数说的是"打算做什么"
    const raw = extra.outputSummary || (failed && !running ? '失败' : argsSummary(name, item.arguments))
    // **在 JS 里截断**，不只是靠 CSS 省略号：这一行是 nowrap，元素的 min-content
    // 就是整串文字宽——完整错误信息（几十个汉字）会把整条消息行顶宽，一路撑到
    // 对话列表出横向滚动条。截断后 min-content 天然受控，跟 CSS 补不补无关。
    const flat = raw.replace(/\s+/g, ' ').trim()
    return flat.length > ROW_DETAIL_MAX ? `${flat.slice(0, ROW_DETAIL_MAX)}…` : flat
  }, [extra.outputSummary, failed, running, name, item.arguments])

  const cls = ['tool-card', running ? 'is-running' : '', failed ? 'is-failed' : ''].filter(Boolean).join(' ')

  return (
    <div className={cls}>
      <button
        type="button"
        className="tool-card-head"
        aria-expanded={expandable ? open : undefined}
        onClick={() => expandable && setOpen((v) => !v)}
      >
        <span className="tool-card-icon" aria-hidden>
          {running ? <IconLoading /> : iconOf(name)}
        </span>
        <span className="tool-card-label">
          {TOOL_LABELS[name] ?? (failed ? `未执行 ${name}` : `已执行 ${name}`)}
        </span>
        {detail && (
          <Typography.Text type="tertiary" size="small" ellipsis={{ showTooltip: true }} className="tool-card-detail">
            {detail}
          </Typography.Text>
        )}
        {hasDiff && (
          <span className="tool-card-diff" aria-label="改动行数">
            {typeof extra.additions === 'number' && <span className="tool-card-diff-add">+{extra.additions}</span>}
            {typeof extra.deletions === 'number' && <span className="tool-card-diff-del">−{extra.deletions}</span>}
          </span>
        )}
        {typeof extra.durationMs === 'number' && extra.durationMs >= 1000 && !running && (
          <Typography.Text type="tertiary" size="small" className="tool-card-time">
            {(extra.durationMs / 1000).toFixed(1)}s
          </Typography.Text>
        )}
        {expandable && <span className="tool-card-caret" aria-hidden>{open ? <IconChevronDown /> : <IconChevronRight />}</span>}
      </button>

      {open && (
        <div className="tool-card-body">
          {/* 折起那行是截断过的，展开给摘要全文（正常卡通常也就一行，不显多余） */}
          {fullText.length > ROW_DETAIL_MAX && (
            <div className={failed ? 'tool-card-error' : 'tool-card-note'}>{fullText}</div>
          )}
          {art?.lines?.length ? (
            art.kind === 'list' ? (
              <SourcesList lines={art.lines} />
            ) : (
              <div className="ai-artifact">
                <CodeCard className="language-md">{art.lines.join('\n')}</CodeCard>
              </div>
            )
          ) : null}
          {art?.truncated && (
            <div className="ai-artifact-note">
              已截断 · 仅显示前 {art.lines?.length ?? 0} 行
              {art.totalLines ? ` · 共 ${art.totalLines} 行` : ''}
            </div>
          )}
        </div>
      )}
    </div>
  )
}
