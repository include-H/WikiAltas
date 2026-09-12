import { useEffect, useMemo, useRef, useState } from 'react'
import type { ReactNode } from 'react'
import { IconChevronRight } from '@douyinfe/semi-icons'
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

interface Node {
  h: OutlineHeading
  children: Node[]
}

/** 平铺的标题列表 → 树：按 level 归属（3 级标题挂到它上面最近的 2 级…）。 */
function buildTree(headings: OutlineHeading[]): Node[] {
  const roots: Node[] = []
  const stack: Node[] = []
  for (const h of headings) {
    const node: Node = { h, children: [] }
    while (stack.length && stack[stack.length - 1].h.level >= h.level) stack.pop()
    if (stack.length) stack[stack.length - 1].children.push(node)
    else roots.push(node)
    stack.push(node)
  }
  return roots
}

export default function OutlinePane({
  headings,
  title,
  scrollTargetId = 'content-scroll',
  contentKey,
}: Props) {
  const [activeId, setActiveId] = useState<string | null>(null)
  // 默认折叠：长文档先给骨架，只露顶层章节；点谁展开谁。
  const [expanded, setExpanded] = useState<Set<string>>(new Set())
  // 程序滚动期间的锁：即时滚动没有中途扫过的过程，短锁只是防跳变瞬间闪一下。
  const lockUntil = useRef(0)
  // 观察者给的回调闭包里 activeId 会过期，用 ref 做个镜像。
  const activeRef = useRef<string | null>(null)
  activeRef.current = activeId
  // 防抖：相邻标题在观察带边界会来回触发，稳定 ~180ms 才真正切换高亮
  // （以前是来了就切，滚动时高亮两行之间来回闪——用户原话"抖动"）。
  const pendingRef = useRef<{ id: string; timer: number } | null>(null)

  const tree = useMemo(() => buildTree(headings), [headings])

  // 活跃标题可能在收起的分支里（没渲染）：把高亮落到它**最近的可见祖先**上，
  // 这样滚动全程大纲里始终有个位置指示，而不是一会儿有、一会儿"无"。
  const visibleActiveId = useMemo(() => {
    if (!activeId) return null
    const path: string[] = []
    const walk = (nodes: Node[], acc: string[]): boolean => {
      for (const n of nodes) {
        const next = [...acc, n.h.id]
        if (n.h.id === activeId) {
          path.push(...next)
          return true
        }
        if (walk(n.children, next)) return true
      }
      return false
    }
    if (!walk(tree, [])) return activeId
    for (let i = path.length - 1; i >= 0; i--) {
      let rendered = true
      for (let j = 0; j < i; j++) {
        if (!expanded.has(path[j])) {
          rendered = false
          break
        }
      }
      if (rendered) return path[i]
    }
    return null
  }, [activeId, tree, expanded])

  const scrollTo = (id: string) => {
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
    // **即时到位，不做平滑滚动**。长文里点一条目录要跨几屏，平滑滚动会把
    // 整页甩过去一秒多——视觉上就是"容器像在放大/抖动"（用户原话），
    // 而且点击的语义本来就是"同时看到那一节"。
    scroller.scrollTo({ top: Math.max(0, top), behavior: 'auto' })
    setActiveId(id)
  }

  const onRowClick = (node: Node) => {
    // 点击即展开（折叠是默认态）；已经展开的不回折——点击的主语义是"去这一节"。
    if (node.children.length) {
      setExpanded((prev) => (prev.has(node.h.id) ? prev : new Set(prev).add(node.h.id)))
    }
    // 即时滚动没有中途扫过的过程，短锁只是防 observer 在跳变瞬间闪一下
    lockUntil.current = Date.now() + 300
    scrollTo(node.h.id)
  }

  const toggle = (id: string) => {
    setExpanded((prev) => {
      const next = new Set(prev)
      if (next.has(id)) next.delete(id)
      else next.add(id)
      return next
    })
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
        if (Date.now() < lockUntil.current) return // 程序滚动进行中，别抢高亮
        const visible = entries
          .filter((e) => e.isIntersecting)
          .sort((a, b) => a.boundingClientRect.top - b.boundingClientRect.top)
        const id = visible[0]?.target.id
        if (!id) return
        if (id === activeRef.current) {
          // 回到了当前项：撤掉尚未兑现的候选
          if (pendingRef.current) {
            window.clearTimeout(pendingRef.current.timer)
            pendingRef.current = null
          }
          return
        }
        if (pendingRef.current?.id === id) return // 同一候选还在等待期内
        if (pendingRef.current) window.clearTimeout(pendingRef.current.timer)
        pendingRef.current = {
          id,
          timer: window.setTimeout(() => {
            setActiveId(id)
            pendingRef.current = null
          }, 180),
        }
      },
      {
        root: scroller,
        rootMargin: '-10% 0px -70% 0px',
        threshold: [0, 1],
      },
    )
    for (const el of elements) observer.observe(el)
    return () => {
      observer.disconnect()
      if (pendingRef.current) {
        window.clearTimeout(pendingRef.current.timer)
        pendingRef.current = null
      }
    }
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

  const renderNode = (node: Node, depth: number): ReactNode => {
    const h = node.h
    const open = expanded.has(h.id)
    const hasKids = node.children.length > 0
    return (
      <div key={`${h.id}#${depth}`}>
        <div
          className={`outline-item level-${h.level}${visibleActiveId === h.id ? ' active' : ''}`}
          style={{ paddingLeft: 8 + depth * 14 }}
          role="button"
          tabIndex={0}
          onClick={() => onRowClick(node)}
          onKeyDown={(e) => {
            if (e.key === 'Enter' || e.key === ' ') {
              e.preventDefault()
              onRowClick(node)
            }
          }}
        >
          {hasKids ? (
            <span
              className={`outline-caret${open ? ' is-open' : ''}`}
              role="button"
              aria-label={open ? '折叠' : '展开'}
              onClick={(e) => {
                e.stopPropagation()
                toggle(h.id)
              }}
            >
              <IconChevronRight size="small" />
            </span>
          ) : (
            <span className="outline-caret is-leaf" aria-hidden />
          )}
          <span className="outline-number">{h.number}</span>
          <span className="outline-text">{h.text}</span>
        </div>
        {hasKids && open && node.children.map((c) => renderNode(c, depth + 1))}
      </div>
    )
  }

  return (
    <div className="outline-pane">
      <div className="pane-header outline-title" title={title}>
        {title || '大纲'}
      </div>
      <nav className="outline-list">{tree.map((n) => renderNode(n, 0))}</nav>
    </div>
  )
}
