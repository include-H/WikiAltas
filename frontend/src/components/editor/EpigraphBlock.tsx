import { useEffect, useRef, useState } from 'react'
import { createReactBlockSpec } from '@blocknote/react'
import { parseEpigraph } from '../../lib/epigraph'

// 题记：本产品唯一允许"文艺"的组件（VISUAL_SPEC §3）。
// 渲染形态对齐 GameManager 原始实现：无底色、无边框，一对对角装饰引号（CSS
// ::before/::after）+ 居中排布 + 中文大字/英文小字 + 署名右对齐。
// 默认编辑态也按渲染形态显示，点击进入源码编辑。

function EpigraphView({ text }: { text: string }) {
  const { lines, author } = parseEpigraph(text)
  return (
    <div className="doc-epigraph-content">
      <blockquote className="doc-epigraph-body">
        {lines.map((line, i) =>
          line.type === 'latin' ? (
            <p className="doc-epigraph-line doc-epigraph-line--latin" key={`latin-${i}`}>
              {line.text}
            </p>
          ) : (
            <p
              className="doc-epigraph-line doc-epigraph-line--display"
              key={`display-${i}`}
              style={{ fontSize: line.style.fontSize, letterSpacing: line.style.letterSpacing }}
            >
              {line.segments.map((seg, j) => (
                <span className="doc-epigraph-segment" key={`seg-${j}`}>
                  <span className="doc-epigraph-segment-text">{seg.text}</span>
                  {seg.punctuation && (
                    <span className="doc-epigraph-punctuation">{seg.punctuation}</span>
                  )}
                </span>
              ))}
            </p>
          ),
        )}
      </blockquote>
      {author && <figcaption className="doc-epigraph-author">—— {author}</figcaption>}
    </div>
  )
}

function EpigraphCard({
  text,
  editable,
  onCommit,
}: {
  text: string
  editable: boolean
  onCommit: (next: string) => void
}) {
  const [editing, setEditing] = useState(false)
  const [draft, setDraft] = useState(text)
  const ref = useRef<HTMLTextAreaElement | null>(null)

  useEffect(() => {
    setDraft(text)
  }, [text])

  useEffect(() => {
    if (editing) ref.current?.focus()
  }, [editing])

  const commit = () => {
    setEditing(false)
    const next = draft.trim()
    if (next !== text.trim()) onCommit(next)
  }

  if (editing && editable) {
    return (
      <figure className="doc-epigraph is-editing">
        <textarea
          ref={ref}
          className="doc-epigraph-input"
          value={draft}
          rows={Math.max(3, draft.split('\n').length + 1)}
          placeholder={'题记正文，可多行\n最后一行以 —— 开头作为署名'}
          onChange={(e) => setDraft(e.target.value)}
          onBlur={commit}
          onKeyDown={(e) => {
            if (e.key === 'Escape') {
              setDraft(text)
              setEditing(false)
            }
          }}
        />
      </figure>
    )
  }

  return (
    <figure
      className={`doc-epigraph${editable ? ' is-editable' : ''}`}
      role={editable ? 'button' : undefined}
      tabIndex={editable ? 0 : undefined}
      title={editable ? '点击编辑题记' : undefined}
      onClick={() => editable && setEditing(true)}
      onKeyDown={(e) => {
        if (editable && e.key === 'Enter') setEditing(true)
      }}
    >
      <EpigraphView text={text} />
    </figure>
  )
}

export const epigraphBlockSpec = createReactBlockSpec(
  {
    type: 'epigraph',
    propSchema: { text: { default: '' } },
    content: 'none',
  },
  {
    render: ({ block, editor }) => (
      <EpigraphCard
        text={String(block.props.text ?? '')}
        editable={editor.isEditable}
        onCommit={(next) => {
          editor.updateBlock(block, { props: { text: next } } as never)
        }}
      />
    ),
  },
)()
