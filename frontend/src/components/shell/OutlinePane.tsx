import { useEffect, useState } from 'react'
import type { OutlineHeading } from '../../types'

interface Props {
  headings: OutlineHeading[]
  /** 当前文档标题，显示在大纲栏顶部（对齐飞书母本） */
  title?: string
  /** container element id that scrolls */
  scrollTargetId?: string
  /** when content changes, re-observe */
  contentKey?: string | number
}

export default function OutlinePane({
  headings,
  title,
  scrollTargetId = 'content-scroll',
  contentKey,
}: Props) {
  const [activeId, setActiveId] = useState<string | null>(null)

  const onClick = (id: string) => {
    const el = document.getElementById(id)
    const scroller = document.getElementById(scrollTargetId)
    if (!el || !scroller) return
    // 用 rect 差值而不是 offsetTop：编辑态里标题的 offsetParent 是
    // .block-editor-inner（position: relative），与滚动容器不同源，
    // offsetTop 相减会算偏；rect 差值对两种态都成立。
    const top =
      scroller.scrollTop +
      el.getBoundingClientRect().top -
      scroller.getBoundingClientRect().top -
      16
    scroller.scrollTo({ top: Math.max(0, top), behavior: 'smooth' })
    setActiveId(id)
  }

  useEffect(() => {
    if (headings.length === 0) {
      setActiveId(null)
      return
    }
    const scroller = document.getElementById(scrollTargetId)
    if (!scroller) return

    const elements = headings
      .map((h) => document.getElementById(h.id))
      .filter((el): el is HTMLElement => !!el)
    if (elements.length === 0) return

    const observer = new IntersectionObserver(
      (entries) => {
        const visible = entries
          .filter((e) => e.isIntersecting)
          .sort((a, b) => a.boundingClientRect.top - b.boundingClientRect.top)
        if (visible[0]?.target.id) {
          setActiveId(visible[0].target.id)
        }
      },
      {
        root: scroller,
        rootMargin: '-10% 0px -70% 0px',
        threshold: [0, 1],
      },
    )
    for (const el of elements) observer.observe(el)
    return () => observer.disconnect()
  }, [headings, scrollTargetId, contentKey])

  if (headings.length === 0) {
    return (
      <div className="outline-pane">
        <div className="pane-header outline-title" title={title}>
          {title || '大纲'}
        </div>
        <div className="outline-empty">无标题</div>
      </div>
    )
  }

  return (
    <div className="outline-pane">
      <div className="pane-header outline-title" title={title}>
        {title || '大纲'}
      </div>
      <nav className="outline-list">
        {headings.map((h, index) => (
          <button
            key={`${h.id}#${index}`}
            type="button"
            className={`outline-item level-${h.level}${activeId === h.id ? ' active' : ''}`}
            onClick={() => onClick(h.id)}
          >
            <span className="outline-number">{h.number}</span>
            <span className="outline-text">{h.text}</span>
          </button>
        ))}
      </nav>
    </div>
  )
}
