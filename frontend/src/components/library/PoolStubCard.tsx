import { useNavigate } from 'react-router-dom'
import { Button, Card, Typography } from '@douyinfe/semi-ui'
import { IconAIFilledLevel1 } from '@douyinfe/semi-icons'
import type { WorkSummary } from '../../types'
import { workPath } from '../../lib/routes'
import { ancestorPath, relativeTime } from '../../lib/tree'

const { Text } = Typography

/** 待建档（stub）卡：已建节点、还没写正文——一键交给 Altas 批量按骨架写。 */
export default function PoolStubCard({
  stubs,
  nodes,
  onBatch,
}: {
  stubs: WorkSummary[]
  nodes: WorkSummary[]
  onBatch: () => void
}) {
  const nav = useNavigate()
  if (stubs.length === 0) return null
  return (
    <Card
      className="pool-card"
      title={
        <span>
          待建档（Wiki）
          <Text type="tertiary" size="small" style={{ marginLeft: 8 }}>
            已建节点、还没写正文——交给 Altas 按骨架写
          </Text>
        </span>
      }
      headerExtraContent={
        <Button
          size="small"
          icon={<IconAIFilledLevel1 />}
          className="ai-coedit-btn"
          theme="solid"
          onClick={onBatch}
        >
          一键批量建档
        </Button>
      }
    >
      {stubs.map((n) => (
        <div className="ga-suggest-row" key={n.id}>
          <span className="ga-suggest-meta">
            <span className="ga-suggest-title" title={n.title}>
              {n.title}
            </span>
            <Text type="tertiary" size="small">
              {ancestorPath(nodes, n.id)
                .slice(0, -1)
                .map((p) => p.title)
                .join(' / ') || '顶级'}
              {' · '}
              {relativeTime(n.updatedAt)}
            </Text>
          </span>
          <Button size="small" theme="borderless" type="primary" onClick={() => nav(workPath(n.id))}>
            打开
          </Button>
        </div>
      ))}
    </Card>
  )
}
