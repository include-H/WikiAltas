import { useState } from 'react'
import { Button, Toast, Typography } from '@douyinfe/semi-ui'
import { IconChevronRight, IconCopy } from '@douyinfe/semi-icons'
import type { ToolStep } from '../../lib/runProjection'

const { Text } = Typography

// 工具芯片：L1 一行人话；展开是 L2 入参/结果摘要 + L3 产物预览（≤40 行，标注截断）。
export default function ToolChip({ step }: { step: ToolStep }) {
  const [open, setOpen] = useState(false)
  const hasDetail = !!(step.detail || step.artifact?.length)

  const copy = async () => {
    const text = [step.detail, step.artifact?.join('\n')].filter(Boolean).join('\n\n')
    try {
      await navigator.clipboard.writeText(text)
      Toast.success('已复制')
    } catch {
      Toast.info('浏览器不允许访问剪贴板')
    }
  }

  return (
    <div className="tool-step">
      <button
        type="button"
        className={`tool-chip${step.running ? ' is-running' : ''}${step.failed ? ' is-failed' : ''}`}
        onClick={() => hasDetail && setOpen((v) => !v)}
        aria-expanded={open}
      >
        <span className={`tool-dot${step.running ? ' running' : ''}`} aria-hidden />
        <span className="tool-chip-label">{step.label}</span>
        {hasDetail && (
          <IconChevronRight
            size="small"
            className={`tool-caret${open ? ' open' : ''}`}
          />
        )}
      </button>

      {open && hasDetail && (
        <div className="tool-detail">
          {step.detail && (
            <div className="tool-detail-section">
              <span className="tool-detail-label">摘要</span>
              <pre className="tool-detail-text">{step.detail}</pre>
            </div>
          )}
          {step.artifact?.length ? (
            <div className="tool-detail-section">
              <div className="tool-detail-head">
                <span className="tool-detail-label">产物</span>
                <Button
                  theme="borderless"
                  type="tertiary"
                  size="small"
                  icon={<IconCopy />}
                  onClick={() => void copy()}
                >
                  复制
                </Button>
              </div>
              <div className="tool-artifact">
                {step.artifact.map((line, i) => (
                  <div className="tool-artifact-line" key={`${i}-${line.slice(0, 8)}`}>
                    <span className="tool-artifact-no">{i + 1}</span>
                    <span className="tool-artifact-text">{line}</span>
                  </div>
                ))}
              </div>
              {step.artifactNote && (
                <Text type="tertiary" size="small">
                  {step.artifactNote}
                </Text>
              )}
            </div>
          ) : null}
        </div>
      )}
    </div>
  )
}
