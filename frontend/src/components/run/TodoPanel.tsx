import { useState } from 'react'
import { IconChevronDown, IconChevronUp, IconChecklistStroked } from '@douyinfe/semi-icons'
import type { RunTaskInfo } from '../../types'

// 任务面板：挂在**输入框上方**，默认收起，点开向上展开。
//
// 照 dsh 的 TodoPanel（它挂在 conversation.input.dock 上）。为什么在这儿而不是
// 时间线里：任务是"现在该做什么"，是实时状态；时间线里那条任务行是**历史**
// （那一轮当时的清单）。跟着输入框走，它才始终在视线里。
//
// 三种状态用三个图形（对齐 dsh 的 14×14 画板）：完成=实圈带勾、
// 进行中=渐隐环（CSS 转）、待办=虚线圈。颜色一律走 Semi token。

function CompletedGlyph() {
  return (
    <svg width={14} height={14} viewBox="0 0 14 14" fill="none" aria-hidden="true" className="todo-glyph-done">
      <circle cx="7" cy="7" r="6.4" stroke="currentColor" strokeWidth="1.2" />
      <path
        d="M10.9631 5.71411L7.70154 8.97571C7.48011 9.19714 7.27736 9.40099 7.09229 9.54993C6.89742 9.70669 6.66314 9.85279 6.3634 9.90027C6.2049 9.92534 6.04339 9.92534 5.88489 9.90027C5.58515 9.85279 5.35087 9.70669 5.15601 9.54993C4.97093 9.40099 4.76818 9.19714 4.54675 8.97571L3.03516 7.46411L3.96313 6.53613L5.47473 8.04773C5.7169 8.28989 5.86196 8.43389 5.97888 8.52795C6.08597 8.61409 6.10875 8.60701 6.08997 8.604C6.11259 8.60758 6.13571 8.60758 6.15833 8.604C6.13954 8.60701 6.16232 8.61409 6.26941 8.52795C6.38633 8.43389 6.53139 8.28989 6.77356 8.04773L10.0352 4.78613L10.9631 5.71411Z"
        fill="currentColor"
      />
    </svg>
  )
}

function ProgressGlyph() {
  return (
    <svg width={14} height={14} viewBox="0 0 14 14" fill="none" aria-hidden="true" className="todo-glyph-active">
      <defs>
        <linearGradient id="todo-active-grad" x1="2.5" y1="12" x2="10.5" y2="3.5" gradientUnits="userSpaceOnUse">
          <stop stopColor="currentColor" />
          <stop offset="1" stopColor="currentColor" stopOpacity="0" />
        </linearGradient>
      </defs>
      <circle cx="7" cy="7" r="6.4" stroke="url(#todo-active-grad)" strokeWidth="1.2" />
    </svg>
  )
}

function PendingGlyph() {
  return (
    <svg width={14} height={14} viewBox="0 0 14 14" fill="none" aria-hidden="true" className="todo-glyph-pending">
      <circle cx="7" cy="7" r="6.4" stroke="currentColor" strokeWidth="1.2" strokeDasharray="2.4 2.4" />
    </svg>
  )
}

function StatusGlyph({ status }: { status: string }) {
  if (status === 'completed') return <CompletedGlyph />
  if (status === 'in_progress') return <ProgressGlyph />
  return <PendingGlyph />
}

export default function TodoPanel({ tasks }: { tasks: RunTaskInfo[] }) {
  const [collapsed, setCollapsed] = useState(true)
  if (!tasks.length) return null

  const done = tasks.filter((t) => t.status === 'completed').length
  const active = tasks.filter((t) => t.status === 'in_progress').length
  const pending = tasks.length - done - active
  // 零的段落不显示——"已完成 0"是噪声（有清单就至少有一段非零）
  const progress = [
    done ? `已完成 ${done}` : '',
    active ? `进行中 ${active}` : '',
    pending ? `待办 ${pending}` : '',
  ]
    .filter(Boolean)
    .join(' · ')

  return (
    <section className="todo-dock">
      <button
        type="button"
        className="todo-head"
        aria-expanded={!collapsed}
        onClick={() => setCollapsed((v) => !v)}
      >
        <span className="todo-lead" aria-hidden>
          <IconChecklistStroked size="small" />
        </span>
        <span className="todo-title">任务</span>
        <span className="todo-progress">{progress}</span>
        <span className="todo-chevron" aria-hidden>
          {collapsed ? <IconChevronUp size="small" /> : <IconChevronDown size="small" />}
        </span>
      </button>
      {!collapsed && (
        <ul className="todo-list">
          {tasks.map((item) => (
            <li key={item.id || item.content} className="todo-item" data-status={item.status}>
              <span className="todo-glyph" aria-hidden>
                <StatusGlyph status={item.status} />
              </span>
              {/* 进行中的那条显示 activeForm（进行式），其余显示 content——
                  这正是 activeForm 这个字段存在的理由 */}
              <span className="todo-content">
                {item.status === 'in_progress' && item.activeForm ? item.activeForm : item.content}
              </span>
            </li>
          ))}
        </ul>
      )}
    </section>
  )
}
