import { useState } from 'react'
import { IconCheckCircleStroked, IconChevronDown, IconChevronUp, IconLoading } from '@douyinfe/semi-icons'
import type { DialogueStep } from '../../lib/runProjection'

interface Props {
  steps: DialogueStep[]
  /** 默认展开第几段（不传=全部收起，飞书式折叠叙事流） */
  defaultOpenIndex?: number | null
}

/**
 * 阶段芯片（默认收起，点开看工具调用）。
 *
 * Semi 的 `AIChatDialogue.Step` 一上来就全展开，且没有"默认收起"的 props
 * （`DialogueStepWidget` 里写死 `new Set(steps.map((_, i) => i))`），
 * 所以这里自绘芯片：状态图标 + 标题 + 箭头，展开后列出各次工具调用。
 * 面板与工单详情共用同一份，避免两处观感不一致。
 */
export default function RunSteps({ steps, defaultOpenIndex = null }: Props) {
  const [openIndex, setOpenIndex] = useState<number | null>(defaultOpenIndex)

  if (!steps?.length) return null

  return (
    <div className="ai-steps">
      {steps.map((step, index) => {
        const open = openIndex === index
        const actions = step.actions ?? []
        const done = step.status === 'completed'
        return (
          <div className="ai-step" key={`${step.summary}-${index}`}>
            <button
              type="button"
              className={`ai-step-chip${open ? ' is-open' : ''}`}
              aria-expanded={open}
              disabled={actions.length === 0}
              onClick={() => setOpenIndex(open ? null : index)}
            >
              <span className={`ai-step-icon${done ? ' is-done' : ' is-running'}`}>
                {done ? <IconCheckCircleStroked size="small" /> : <IconLoading size="small" />}
              </span>
              <span className="ai-step-title">{step.summary}</span>
              {actions.length > 0 && (
                <span className="ai-step-count">
                  {actions.length}
                  {open ? <IconChevronUp size="small" /> : <IconChevronDown size="small" />}
                </span>
              )}
            </button>
            {open && actions.length > 0 && (
              <div className="ai-step-actions">
                {actions.map((action, i) => (
                  <div className="ai-step-action" key={`${action.summary}-${i}`}>
                    <span className="ai-step-action-summary">{action.summary}</span>
                    {action.description && (
                      <span className="ai-step-action-desc" title={action.description}>
                        {action.description}
                      </span>
                    )}
                  </div>
                ))}
              </div>
            )}
          </div>
        )
      })}
    </div>
  )
}
